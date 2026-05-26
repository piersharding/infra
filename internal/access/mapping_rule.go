package access

import (
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// ListMappingRules checks authorization for listing mapping rules.
// Requires InfraAdminRole — mapping rules control access policies.
func ListMappingRules(rCtx RequestContext) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "mapping rule", "list", models.InfraAdminRole)
	}
	return nil
}

// GetMappingRule checks authorization and fetches a single mapping rule by ID.
// Requires InfraAdminRole — mapping rules control access policies.
func GetMappingRule(rCtx RequestContext, id uid.ID) (*models.MappingRule, error) {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return nil, HandleAuthErr(err, "mapping rule", "get", models.InfraAdminRole)
	}

	opts := data.GetMappingRuleOptions{ByID: id}
	mapping, err := data.GetMappingRule(rCtx.DBTxn, opts)
	if err != nil {
		return nil, err
	}

	return mapping, nil
}

// CreateMappingRule checks authorization and delegates to the data layer.
// Requires InfraAdminRole — creating a rule can grant access to new groups, so it's admin-only.
func CreateMappingRule(rCtx RequestContext, mapping *models.MappingRule) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "mapping rule", "create", models.InfraAdminRole)
	}
	return data.CreateMappingRule(rCtx.DBTxn, mapping)
}

// UpdateMappingRule checks authorization and delegates to the data layer.
// Requires InfraAdminRole — modifying a rule can change which groups have access.
func UpdateMappingRule(rCtx RequestContext, id uid.ID, mapping *models.MappingRule) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "mapping rule", "update", models.InfraAdminRole)
	}
	mapping.ID = id
	return data.UpdateMappingRule(rCtx.DBTxn, mapping)
}

// GetMappingRuleEvalStatus checks authorization for reading the evaluation status.
// Requires InfraAdminRole — mapping rules control access policies.
func GetMappingRuleEvalStatus(rCtx RequestContext) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "mapping rule", "get", models.InfraAdminRole)
	}
	return nil
}

// DeleteMappingRule checks authorization and delegates to the data layer.
// Requires InfraAdminRole — deleting a rule can revoke access from groups.
func DeleteMappingRule(rCtx RequestContext, id uid.ID) error {
	if err := IsAuthorized(rCtx, models.InfraAdminRole); err != nil {
		return HandleAuthErr(err, "mapping rule", "delete", models.InfraAdminRole)
	}
	return data.DeleteMappingRule(rCtx.DBTxn, id)
}
