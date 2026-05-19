package server

import (
	"fmt"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/access"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
)

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

func (a *API) GetGroupMapping(rCtx access.RequestContext, r *api.Resource) (*api.GroupMapping, error) {
	mapping, err := access.GetGroupMapping(rCtx, r.ID)
	if err != nil {
		return nil, err
	}

	return mapping.ToAPI(), nil
}

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

	return mapping.ToAPI(), nil
}

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
	return mapping.ToAPI(), nil
}

func (a *API) DeleteGroupMapping(rCtx access.RequestContext, r *api.Resource) (*api.EmptyResponse, error) {
	return nil, access.DeleteGroupMapping(rCtx, r.ID)
}
