package server

import (
	"fmt"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/access"
	"github.com/infrahq/infra/internal/logging"
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
		m := mapping
		return *m.ToAPI()
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

// CreateMappingRule adds a new mapping rule and immediately triggers EvaluateMappingRules
// because a new rule can match already-existing groups, creating new access grants.
// Requires InfraAdminRole.
func (a *API) CreateMappingRule(rCtx access.RequestContext, r *api.CreateMappingRuleRequest) (*api.MappingRule, error) {
	mapping := &models.MappingRule{
		RuleName:          r.RuleName,
		SourceGroupRegex:  r.SourceGroupRegex,
		DestinationType:   models.DestinationType(r.DestinationType),
		NameTemplate:      r.NameTemplate,
		NamespaceTemplate: r.NamespaceTemplate,
		RoleTemplate:      r.RoleTemplate,
	}

	if err := access.CreateMappingRule(rCtx, mapping); err != nil {
		return nil, fmt.Errorf("create mapping rule: %w", err)
	}

	// A new mapping rule can match already-existing groups, so we re-evaluate to
	// immediately create the corresponding access grants. Errors are logged but not returned.
	// The mapping is still saved; a subsequent evaluation will catch up.
	if err := EvaluateMappingRules(rCtx.DBTxn); err != nil {
		logging.L.Warn().Err(err).Str("rule", r.RuleName).Msg("error evaluating group mappings after creating mapping")
	}

	return mapping.ToAPI(), nil
}

// UpdateMappingRule modifies an existing mapping rule and immediately triggers EvaluateMappingRules
// because changed rules can alter which groups have access.
// Requires InfraAdminRole.
func (a *API) UpdateMappingRule(rCtx access.RequestContext, r *api.UpdateMappingRuleRequest) (*api.MappingRule, error) {
	mapping := &models.MappingRule{
		RuleName:          r.RuleName,
		SourceGroupRegex:  r.SourceGroupRegex,
		DestinationType:   models.DestinationType(r.DestinationType),
		NameTemplate:      r.NameTemplate,
		NamespaceTemplate: r.NamespaceTemplate,
		RoleTemplate:      r.RoleTemplate,
	}

	if err := access.UpdateMappingRule(rCtx, r.ID, mapping); err != nil {
		return nil, fmt.Errorf("update mapping rule: %w", err)
	}

	mapping.ID = r.ID

	// The updated mapping may now match different groups or produce different resource names,
	// so we re-evaluate to update the grant set accordingly. Errors are logged but not returned.
	if err := EvaluateMappingRules(rCtx.DBTxn); err != nil {
		logging.L.Warn().Err(err).Str("rule", r.RuleName).Msg("error evaluating group mappings after updating mapping")
	}

	return mapping.ToAPI(), nil
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
	if err := EvaluateMappingRules(rCtx.DBTxn); err != nil {
		logging.L.Warn().Err(err).Str("mapping_id", r.ID.String()).Msg("error evaluating group mappings after deleting mapping")
	}

	return &api.EmptyResponse{}, nil
}
