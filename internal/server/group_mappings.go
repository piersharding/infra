package server

import (
	"fmt"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/access"
	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
)

// ListGroupMappings lists all group mapping rules with optional name filtering and pagination.
// Requires InfraAdminRole. Returns an empty list (not 404) when no mappings exist.
func (a *API) ListGroupMappings(rCtx access.RequestContext, r *api.ListGroupMappingsRequest) (*api.ListResponse[api.GroupMapping], error) {
	if err := access.ListGroupMappings(rCtx); err != nil {
		return nil, err
	}

	p := PaginationFromRequest(r.PaginationRequest)

	opts := data.ListGroupMappingsOptions{
		Name:       r.Name,
		Pagination: &p,
	}
	mappings, err := data.ListGroupMappings(rCtx.DBTxn, opts)
	if err != nil {
		return nil, err
	}

	result := api.NewListResponse(mappings, PaginationToResponse(p), func(mapping models.GroupMapping) api.GroupMapping {
		m := mapping
		return *m.ToAPI()
	})

	return result, nil
}

// GetGroupMapping retrieves a single group mapping rule by ID.
// Requires InfraAdminRole. Returns 404 if the rule doesn't exist.
func (a *API) GetGroupMapping(rCtx access.RequestContext, r *api.Resource) (*api.GroupMapping, error) {
	mapping, err := access.GetGroupMapping(rCtx, r.ID)
	if err != nil {
		return nil, err
	}

	return mapping.ToAPI(), nil
}

// CreateGroupMapping adds a new mapping rule and immediately triggers EvaluateGroupMappings
// because a new rule can match already-existing groups, creating new access grants.
// Requires InfraAdminRole.
func (a *API) CreateGroupMapping(rCtx access.RequestContext, r *api.CreateGroupMappingRequest) (*api.GroupMapping, error) {
	mapping := &models.GroupMapping{
		RuleName:          r.RuleName,
		SourceGroupRegex:  r.SourceGroupRegex,
		DestinationType:   models.DestinationType(r.DestinationType),
		NameTemplate:      r.NameTemplate,
		NamespaceTemplate: r.NamespaceTemplate,
		RoleTemplate:      r.RoleTemplate,
	}

	if err := access.CreateGroupMapping(rCtx, mapping); err != nil {
		return nil, fmt.Errorf("create group mapping: %w", err)
	}

	// A new mapping rule can match already-existing groups, so we re-evaluate to
	// immediately create the corresponding access grants. Errors are logged but not returned.
	// The mapping is still saved; a subsequent evaluation will catch up.
	if err := EvaluateGroupMappings(rCtx.DBTxn); err != nil {
		logging.L.Warn().Err(err).Str("rule", r.RuleName).Msg("error evaluating group mappings after creating mapping")
	}

	return mapping.ToAPI(), nil
}

// UpdateGroupMapping modifies an existing mapping rule and immediately triggers EvaluateGroupMappings
// because changed rules can alter which groups have access.
// Requires InfraAdminRole.
func (a *API) UpdateGroupMapping(rCtx access.RequestContext, r *api.UpdateGroupMappingRequest) (*api.GroupMapping, error) {
	mapping := &models.GroupMapping{
		RuleName:          r.RuleName,
		SourceGroupRegex:  r.SourceGroupRegex,
		DestinationType:   models.DestinationType(r.DestinationType),
		NameTemplate:      r.NameTemplate,
		NamespaceTemplate: r.NamespaceTemplate,
		RoleTemplate:      r.RoleTemplate,
	}

	if err := access.UpdateGroupMapping(rCtx, r.ID, mapping); err != nil {
		return nil, fmt.Errorf("update group mapping: %w", err)
	}

	mapping.ID = r.ID

	// The updated mapping may now match different groups or produce different resource names,
	// so we re-evaluate to update the grant set accordingly. Errors are logged but not returned.
	if err := EvaluateGroupMappings(rCtx.DBTxn); err != nil {
		logging.L.Warn().Err(err).Str("rule", r.RuleName).Msg("error evaluating group mappings after updating mapping")
	}

	return mapping.ToAPI(), nil
}

// DeleteGroupMapping soft-deletes a mapping rule and triggers EvaluateGroupMappings to
// remove any auto-granted access that was produced by this now-removed rule.
// Requires InfraAdminRole.
func (a *API) DeleteGroupMapping(rCtx access.RequestContext, r *api.Resource) (*api.EmptyResponse, error) {
	if err := access.DeleteGroupMapping(rCtx, r.ID); err != nil {
		return nil, err
	}

	// Deleting a mapping can revoke access for previously-matched groups, so we
	// immediately re-run the engine which will detect and remove stale auto-grants.
	if err := EvaluateGroupMappings(rCtx.DBTxn); err != nil {
		logging.L.Warn().Err(err).Str("mapping_id", r.ID.String()).Msg("error evaluating group mappings after deleting mapping")
	}

	return &api.EmptyResponse{}, nil
}
