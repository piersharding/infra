package access

import (
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

func ListGroupMappings(rCtx RequestContext) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "group mapping", "list", models.InfraAdminRole)
	}
	return nil
}

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

func CreateGroupMapping(rCtx RequestContext, mapping *models.GroupMapping) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "group mapping", "create", models.InfraAdminRole)
	}
	return data.CreateGroupMapping(rCtx.DBTxn, mapping)
}

func UpdateGroupMapping(rCtx RequestContext, id uid.ID, mapping *models.GroupMapping) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "group mapping", "update", models.InfraAdminRole)
	}
	mapping.ID = id
	return data.UpdateGroupMapping(rCtx.DBTxn, mapping)
}

func DeleteGroupMapping(rCtx RequestContext, id uid.ID) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "group mapping", "delete", models.InfraAdminRole)
	}
	return data.DeleteGroupMapping(rCtx.DBTxn, id)
}
