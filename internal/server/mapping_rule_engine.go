package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/infrahq/infra/internal"
	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// EvaluateMappingRules evaluates all active mapping rules and creates or updates grants to match.
// It iterates over mapping rules, matches them against groups using regex patterns,
// and creates group-level grants for SSH and Kubernetes destinations. Stale grants
// (created by this engine that no longer have a matching rule) are cleaned up automatically.
// EvaluateMappingRules evaluates all active mapping rules and creates or updates grants to match.
// It uses a database-level advisory lock (scoped by organization_id) to prevent concurrent
// evaluations from racing on grant creation. If the lock cannot be acquired immediately,
// the function returns nil (another evaluation is in progress doing the same work).
func EvaluateMappingRules(tx data.WriteTxn) error {
	// Acquire an xact-scoped advisory lock keyed by organization ID.
	// pg_try_advisory_xact_lock auto-releases on transaction end, so no explicit unlock needed.
	lockKey := int64(tx.OrganizationID())
	var locked bool
	if err := tx.QueryRow("SELECT pg_try_advisory_xact_lock($1)", lockKey).Scan(&locked); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	if !locked {
		// Another evaluation is in progress for this org — skip to avoid race.
		logging.L.Debug().Msg("skipping EvaluateMappingRules: another instance holds the lock")
		return nil
	}

	mappings, err := data.ListMappingRules(tx, data.ListMappingRulesOptions{})
	if err != nil {
		return fmt.Errorf("list mapping rules: %w", err)
	}

	var validMappings []models.MappingRule
	for _, m := range mappings {
		if !m.DeletedAt.Valid {
			validMappings = append(validMappings, m)
		}
	}

	groups, err := data.ListGroups(tx, data.ListGroupsOptions{})
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}

	for _, mapping := range validMappings {
		re, err := regexp.Compile(mapping.SourceGroupRegex)
		if err != nil {
			logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Msg("invalid regex in group mapping")
			continue
		}

		for _, g := range groups {
			matches := re.FindStringSubmatch(g.Name)
			if matches == nil {
				continue // no match — skip this group for this rule
			}

			resourceName, err := applyTemplate(mapping.NameTemplate, g.Name, re)
			if err != nil {
				logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to apply destination name template")
				continue
			}

			var privilege string
			switch models.DestinationType(mapping.DestinationType) {
			case models.DestinationTypeSSH:
				privilege = "connect"
			case models.DestinationTypeKubernetes:
				if mapping.RoleTemplate != nil && *mapping.RoleTemplate != "" {
					role, err := applyTemplate(*mapping.RoleTemplate, g.Name, re)
					if err != nil {
						logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to apply role template")
						continue
					}
					privilege = role
				} else {
					privilege = "view" // Infra requires a valid RBAC role; "view" is the least-privileged option
				}
			default:
				logging.L.Warn().Str("type", string(mapping.DestinationType)).Msg("unknown destination type")
				continue
			}

			if err := createOrUpdateGrant(tx, tx.OrganizationID(), mapping.DestinationType, g.ID, privilege, resourceName); err != nil {
				logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to create grant")
			}

			// Kubernetes destinations can be scoped to specific namespaces via NamespaceTemplate.
			// When set, the engine queries the destination for its live namespace list,
			// compiles the template result as a regex pattern, and creates one grant per
			// matching namespace: resource = "<cluster>.<namespace>".
			if models.DestinationType(mapping.DestinationType) == models.DestinationTypeKubernetes && mapping.NamespaceTemplate != nil && *mapping.NamespaceTemplate != "" {
				// First apply $N capture group references in NamespaceTemplate.
				extendedNS, err := applyTemplate(*mapping.NamespaceTemplate, g.Name, re)
				if err != nil {
					logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to expand namespace template with capture groups")
					continue
				}

				nsResourceNames, err := expandNamespaceTemplate(tx, resourceName, extendedNS)
				if err != nil {
					logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to expand namespace template")
					continue
				}

				for _, nsResource := range nsResourceNames {
					if err := createOrUpdateGrant(tx, tx.OrganizationID(), mapping.DestinationType, g.ID, privilege, nsResource); err != nil {
						logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to create namespaced grant")
					}
				}
			}
		}
	}

	return cleanupStaleGrants(tx)
}

// EvalStatusReport captures the result of the most recent EvaluateMappingRulesAsync
// invocation for an organization. Stored in-memory and exposed via the API so the
// UI can display whether the last evaluation succeeded or failed.
type EvalStatusReport struct {
	LastRunAt time.Time `json:"last_run_at"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
}

// evalStatusStore is an in-memory store of the last evaluation result per org.
// Keyed by orgID uid.ID; values are *EvalStatusReport. Populated by
// EvaluateMappingRulesAsync and read by GetEvalStatus.
var evalStatusStore sync.Map

// recordEvalStatus stores the outcome of an EvaluateMappingRulesAsync run.
// Called by the background goroutine after evaluation completes.
func recordEvalStatus(orgID uid.ID, err error) {
	s := &EvalStatusReport{
		LastRunAt: time.Now(),
		Success:   err == nil,
	}
	if err != nil {
		s.Error = err.Error()
	}
	evalStatusStore.Store(orgID, s)
}

// GetEvalStatus returns the last known evaluation result for the given org,
// or nil if no evaluation has been recorded yet.
func GetEvalStatus(orgID uid.ID) *EvalStatusReport {
	val, ok := evalStatusStore.Load(orgID)
	if !ok {
		return nil
	}
	return val.(*EvalStatusReport)
}

// EvaluateMappingRulesAsync runs EvaluateMappingRules in a background goroutine
// with its own database transaction, scoped to the given organization. Errors are
// logged but not returned — the caller is free to respond to the HTTP request
// without waiting for evaluation to complete. On completion, the result is
// recorded in the in-memory evalStatusStore and visible via GetEvalStatus.
func EvaluateMappingRulesAsync(db *data.DB, orgID uid.ID) {
	go func() {
		tx, err := db.Begin(context.Background(), nil)
		if err != nil {
			logging.L.Warn().Err(err).Msg("async mapping eval: begin txn")
			recordEvalStatus(orgID, err)
			return
		}
		defer tx.Rollback()
		if err := EvaluateMappingRules(tx.WithOrgID(orgID)); err != nil {
			logging.L.Warn().Err(err).Msg("async mapping eval failed")
			recordEvalStatus(orgID, err)
			return
		}
		if err := tx.Commit(); err != nil {
			logging.L.Warn().Err(err).Msg("async mapping eval: commit")
			recordEvalStatus(orgID, err)
			return
		}
		recordEvalStatus(orgID, nil)
	}()
}

// applyTemplate substitutes $N references in a template string with the Nth capture group
// from a regex match on the given input. Supports multi-digit references ($10, $25).
// Only bare numeric references like $1 are supported — ${...} syntax is rejected.
func applyTemplate(template, input string, re *regexp.Regexp) (string, error) {
	matches := re.FindStringSubmatch(input)
	if matches == nil || len(matches) == 0 {
		return "", fmt.Errorf("regex did not match")
	}

	var result strings.Builder
	for i := 0; i < len(template); i++ {
		ch := template[i]
		if ch == '$' && i+1 < len(template) {
			nextCh := template[i+1]
			if nextCh >= '0' && nextCh <= '9' {
				// Parse the full capture group reference number.
				numStart := i + 1
				for numStart+1 < len(template) && template[numStart+1] >= '0' && template[numStart+1] <= '9' {
					numStart++
				}
				refNumStr := template[i+1 : numStart+1]

				if refNum, err := parseCaptureRef(refNumStr); err != nil {
					return "", fmt.Errorf("invalid capture reference %s: %w", refNumStr, err)
				} else if refNum >= 0 && refNum < len(matches) {
					result.WriteString(matches[refNum])
				} else {
					// Unmatched reference → empty string (as per spec).
					result.WriteString("")
				}

				i = numStart // skip past the number
			} else if nextCh == '{' {
				// Reject any ${...} pattern — only $N (bare number) syntax is supported
				return "", fmt.Errorf("invalid template syntax: ${...} is not supported; use $N instead")
			} else {
				result.WriteByte(ch)
			}
		} else {
			result.WriteByte(ch)
		}
	}

	return result.String(), nil
}

// parseCaptureRef converts a digit string like "2" or "10" into its integer value.
// Returns an error if the string contains non-digit characters or is "0"
// (capture references are 1-indexed per regexp.SubexpIndex).
func parseCaptureRef(s string) (int, error) {
	var n int
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-digit character in capture reference")
		}
		n = n*10 + int(ch-'0')
	}
	if n == 0 {
		return 0, fmt.Errorf("capture group references must be 1-indexed (got $%s)", s)
	}
	return n, nil
}

// createOrUpdateGrant ensures a grant exists for the given subject, privilege, and resource name.
// It first checks if a matching grant already exists (same group + privilege + resource).
// If found, it marks the existing grant as auto-granted and returns early — avoiding duplicate grants
// when EvaluateMappingRules is called multiple times. Otherwise it creates a new grant with:
//
//   - CreatedBy = "system"   (distinguishes engine-created grants from user-managed ones)
//   - AutoGrant = true       (marks this as safe for cleanupStaleGrants to remove)
//   - Subject = Group(subjectID)  (mapping rules always grant access to groups, not individual users)
func createOrUpdateGrant(tx data.WriteTxn, orgID uid.ID, destType models.DestinationType, subjectID uid.ID, privilege, resource string) error {
	preExisting, err := data.GetGrant(tx, data.GetGrantOptions{
		BySubject:   models.NewSubjectForGroup(subjectID),
		ByPrivilege: privilege,
		ByResource:  resource,
	})
	found := true
	if err != nil {
		if errors.Is(err, internal.ErrNotFound) {
			found = false // No existing grant — proceed to create one below.
		} else {
			return fmt.Errorf("check existing grant for subject %s (priv=%s, res=%s): %w", subjectID.String(), privilege, resource, err)
		}
	}
	if found {
		_, _ = tx.Exec(`UPDATE grants SET auto_grant = true WHERE id = ?`, preExisting.ID)
		return nil // grant already covers this subject → nothing to do
	}

	if err := data.CreateGrant(tx, &models.Grant{
		Model:              models.Model{}, // will be set by OnInsert
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		CreatedBy:          models.CreatedBySystem,
		AutoGrant:          true, // marks this as a mapping-engine auto-grant (safe to clean up)
		Subject:            models.NewSubjectForGroup(subjectID),
		Privilege:          privilege,
		Resource:           resource,
	}); err != nil {
		return fmt.Errorf("create grant for subject %s (priv=%s, res=%s): %w", subjectID.String(), privilege, resource, err)
	}

	return nil
}

// cleanupStaleGrants runs after EvaluateMappingRules to remove stale auto-granted access
// for rules that are being removed or changed. It queries only grants with AutoGrant=true
// (set exclusively by the mapping engine) whose resource name doesn't match any active
// mapping rule.
//
// Key design: only grants marked as auto-grants can be cleaned up — bootstrap/manual grants
// always have AutoGrant=false and are never touched by the engine.
// expandNamespaceTemplate queries the K8s destination matching clusterName (derived from
// NameTemplate), compiles expandedPattern as a Go regex pattern against each namespace
// in the destination's Resources list, and returns one resource string per match.
// Each result has the form "<cluster>.<namespace>". If the destination doesn't exist
// or is disconnected, it logs a warning and returns nil (graceful degradation).
func expandNamespaceTemplate(tx data.WriteTxn, clusterName, expandedPattern string) ([]string, error) {
	destinations, err := data.ListDestinations(tx, data.ListDestinationsOptions{ByKind: "kubernetes", ByName: clusterName})
	if err != nil {
		return nil, fmt.Errorf("query k8s destinations for %q: %w", clusterName, err)
	}

	if len(destinations) == 0 {
		logging.L.Warn().Str("cluster", clusterName).Msg("k8s destination not found; skipping namespace expansion")
		return nil, nil
	}

	dest := &destinations[0]

	// Compile the expanded template result as a Go regex pattern against dest.Resources.
	patternRegex, err := regexp.Compile(expandedPattern)
	if err != nil {
		logging.L.Warn().Str("cluster", clusterName).Str("pattern", expandedPattern).Msg("invalid namespace pattern regex")
		return nil, fmt.Errorf("compile namespace pattern %q as regex: %w", expandedPattern, err)
	}

	var matchedResources []string
	for _, ns := range dest.Resources {
		if patternRegex.MatchString(ns) {
			matchedResources = append(matchedResources, clusterName+"."+ns)
		}
	}

	return matchedResources, nil
}

func cleanupStaleGrants(tx data.WriteTxn) error {
	grants, err := data.ListAutoGrants(tx)
	if err != nil {
		return fmt.Errorf("list auto-grants for cleanup: %w", err)
	}

	// Fetch active mappings once — avoiding a database query per grant.
	mappings, err := data.ListMappingRules(tx, data.ListMappingRulesOptions{})
	if err != nil {
		return fmt.Errorf("list group mappings for cleanup: %w", err)
	}

	// Pre-compile all mapping rule regexes to avoid recompiling them per-grant.
	precompiled := make(map[string]*regexp.Regexp, len(mappings))
	for _, m := range mappings {
		if re, err := regexp.Compile(m.SourceGroupRegex); err == nil {
			precompiled[m.RuleName] = re
		}
	}

	for _, grant := range grants {
		// Only clean up auto-grants created by previous mapping rules.
		// Grants with AutoGrant=false are bootstrap or manual access — always preserve them.
		if !grant.AutoGrant {
			continue
		}

		// Check if the group that owns this grant is still covered by an active mapping rule.
		matched := false

		// Get the group name to check against mapping rule patterns.
		grpName := ""
		if grant.Subject.Kind == models.SubjectKindGroup && grant.Subject.ID != 0 {
			grp, err2 := data.GetGroup(tx, data.GetGroupOptions{ByID: grant.Subject.ID})
			if err2 == nil && grp != nil {
				grpName = grp.Name
			}
		}

		for _, m := range mappings {
			if m.OrganizationID == tx.OrganizationID() && !m.DeletedAt.Valid {
				re, ok := precompiled[m.RuleName]
				if !ok || grpName == "" {
					continue
				}
				// If this grant's group matches the rule pattern and would produce a matching resource,
				// keep it. For namespaced K8s grants (resource.namespace), check both base resource
				// AND that the namespace part still exists in the destination's live Resources list.
				if re.MatchString(grpName) {
					baseResource := grant.Resource
					namespacePart := ""
					if idx := strings.LastIndex(grant.Resource, "."); idx != -1 {
						baseResource = grant.Resource[:idx]
						namespacePart = grant.Resource[idx+1:]
					}

					matchedRes, err3 := applyTemplate(m.NameTemplate, grpName, re)
					if err3 != nil {
						continue
					}

					// Base resource must match the cluster name.
					if baseResource != matchedRes {
						continue
					}

					matched = true

					// For namespaced K8s grants, also verify the namespace part still exists in the destination.
					if m.DestinationType == models.DestinationTypeKubernetes && namespacePart != "" {
						namespaces, err4 := data.ListDestinations(tx, data.ListDestinationsOptions{ByKind: "kubernetes", ByName: matchedRes})
						if err4 != nil || len(namespaces) == 0 {
							// Destination not found — grant is stale.
							matched = false
							continue
						}

						dest := namespaces[0]
						namespaceFound := false
						for _, ns := range dest.Resources {
							if ns == namespacePart {
								namespaceFound = true
								break
							}
						}

						// Also verify the rule's NamespaceTemplate still matches at least one live namespace.
						hasActiveRule := true
						if m.NamespaceTemplate != nil && *m.NamespaceTemplate != "" {
							expanded, err5 := applyTemplate(*m.NamespaceTemplate, grpName, re)
							if err5 == nil {
								patternRegex, err6 := regexp.Compile(expanded)
								if err6 == nil {
									hasMatch := false
									for _, ns := range dest.Resources {
										if patternRegex.MatchString(ns) {
											hasMatch = true
											break
										}
									}
									if !hasMatch {
										hasActiveRule = false
									}
								}
							}
						}

						// Grant is valid only if namespace exists AND rule still has active matches.
						if !namespaceFound || !hasActiveRule {
							matched = false
						} else {
							matched = true
						}
					}
				}
			}
		}

		if !matched {
			if err := data.DeleteGrants(tx, data.DeleteGrantsOptions{ByID: grant.ID}); err != nil {
				logging.L.Warn().Err(err).Str("grant", grant.ID.String()).Msg("failed to delete stale auto-grant")
			}
		}
	}

	return nil
}
