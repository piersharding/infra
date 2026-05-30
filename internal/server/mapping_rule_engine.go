package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal"
	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/data/querybuilder"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// EvaluateMappingRules processes all active mapping rules against the current set of groups,
// creating or updating grants where a group's name matches a rule's SourceGroupRegex.
// It first acquires an xact-scoped PostgreSQL advisory lock to prevent concurrent evaluations
// for the same organization, then iterates each non-deleted mapping rule and matching group
// to produce auto-grants. Finally it runs cleanupStaleGrants to remove stale access.
func EvaluateMappingRules(tx data.WriteTxn) error {
	orgID := tx.OrganizationID()
	// Acquire an xact-scoped advisory lock keyed by organization ID.
	// pg_try_advisory_xact_lock auto-releases on transaction end, so no explicit unlock needed.
	lockKey := int64(orgID)
	var locked bool
	if err := tx.QueryRow("SELECT pg_try_advisory_xact_lock($1)", lockKey).Scan(&locked); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	if !locked {
		// Another evaluation is in progress for this org — skip to avoid race.
		logging.L.Debug().Msg("skipping EvaluateMappingRules: another instance holds the lock")
		return nil
	}

	// Clear mapping rule grants cache for this org before a fresh run.
	MRGrantsCache.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, orgID.String()+":") {
			MRGrantsCache.Delete(key)
		}
		return true
	})

	mappings, err := data.ListMappingRules(tx, data.ListMappingRulesOptions{})
	if err != nil {
		return fmt.Errorf("list mapping rules: %w", err)
	}

	validMappings := filterValidMappings(mappings)

	groups, err := data.ListGroups(tx, data.ListGroupsOptions{})
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}

	// Ensure every rule has a cache entry (even with zero matched groups) so
	// the grant-counting path can always find it.
	for _, mapping := range validMappings {
		ruleKey := fmt.Sprintf("%s:%s", tx.OrganizationID().String(), mapping.ID)
		if _, ok := MRGrantsCache.Load(ruleKey); !ok {
			MRGrantsCache.Store(ruleKey, []api.MappingRuleGrant{})
		}
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

			evaluateRuleForGroup(tx, tx.OrganizationID(), mapping, g, re)
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

// MRGrantsCache tracks, for each mapping rule in a given org,
// which group names matched its SourceGroupRegex and their grant details
// during the last evaluation. Keyed by (orgID.String(), rule.ID) → []MappingRuleGrant.
var MRGrantsCache sync.Map

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
		autoGrantQuery := querybuilder.New("UPDATE grants")
		autoGrantQuery.B("SET auto_grant = ?", true)
		autoGrantQuery.B("WHERE id = ?", preExisting.ID)
		_, err := tx.Exec(autoGrantQuery.String(), autoGrantQuery.Args...)
		if err != nil {
			return fmt.Errorf("mark grant as auto-granted: %w", err)
		}
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

// fetchK8sDestsByClusterName looks up K8s destinations by cluster name.
func fetchK8sDestination(tx data.WriteTxn, clusterName string) ([]models.Destination, error) {
	destinations, err := data.ListDestinations(tx, data.ListDestinationsOptions{ByKind: "kubernetes", ByName: clusterName})
	if err != nil {
		return nil, fmt.Errorf("query k8s destinations for %q: %w", clusterName, err)
	}

	if len(destinations) == 0 {
		logging.L.Warn().Str("cluster", clusterName).Msg("k8s destination not found; skipping namespace expansion")
	}

	return destinations, nil
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
	destinations, err := fetchK8sDestination(tx, clusterName)
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

// filterValidMappings returns only non-deleted mappings from the given list.
func filterValidMappings(mappings []models.MappingRule) []models.MappingRule {
	var valid []models.MappingRule
	for _, m := range mappings {
		if !m.DeletedAt.Valid {
			valid = append(valid, m)
		}
	}
	return valid
}

// resolvePrivilege determines the privilege strings for a mapping rule.
// SSH always maps to ["connect"]; K8s uses RoleTemplate (applied via template), falling back to ["view"].
// For Kubernetes, if the resolved role is exactly "admin", it is translated to "cluster-admin"
// because the built-in ClusterRole "admin" only provides namespace-level permissions,
// while "cluster-admin" grants full cluster-wide access as intended by admin rules.
// aivadmin returns both its own privilege and admin in addition.
func resolvePrivilege(mapping models.MappingRule, grpName string, re *regexp.Regexp) ([]string, error) {
	switch models.DestinationType(mapping.DestinationType) {
	case models.DestinationTypeSSH:
		return []string{"connect"}, nil
	case models.DestinationTypeKubernetes:
		if mapping.RoleTemplate != nil && *mapping.RoleTemplate != "" {
			role, err := applyTemplate(*mapping.RoleTemplate, grpName, re)
			if err != nil {
				return nil, fmt.Errorf("apply role template: %w", err)
			}
			// aivadmin includes admin in addition to its own privilege.
			if role == "aivadmin" {
				return []string{"aivadmin", "admin"}, nil
			}
			// Translate admin → cluster-admin for full cluster-wide access.
			if role == "admin" {
				role = "cluster-admin"
			}
			return []string{role}, nil
		}
		return []string{"view"}, nil // least-privileged valid RBAC role
	default:
		return nil, fmt.Errorf("unknown destination type: %s", mapping.DestinationType)
	}
}

// createNamespacedGrants creates auto-grants for each namespaced resource.
func createNamespacedGrants(tx data.WriteTxn, orgID uid.ID, destType models.DestinationType, subjectID uid.ID, privilege string, nsResources []string) {
	for _, nsResource := range nsResources {
		if err := createOrUpdateGrant(tx, orgID, destType, subjectID, privilege, nsResource); err != nil {
			logging.L.Warn().Err(err).Str("group", subjectID.String()).Msg("failed to create namespaced grant")
		}
	}
}

// evaluateRuleForGroup processes a single mapping rule against a matching group.
func evaluateRuleForGroup(tx data.WriteTxn, orgID uid.ID, mapping models.MappingRule, g models.Group, re *regexp.Regexp) {
	ruleKey := fmt.Sprintf("%s:%s", orgID.String(), mapping.ID)

	resourceName, err := applyTemplate(mapping.NameTemplate, g.Name, re)
	if err != nil {
		logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to apply destination name template")
		return
	}

	privileges, err := resolvePrivilege(mapping, g.Name, re)
	if err != nil {
		logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to resolve privilege")
		return
	}

	// Resolve destination info for UI linking.
	var destInfo *models.Destination
	dests, err := data.ListDestinations(tx, data.ListDestinationsOptions{
		ByKind: string(mapping.DestinationType),
		ByName: resourceName,
	})
	if err != nil {
		logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Msg("failed to resolve destination for UI linking")
	} else if len(dests) > 0 {
		destInfo = &dests[0]
	}

	// Track matched grants with full details for the UI.
	addMatchedGrant := func(privilege, resource string) {
		var grants []api.MappingRuleGrant
		if val, ok := MRGrantsCache.Load(ruleKey); ok && val != nil {
			grants = val.([]api.MappingRuleGrant)
		}
		for _, priv := range privileges {
			grantInfo := api.MappingRuleGrant{
				GroupID:   g.ID,
				GroupName: g.Name,
				Privilege: priv,
				Resource:  resource,
			}
			if destInfo != nil {
				grantInfo.DestinationID = destInfo.ID
			}
			grants = append(grants, grantInfo)
		}
		MRGrantsCache.Store(ruleKey, grants)
	}

	if models.DestinationType(mapping.DestinationType) == models.DestinationTypeKubernetes && mapping.NamespaceTemplate != nil && *mapping.NamespaceTemplate != "" {
		extendedNS, err := applyTemplate(*mapping.NamespaceTemplate, g.Name, re)
		if err != nil {
			logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to expand namespace template with capture groups")
			return
		}

		nsResourceNames, err := expandNamespaceTemplate(tx, resourceName, extendedNS)
		if err != nil {
			logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to expand namespace template")
			return
		}

		for _, nsResource := range nsResourceNames {
			for _, priv := range privileges {
				if err := createOrUpdateGrant(tx, orgID, mapping.DestinationType, g.ID, priv, nsResource); err != nil {
					logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to create namespaced grant")
				}
			}
			addMatchedGrant(privileges[0], nsResource)
		}
	} else {
		for _, priv := range privileges {
			if err := createOrUpdateGrant(tx, orgID, mapping.DestinationType, g.ID, priv, resourceName); err != nil {
				logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to create grant")
			}
		}
		addMatchedGrant(privileges[0], resourceName)
	}
}

// k8sDestCache memoizes K8s destination lookups by cluster name.
type k8sDestCache struct {
	items map[string][]models.Destination // clusterName → destinations
}

// newK8sDestCache creates a fresh K8s destination cache for the current evaluation scope.
// The cache is short-lived — it covers only one cleanupStaleGrants call.
func newK8sDestCache() *k8sDestCache {
	return &k8sDestCache{items: make(map[string][]models.Destination)}
}

// getK8sDestsByClusterName returns K8s destinations matching the given cluster name,
// fetching from DB on cache miss. Delegates to fetchK8sDestination for actual I/O.
func (c *k8sDestCache) getK8sDestsByClusterName(tx data.WriteTxn, clusterName string) ([]models.Destination, error) {
	if dests, ok := c.items[clusterName]; ok {
		return dests, nil
	}
	dests, err := fetchK8sDestination(tx, clusterName)
	if err != nil {
		return nil, err
	}
	c.items[clusterName] = dests
	return dests, nil
}

// cleanupStaleGrantsContext holds the setup state needed to validate grants against active rules.
type cleanupStaleGrantsContext struct {
	grants      []models.Grant            // auto-granted access entries to validate
	rules       []models.MappingRule      // non-deleted mapping rules (pre-sorted)
	precompiled map[string]*regexp.Regexp // ruleName → compiled regex, only for valid SourceGroupRegex values
}

// loadCleanupState fetches auto-grants, active mappings, and pre-compiles their regexes.
func loadCleanupState(tx data.WriteTxn) (*cleanupStaleGrantsContext, error) {
	grants, err := data.ListAutoGrants(tx)
	if err != nil {
		return nil, fmt.Errorf("list auto-grants for cleanup: %w", err)
	}

	mappings, err := data.ListMappingRules(tx, data.ListMappingRulesOptions{})
	if err != nil {
		return nil, fmt.Errorf("list group mappings for cleanup: %w", err)
	}

	precompiled := make(map[string]*regexp.Regexp, len(mappings))
	for _, m := range mappings {
		if re, err := regexp.Compile(m.SourceGroupRegex); err == nil {
			precompiled[m.RuleName] = re
		}
	}

	return &cleanupStaleGrantsContext{
		grants:      grants,
		rules:       mappings,
		precompiled: precompiled,
	}, nil
}

// getGroupByID looks up a group name by ID.
func getGroupByID(tx data.ReadTxn, id uid.ID) string {
	grp, err := data.GetGroup(tx, data.GetGroupOptions{ByID: id})
	if err == nil && grp != nil {
		return grp.Name
	}
	return ""
}

// isGrantStillValid checks whether a single auto-grant is still covered by any active mapping rule.
func (ctx *cleanupStaleGrantsContext) isGrantStillValid(tx data.WriteTxn, grant models.Grant, grpName string, destCache *k8sDestCache) bool {
	for _, m := range ctx.rules {
		if !ctx.ruleAppliesToOrg(tx, m) || m.DeletedAt.Valid {
			continue
		}
		re, ok := ctx.precompiled[m.RuleName]
		if !ok || grpName == "" {
			continue
		}
		if !ruleMatchesGrant(grant, re, m, grpName) {
			continue
		}
		if _, nsValid := ctx.checkNamespacedK8s(tx, grant, re, m, grpName, destCache); !nsValid {
			return false
		}
		return true
	}
	return false
}

// ruleAppliesToOrg checks whether a mapping rule belongs to the current organization
// and has not been soft-deleted.
func (ctx *cleanupStaleGrantsContext) ruleAppliesToOrg(tx data.WriteTxn, m models.MappingRule) bool {
	return m.OrganizationID == tx.OrganizationID()
}

// ruleMatchesGrant checks whether a grant's base resource matches the NameTemplate output
// of an active mapping rule whose SourceGroupRegex also matches the group name.
func ruleMatchesGrant(grant models.Grant, re *regexp.Regexp, m models.MappingRule, grpName string) bool {
	if !re.MatchString(grpName) {
		return false
	}
	matchedRes, err := applyTemplate(m.NameTemplate, grpName, re)
	if err != nil {
		return false
	}
	baseResource := grant.Resource
	if idx := strings.LastIndex(grant.Resource, "."); idx != -1 {
		baseResource = grant.Resource[:idx]
	}
	return baseResource == matchedRes
}

// checkNamespacedK8s validates a namespaced K8s grant during cleanup:
//  1. The cluster destination must still exist.
//  2. The namespace portion of the grant's resource must be in the dest's Resources list,
//     or if NamespaceTemplate is set, the expanded template pattern must match at least one namespace.
func (ctx *cleanupStaleGrantsContext) checkNamespacedK8s(tx data.WriteTxn, grant models.Grant, re *regexp.Regexp, m models.MappingRule, grpName string, destCache *k8sDestCache) (*models.Destination, bool) {
	if m.DestinationType != models.DestinationTypeKubernetes || !strings.Contains(grant.Resource, ".") {
		return nil, true
	}

	namespace := grant.Resource[strings.LastIndex(grant.Resource, ".")+1:]
	baseResource := grant.Resource[:strings.LastIndex(grant.Resource, ".")]

	dests, err := destCache.getK8sDestsByClusterName(tx, baseResource)
	if err != nil || len(dests) == 0 {
		return nil, false
	}
	dest := &dests[0]

	namespaceFound := false
	for _, ns := range dest.Resources {
		if ns == namespace {
			namespaceFound = true
			break
		}
	}
	if !namespaceFound {
		return dest, false
	}

	if m.NamespaceTemplate != nil && *m.NamespaceTemplate != "" {
		extended, err := applyTemplate(*m.NamespaceTemplate, grpName, re)
		if err == nil {
			patternRegex, err2 := regexp.Compile(extended)
			if err2 == nil {
				hasMatch := false
				for _, ns := range dest.Resources {
					if patternRegex.MatchString(ns) {
						hasMatch = true
						break
					}
				}
				if !hasMatch {
					return dest, false
				}
			}
		}
	}

	return dest, true
}

// cleanupStaleGrants removes auto-granted access that no longer matches any active mapping rule.
// It also cleans up orphaned group rows (IDP-synced groups with zero identity members)
// that don't match any active mapping rule regex.
func cleanupStaleGrants(tx data.WriteTxn) error {
	ctx, err := loadCleanupState(tx)
	if err != nil {
		return err
	}

	destCache := newK8sDestCache()

	for _, grant := range ctx.grants {
		if !grant.AutoGrant {
			continue
		}

		var grpName string
		if grant.Subject.Kind == models.SubjectKindGroup && grant.Subject.ID != 0 {
			grpName = getGroupByID(tx, grant.Subject.ID)
		}

		matched := ctx.isGrantStillValid(tx, grant, grpName, destCache)
		if !matched {
			if err := data.DeleteGrants(tx, data.DeleteGrantsOptions{ByID: grant.ID}); err != nil {
				logging.L.Warn().Err(err).Str("grant", grant.ID.String()).Msg("failed to delete stale auto-grant")
			}
		}
	}

	// Clean up orphaned group rows that no longer match any active mapping rule.
	if err := cleanupOrphanedGroups(tx, ctx.rules); err != nil {
		logging.L.Warn().Err(err).Msg("failed to clean up orphaned groups")
	}

	return nil
}

// cleanupOrphanedGroups removes group rows synced from an IDP that don't match any active mapping rule.
func cleanupOrphanedGroups(tx data.WriteTxn, rules []models.MappingRule) error {
	// Build combined regex from all active mapping rule patterns.
	parts := make([]string, len(rules))
	for i, r := range rules {
		parts[i] = r.SourceGroupRegex
	}
	combinedPattern := strings.Join(parts, "|")

	// When there are no active mapping rules, all IDP-synced groups become orphans.
	var combinedRe *regexp.Regexp
	if combinedPattern != "" {
		var compileErr error
		combinedRe, compileErr = regexp.Compile(combinedPattern)
		if compileErr != nil {
			return fmt.Errorf("compile combined mapping rule regex for orphan cleanup: %w", compileErr)
		}
	}

	// Find all IDP-synced groups (created_by_provider != 0) that are not soft-deleted.
	rows, err := tx.Query(`SELECT id, name FROM groups WHERE deleted_at IS NULL AND organization_id = ? AND created_by_provider <> 0`, tx.OrganizationID())
	if err != nil {
		return fmt.Errorf("query orphaned groups: %w", err)
	}

	// Collect all group IDs that need to be deleted first — we can't call DeleteGroup while iterating,
	// because Go's database/sql doesn't allow multiple open result sets on one connection.
	var idsToDelete []uid.ID
	for rows.Next() {
		var groupID uid.ID
		var name string
		if err := rows.Scan(&groupID, &name); err != nil {
			logging.L.Warn().Err(err).Str("group", name).Msg("failed to scan orphaned group")
			continue
		}

		// If there are no rules or this group doesn't match any rule, mark for deletion.
		if combinedRe == nil || !combinedRe.MatchString(name) {
			idsToDelete = append(idsToDelete, groupID)
		}
	}
	rows.Close()

	// Now delete all marked groups after the cursor is closed.
	for _, groupID := range idsToDelete {
		grpName := getGroupByID(tx, groupID)
		logging.L.Info().Str("group", grpName).Msg("deleted orphaned group: not locally created and does not match any mapping rule")
		if err := data.DeleteGroup(tx, groupID); err != nil {
			logging.L.Warn().Err(err).Str("group", grpName).Msg("failed to delete orphaned group")
		}
	}

	return nil
}
