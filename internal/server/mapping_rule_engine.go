package server

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// EvaluateMappingRules evaluates all active mapping rules and creates or updates grants to match.
// It iterates over mapping rules, matches them against groups using regex patterns,
// and creates group-level grants for SSH and Kubernetes destinations. Stale grants
// (created by this engine that no longer have a matching rule) are cleaned up automatically.
func EvaluateMappingRules(tx data.WriteTxn) error {
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

	if len(validMappings) == 0 {
		// No active mapping rules — nothing to clean up against.
		// Do not delete grants that were created through other flows (e.g., signup).
		return nil
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
					privilege = "view" // fallback for k8s with no role_template
				}
			default:
				logging.L.Warn().Str("type", string(mapping.DestinationType)).Msg("unknown destination type")
				continue
			}

			if err := createOrUpdateGrant(tx, tx.OrganizationID(), mapping.DestinationType, g.ID, privilege, resourceName); err != nil {
				logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to create grant")
			}

			// For kubernetes with namespace_template, also create a namespaced grant.
			if models.DestinationType(mapping.DestinationType) == models.DestinationTypeKubernetes && mapping.NamespaceTemplate != nil && *mapping.NamespaceTemplate != "" {
				namespaceName, err := applyTemplate(*mapping.NamespaceTemplate, g.Name, re)
				if err != nil {
					logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to apply namespace template")
					continue
				}

				nsResource := fmt.Sprintf("%s.%s", resourceName, namespaceName)
				if err := createOrUpdateGrant(tx, tx.OrganizationID(), mapping.DestinationType, g.ID, privilege, nsResource); err != nil {
					logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to create namespaced grant")
				}
			}

			// For SSH destinations with matched groups, also create a group-level
			// grant (same pattern as Kubernetes). Grants are given to the group,
			// not individual members — access flows through group membership.
			if models.DestinationType(mapping.DestinationType) == models.DestinationTypeSSH {
				if err := createOrUpdateGrant(tx, tx.OrganizationID(), mapping.DestinationType, g.ID, privilege, resourceName); err != nil {
					logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to create group SSH grant")
				}
			}
		}
	}

	return cleanupStaleGrants(tx)
}

// applyTemplate substitutes $N references in a template string with the Nth capture group
// from a regex match on the given input.
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

// createOrUpdateGrant creates or updates a grant with the specified privilege and resource
// for the given destination type. Grants are created with CreatedBy="system" and AutoGrant=true so they can be
// identified and cleaned up by cleanupStaleGrants.
func createOrUpdateGrant(tx data.WriteTxn, orgID uid.ID, destType models.DestinationType, subjectID uid.ID, privilege, resource string) error {
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

	if destType == models.DestinationTypeKubernetes {
		// For k8s, the connector polls grants by group name and cluster name.
		// The grant's Subject.ID is the group ID; Resource is the cluster name or cluster.namespace.
	} else if destType == models.DestinationTypeSSH {
		// For SSH, privilege must be "connect" and resource is the destination Name.
	}

	return nil
}

// cleanupStaleGrants runs after EvaluateMappingRules to remove auto-granted access for rules
// that are being removed or changed. It iterates all grants and deletes only those with
// CreatedBy="system" AND AutoGrant=true whose resource name doesn't match the pattern of any active mapping rule.
//
// Key design: only grants explicitly marked as auto-grants (AutoGrant=true) from previous mapping rules
// are cleaned up. Bootstrap-created grants (CreatedBy="system", AutoGrant=false) and manually created grants
// (CreatedBy != "system") are always preserved — the engine never touches user-managed access or initial admin setup.
func cleanupStaleGrants(tx data.WriteTxn) error {
	grants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	if err != nil {
		return fmt.Errorf("list grants for cleanup: %w", err)
	}

	for _, grant := range grants {
		if grant.CreatedBy != models.CreatedBySystem {
			continue // skip manually-created grants
		}

		// Only clean up auto-grants created by previous mapping rules.
		// Grants with AutoGrant=false are bootstrap or manual access — always preserve them.
		if !grant.AutoGrant {
			continue
		}

		// Check if this grant's resource still matches any active mapping.
		matched := false
		mappings, err := data.ListMappingRules(tx, data.ListMappingRulesOptions{})
		if err != nil {
			return fmt.Errorf("list group mappings for cleanup: %w", err)
		}

		for _, m := range mappings {
			if m.OrganizationID == tx.OrganizationID() && !m.DeletedAt.Valid {
				re, err := regexp.Compile(m.SourceGroupRegex)
				if err != nil {
					continue
				}
				// If the grant resource matches what this mapping would generate, keep it.
				ok := re.FindStringSubmatch(grant.Resource)
				matched = matched || (ok != nil && len(ok) > 0)
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
