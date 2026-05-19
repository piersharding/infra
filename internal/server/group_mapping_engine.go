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

// EvaluateGroupMappings is a BackgroundJobFunc that evaluates all group mappings
// for every organization and creates/upgrades grants accordingly.
func EvaluateGroupMappings(tx data.WriteTxn) error {
	orgs, err := data.ListOrganizations(tx, data.ListOrganizationsOptions{})
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}

	for _, org := range orgs {
		if err := evaluateMappingsForOrg(tx, org.ID); err != nil {
			logging.L.Warn().Err(err).Str("org_id", org.ID.String()).Msg("error evaluating group mappings")
		}
	}

	return nil
}

func evaluateMappingsForOrg(tx data.WriteTxn, orgID uid.ID) error {
	mappings, err := data.ListGroupMappings(tx, data.ListGroupMappingsOptions{})
	if err != nil {
		return fmt.Errorf("list group mappings: %w", err)
	}

	var validMappings []models.GroupMapping
	for _, m := range mappings {
		if m.OrganizationID == orgID && !m.DeletedAt.Valid {
			validMappings = append(validMappings, m)
		}
	}

	if len(validMappings) == 0 {
		return cleanupStaleGrants(tx, orgID)
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
				logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to apply name template")
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

			if err := createOrUpdateGrant(tx, orgID, mapping.DestinationType, g.ID, privilege, resourceName); err != nil {
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
				if err := createOrUpdateGrant(tx, orgID, mapping.DestinationType, g.ID, privilege, nsResource); err != nil {
					logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to create namespaced grant")
				}
			}

			// For SSH destinations with matched groups containing members, also expand
			// into per-user grants so the SSH connector picks them up.
			if models.DestinationType(mapping.DestinationType) == models.DestinationTypeSSH {
				members, err := data.ListGroupMembers(tx, g.ID)
				if err != nil {
					logging.L.Warn().Err(err).Str("group", g.Name).Msg("failed to list group members")
					continue
				}

				for _, memberID := range members {
					if err := createOrUpdateGrant(tx, orgID, mapping.DestinationType, memberID, privilege, resourceName); err != nil {
						logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("user", memberID.String()).Msg("failed to create per-user SSH grant")
					}
				}
			}
		}
	}

	return cleanupStaleGrants(tx, orgID)
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

// createOrUpdateGrant creates a grant for the given subject.
func createOrUpdateGrant(tx data.WriteTxn, orgID uid.ID, destType models.DestinationType, subjectID uid.ID, privilege, resource string) error {
	if err := data.CreateGrant(tx, &models.Grant{
		Model:              models.Model{}, // will be set by OnInsert
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		CreatedBy:          models.CreatedBySystem,
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

// cleanupStaleGrants removes grants created by this engine that no longer have a matching rule.
func cleanupStaleGrants(tx data.WriteTxn, orgID uid.ID) error {
	grants, err := data.ListAllGrants(tx, orgID)
	if err != nil {
		return fmt.Errorf("list grants for cleanup: %w", err)
	}

	for _, grant := range grants {
		if grant.CreatedBy != models.CreatedBySystem {
			continue // skip manually-created grants
		}

		// Check if this grant's resource still matches any active mapping.
		matched := false
		mappings, err := data.ListGroupMappings(tx, data.ListGroupMappingsOptions{})
		if err != nil {
			return fmt.Errorf("list group mappings for cleanup: %w", err)
		}

		for _, m := range mappings {
			if m.OrganizationID == orgID && !m.DeletedAt.Valid {
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
			if err := data.DeleteGrant(tx, grant.ID); err != nil {
				logging.L.Warn().Err(err).Str("grant", grant.ID.String()).Msg("failed to delete stale auto-grant")
			}
		}
	}

	return nil
}

// EvaluateGroupMappingForUser evaluates group mappings for a specific user's groups.
func EvaluateGroupMappingForUser(tx data.WriteTxn, userID uid.ID) error {
	orgs, err := data.ListOrganizations(tx, data.ListOrganizationsOptions{})
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}

	for _, org := range orgs {
		if err := evaluateMappingsForUserInOrg(tx, userID, org.ID); err != nil {
			logging.L.Warn().Err(err).Str("org_id", org.ID.String()).Msg("error evaluating user group mappings")
		}
	}

	return nil
}

func evaluateMappingsForUserInOrg(tx data.WriteTxn, userID uid.ID, orgID uid.ID) error {
	mappings, err := data.ListGroupMappings(tx, data.ListGroupMappingsOptions{})
	if err != nil {
		return fmt.Errorf("list group mappings: %w", err)
	}

	var validMappings []models.GroupMapping
	for _, m := range mappings {
		if m.OrganizationID == orgID && !m.DeletedAt.Valid {
			validMappings = append(validMappings, m)
		}
	}

	if len(validMappings) == 0 {
		return cleanupStaleUserGrants(tx, userID, orgID)
	}

	groups, err := data.ListGroups(tx, data.ListGroupsOptions{})
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}

	for _, mapping := range validMappings {
		re, err := regexp.Compile(mapping.SourceGroupRegex)
		if err != nil {
			continue
		}

		for _, g := range groups {
			matches := re.FindStringSubmatch(g.Name)
			if matches == nil {
				continue
			}

			// Check if this user is a member of the group.
			isMember, err := data.IsGroupMember(tx, userID, g.ID)
			if err != nil || !isMember {
				continue
			}

			resourceName, err := applyTemplate(mapping.NameTemplate, g.Name, re)
			if err != nil {
				logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("group", g.Name).Msg("failed to apply name template")
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
					privilege = "view"
				}
			default:
				logging.L.Warn().Str("type", string(mapping.DestinationType)).Msg("unknown destination type")
				continue
			}

			if err := createOrUpdateUserGrant(tx, orgID, mapping.DestinationType, userID, privilege, resourceName); err != nil {
				logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("user", userID.String()).Msg("failed to create user grant")
			}

			if models.DestinationType(mapping.DestinationType) == models.DestinationTypeKubernetes && mapping.NamespaceTemplate != nil && *mapping.NamespaceTemplate != "" {
				namespaceName, err := applyTemplate(*mapping.NamespaceTemplate, g.Name, re)
				if err != nil {
					continue
				}

				if err := createOrUpdateUserGrant(tx, orgID, mapping.DestinationType, userID, privilege, fmt.Sprintf("%s.%s", resourceName, namespaceName)); err != nil {
					logging.L.Warn().Err(err).Str("rule", mapping.RuleName).Str("user", userID.String()).Msg("failed to create namespaced user grant")
				}
			}
		}
	}

	return cleanupStaleUserGrants(tx, userID, orgID)
}

func createOrUpdateUserGrant(tx data.WriteTxn, orgID uid.ID, destType models.DestinationType, subjectID uid.ID, privilege, resource string) error {
	if err := data.CreateGrant(tx, &models.Grant{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		CreatedBy:          models.CreatedBySystem,
		Subject:            models.NewSubjectForUser(subjectID),
		Privilege:          privilege,
		Resource:           resource,
	}); err != nil {
		return fmt.Errorf("create user grant for subject %s (priv=%s, res=%s): %w", subjectID.String(), privilege, resource, err)
	}
	return nil
}

func cleanupStaleUserGrants(tx data.WriteTxn, userID uid.ID, orgID uid.ID) error {
	grants, err := data.ListAllGrants(tx, orgID)
	if err != nil {
		return fmt.Errorf("list grants for user cleanup: %w", err)
	}

	for _, grant := range grants {
		if grant.CreatedBy != models.CreatedBySystem {
			continue
		}
		if grant.Subject.Kind == 0 && grant.Subject.ID == userID { // User subject kind = 0
			if err := data.DeleteGrant(tx, grant.ID); err != nil {
				logging.L.Warn().Err(err).Str("grant", grant.ID.String()).Msg("failed to delete stale user auto-grant")
			}
		}
	}

	return nil
}
