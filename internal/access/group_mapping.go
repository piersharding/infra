package access

import (
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// ListGroupMappings checks authorization for listing group mappings.
// Requires InfraAdminRole — only admins can view mapping rules since they define access policies.
func ListGroupMappings(rCtx RequestContext) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "group mapping", "list", models.InfraAdminRole)
	}
	return nil
}

// GetGroupMapping checks authorization and fetches a single group mapping by ID.
// Requires InfraAdminRole. Returns the model directly (no RBAC check on individual rules needed).
func GetGroupMapping(rCtx RequestContext, id uid.ID) (*models.GroupMapping, error) {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return nil, HandleAuthErr(err, "group mapping", "get", models.InfraAdminRole)
	}

	opts := data.GetGroupMappingOptions{ByID: id}
	mapping, err := data.GetGroupMapping(rCtx.DBTxn, opts)
	if err != nil {
		return nil, err
	}

	return mapping, nil
}

// CreateGroupMapping checks authorization and delegates to the data layer.
// Requires InfraAdminRole — creating a rule can grant access to new groups, so it's admin-only.
func CreateGroupMapping(rCtx RequestContext, mapping *models.GroupMapping) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "group mapping", "create", models.InfraAdminRole)
	}
	return data.CreateGroupMapping(rCtx.DBTxn, mapping)
}

// UpdateGroupMapping checks authorization and delegates to the data layer.
// Requires InfraAdminRole — modifying a rule can change which groups have access.
func UpdateGroupMapping(rCtx RequestContext, id uid.ID, mapping *models.GroupMapping) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "group mapping", "update", models.InfraAdminRole)
	}
	mapping.ID = id
	return data.UpdateGroupMapping(rCtx.DBTxn, mapping)
}

// DeleteGroupMapping checks authorization and delegates to the data layer.
// Requires InfraAdminRole — deleting a rule can revoke access from groups.
func DeleteGroupMapping(rCtx RequestContext, id uid.ID) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "group mapping", "delete", models.InfraAdminRole)
	}
	return data.DeleteGroupMapping(rCtx.DBTxn, id)
}
