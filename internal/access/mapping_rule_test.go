// Tests for mapping rule access layer authorization checks.
package access

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/internal/testing/database"
	"github.com/infrahq/infra/internal/testing/patch"
	"github.com/infrahq/infra/uid"
)

func setupMappingRuleTestDB(t *testing.T) *data.DB {
	t.Helper()
	patch.ModelsSymmetricKey(t)
	db, err := data.NewDB(data.NewDBOptions{DSN: database.PostgresDriver(t, "mapping_rule_auth").DSN})
	assert.NilError(t, err)
	return db
}

func setupMappingRuleTestContext(t *testing.T, withAdmin bool) RequestContext {
	t.Helper()
	db := setupMappingRuleTestDB(t)

	tx := txnForTestCase(t, db)

	admin := &models.Identity{Name: "admin@example.com"}
	err := data.CreateIdentity(tx, admin)
	assert.NilError(t, err)

	rCtx := RequestContext{
		DBTxn:         tx,
		Authenticated: Authenticated{User: admin},
	}

	if withAdmin {
		adminGrant := &models.Grant{
			Subject:   models.NewSubjectForUser(admin.ID),
			Privilege: models.InfraAdminRole,
			Resource:  ResourceInfraAPI,
		}
		err = data.CreateGrant(tx, adminGrant)
		assert.NilError(t, err)
	}

	return rCtx
}

func TestListMappingRulesRequiresAdminRole(t *testing.T) {
	rCtx := setupMappingRuleTestContext(t, false) // no admin grant

	err := ListMappingRules(rCtx)
	assert.Assert(t, err != nil, "expected error for non-admin user listing mapping rules")
}

func TestGetMappingRuleRequiresAdminRole(t *testing.T) {
	db := setupMappingRuleTestDB(t)
	tx := txnForTestCase(t, db)

	nonAdmin := &models.Identity{Name: "nonadmin@example.com"}
	err := data.CreateIdentity(tx, nonAdmin)
	assert.NilError(t, err)

	rCtx := RequestContext{
		DBTxn:         tx,
		Authenticated: Authenticated{User: nonAdmin},
	}

	_, err = GetMappingRule(rCtx, uid.New())
	assert.Assert(t, err != nil, "expected error for non-admin user getting mapping rule")
}

func TestCreateMappingRuleRequiresAdminRole(t *testing.T) {
	rCtx := setupMappingRuleTestContext(t, false) // no admin grant

	mapping := &models.MappingRule{
		RuleName:         "test-rule",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  models.DestinationTypeSSH,
		NameTemplate:     "host-$1",
	}

	err := CreateMappingRule(rCtx, mapping)
	assert.Assert(t, err != nil, "expected error for non-admin user creating mapping rule")
}

func TestUpdateMappingRuleRequiresAdminRole(t *testing.T) {
	rCtx := setupMappingRuleTestContext(t, false) // no admin grant

	err := UpdateMappingRule(rCtx, uid.New(), &models.MappingRule{})
	assert.Assert(t, err != nil, "expected error for non-admin user updating mapping rule")
}

func TestDeleteMappingRuleRequiresAdminRole(t *testing.T) {
	rCtx := setupMappingRuleTestContext(t, false) // no admin grant

	err := DeleteMappingRule(rCtx, uid.New())
	assert.Assert(t, err != nil, "expected error for non-admin user deleting mapping rule")
}
