package server

import (
	"fmt"
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
		var matchedGrants []api.MappingRuleGrant
		if val, ok := MRGrantsCache.Load(ruleKey); ok && val != nil {
			matchedGrants = val.([]api.MappingRuleGrant)
		}
		m := mapping
		apiRule := *m.ToAPI()
		apiRule.MatchedGrants = matchedGrants
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
// Requires InfraAdminRole.
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
