package server

import (
	"fmt"
	"slices"
	"strings"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/access"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
)

// ListMappingRules lists all mapping rules with optional name filtering and pagination.
// Requires InfraAdminRole. Returns an empty list (not 404) when no mappings exist.
func (a *API) ListMappingRules(rCtx access.RequestContext, r *api.ListMappingRulesRequest) (*api.ListResponse[api.MappingRule], error) {
	if err := access.ListMappingRules(rCtx); err != nil {
		return nil, err
	}

	p := PaginationFromRequest(r.PaginationRequest)

	opts := data.ListMappingRulesOptions{
		Name:       r.Name,
		Pagination: &p,
	}
	mappings, err := data.ListMappingRules(rCtx.DBTxn, opts)
	if err != nil {
		return nil, err
	}

	result := api.NewListResponse(mappings, PaginationToResponse(p), func(mapping models.MappingRule) api.MappingRule {
		ruleKey := fmt.Sprintf("%s:%s", rCtx.DBTxn.OrganizationID().String(), mapping.ID)
		var grantsCount int
		if val, ok := matchedGroupsCache.Load(ruleKey); ok && val != nil {
			matchedGroupNames := val.([]string)
			autoGrants, err := data.ListAutoGrants(rCtx.DBTxn)
			if err == nil {
				matchedSet := make(map[string]bool, len(matchedGroupNames))
				for _, name := range matchedGroupNames {
					matchedSet[name] = true
				}
				for _, grant := range autoGrants {
					if grant.Subject.Kind != models.SubjectKindGroup || grant.Subject.ID == 0 {
						continue
					}
					grpName := getGroupByID(rCtx.DBTxn, grant.Subject.ID)
					if matchedSet[grpName] {
						grantsCount++
					}
				}
			}
		}
		m := mapping
		apiRule := *m.ToAPI()
		apiRule.GrantsCount = grantsCount
		return apiRule
	})

	return result, nil
}

// GetMappingRule retrieves a single mapping rule by ID.
// Requires InfraAdminRole. Returns 404 if the rule doesn't exist.
func (a *API) GetMappingRule(rCtx access.RequestContext, r *api.Resource) (*api.MappingRule, error) {
	mapping, err := access.GetMappingRule(rCtx, r.ID)
	if err != nil {
		return nil, err
	}

	return mapping.ToAPI(), nil
}

// anchorRegex ensures the regex is anchored with ^...$ to prevent partial matches.
// If already anchored on either side, only the missing anchor is added.
func anchorRegex(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if !strings.HasPrefix(s, "^") {
		s = "^" + s
	}
	if !strings.HasSuffix(s, "$") {
		s = s + "$"
	}
	return s
}

// expandGlobPtr applies glob wildcard expansion to a pointer-to-string, returning nil for nil input.
func expandGlobPtr(s *string) *string {
	if s == nil {
		return nil
	}
	r := strings.ReplaceAll(*s, "*", ".*")
	return &r
}

// CreateMappingRule adds a new mapping rule and immediately triggers EvaluateMappingRules
// because a new rule can match already-existing groups, creating new access grants.
// Requires InfraAdminRole.
func (a *API) CreateMappingRule(rCtx access.RequestContext, r *api.CreateMappingRuleRequest) (*api.MappingRule, error) {
	mapping := &models.MappingRule{
		RuleName:          r.RuleName,
		SourceGroupRegex:  anchorRegex(r.SourceGroupRegex),
		DestinationType:   models.DestinationType(r.DestinationType),
		NameTemplate:      r.NameTemplate,
		NamespaceTemplate: expandGlobPtr(r.NamespaceTemplate),
		RoleTemplate:      r.RoleTemplate,
	}

	if err := access.CreateMappingRule(rCtx, mapping); err != nil {
		return nil, fmt.Errorf("create mapping rule: %w", err)
	}

	// A new mapping rule can match already-existing groups, so we re-evaluate to
	// immediately create the corresponding access grants.
	EvaluateMappingRulesAsync(rCtx.DataDB, rCtx.DBTxn.OrganizationID())

	return mapping.ToAPI(), nil
}

// UpdateMappingRule modifies an existing mapping rule and immediately triggers EvaluateMappingRules
// because changed rules can alter which groups have access.
// Requires InfraAdminRole.
func (a *API) UpdateMappingRule(rCtx access.RequestContext, r *api.UpdateMappingRuleRequest) (*api.MappingRule, error) {
	mapping := &models.MappingRule{
		RuleName:          r.RuleName,
		SourceGroupRegex:  anchorRegex(r.SourceGroupRegex),
		DestinationType:   models.DestinationType(r.DestinationType),
		NameTemplate:      r.NameTemplate,
		NamespaceTemplate: expandGlobPtr(r.NamespaceTemplate),
		RoleTemplate:      r.RoleTemplate,
	}

	if err := access.UpdateMappingRule(rCtx, r.ID, mapping); err != nil {
		return nil, fmt.Errorf("update mapping rule: %w", err)
	}

	mapping.ID = r.ID

	// The updated mapping may now match different groups or produce different resource names,
	// so we re-evaluate to update the grant set accordingly.
	EvaluateMappingRulesAsync(rCtx.DataDB, rCtx.DBTxn.OrganizationID())

	return mapping.ToAPI(), nil
}

// GetMappingRuleEvalStatus returns the result of the last async evaluation.
// Requires InfraViewRole — same as listing rules.
func (a *API) GetMappingRuleEvalStatus(rCtx access.RequestContext, _ *api.EmptyRequest) (*api.MappingRuleEvalStatus, error) {
	if err := access.GetMappingRuleEvalStatus(rCtx); err != nil {
		return nil, err
	}
	status := GetEvalStatus(rCtx.DBTxn.OrganizationID())
	if status == nil {
		return &api.MappingRuleEvalStatus{}, nil
	}
	return &api.MappingRuleEvalStatus{
		LastRunAt: api.Time(status.LastRunAt),
		Success:   status.Success,
		Error:     status.Error,
	}, nil
}

// DeleteMappingRule soft-deletes a mapping rule and triggers EvaluateMappingRules to
// remove any auto-granted access that was produced by this now-removed rule.
// Requires InfraAdminRole.
func (a *API) DeleteMappingRule(rCtx access.RequestContext, r *api.Resource) (*api.EmptyResponse, error) {
	if err := access.DeleteMappingRule(rCtx, r.ID); err != nil {
		return nil, err
	}

	// Deleting a mapping can revoke access for previously-matched groups, so we
	// immediately re-run the engine which will detect and remove stale auto-grants.
	EvaluateMappingRulesAsync(rCtx.DataDB, rCtx.DBTxn.OrganizationID())

	return &api.EmptyResponse{}, nil
}

// GetMappingRuleGrants returns all auto-grants that were created by the given mapping rule.
// Requires InfraAdminRole. Returns grants sorted by subject name, then resource.
func (a *API) GetMappingRuleGrants(rCtx access.RequestContext, r *api.Resource) (*api.ListMappingRuleGrantsResponse, error) {
	if err := access.GetMappingRuleEvalStatus(rCtx); err != nil {
		return nil, err
	}

	autoGrants, err := data.ListAutoGrants(rCtx.DBTxn)
	if err != nil {
		return nil, fmt.Errorf("list auto grants: %w", err)
	}

	ruleKey := fmt.Sprintf("%s:%s", rCtx.DBTxn.OrganizationID().String(), r.ID)
	val, ok := matchedGroupsCache.Load(ruleKey)
	if !ok || val == nil {
		return &api.ListMappingRuleGrantsResponse{Count: 0, Items: []api.MappingRuleGrant{}}, nil
	}

	matchedGroupNames := val.([]string)
	matchedSet := make(map[string]bool, len(matchedGroupNames))
	for _, name := range matchedGroupNames {
		matchedSet[name] = true
	}

	var result []api.MappingRuleGrant
	for _, grant := range autoGrants {
		if grant.Subject.Kind != models.SubjectKindGroup || grant.Subject.ID == 0 {
			continue
		}
		grpName := getGroupByID(rCtx.DBTxn, grant.Subject.ID)
		if !matchedSet[grpName] {
			continue
		}
		result = append(result, api.MappingRuleGrant{
			ID:        grant.ID,
			Subject:   grpName,
			Privilege: grant.Privilege,
			Resource:  grant.Resource,
		})
	}

	slices.SortFunc(result, func(a, b api.MappingRuleGrant) int {
		if c := strings.Compare(a.Subject, b.Subject); c != 0 {
			return c
		}
		return strings.Compare(a.Resource, b.Resource)
	})

	return &api.ListMappingRuleGrantsResponse{Count: len(result), Items: result}, nil
}
