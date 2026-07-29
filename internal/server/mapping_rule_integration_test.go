// Integration tests for the group mapping rules engine.
// These tests spin up a full Server with real PostgreSQL and verify
// that creating/deleting mapping rules correctly creates/cleans up grants.
package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// setupMappingRuleIntegrationTest creates a server with an admin user,
// sets up the DB, and returns the org ID. The caller is responsible for
// rolling back any transaction created during the test.
func setupMappingRuleIntegrationTest(t *testing.T) (*Server, uid.ID) {
	t.Helper()
	srv := setupServer(t, withAdminUser)
	return srv, srv.db.DefaultOrg.ID
}

// TestIntegrationCreateMappingRuleAndEvaluate verifies that:
// 1. Creating a mapping rule persists it in the database
// 2. Running EvaluateMappingRules creates grants matching the rule's regex
// 3. Only matching groups get grants (non-matching are ignored)
func TestIntegrationCreateMappingRuleAndEvaluate(t *testing.T) {
	srv, orgID := setupMappingRuleIntegrationTest(t)

	rawTx, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rawTx.Rollback() })
	tx := rawTx.WithOrgID(orgID)

	// 1. Create a mapping rule that matches groups starting with "team-".
	mappingRule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "team-access",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "$1-infra",
	}
	assert.NilError(t, data.CreateMappingRule(tx, mappingRule))

	// 2. Create groups — some matching the rule, some not.
	groups := []models.Group{
		{Name: "team-platform"}, // matches ^team-(.*)$ → $1 = "platform"
		{Name: "team-devops"},   // matches → $1 = "devops"
		{Name: "ops-general"},   // does NOT match
	}

	for _, g := range groups {
		g.OrganizationMember = models.OrganizationMember{OrganizationID: orgID}
		assert.NilError(t, data.CreateGroup(tx, &g))
	}

	// 3. Run the engine — should create grants for matching groups only.
	assert.NilError(t, EvaluateMappingRules(tx))

	// 4. Verify grants were created in the real database.
	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var sshGroupNames []string
	for _, grant := range allGrants {
		if grant.Subject.Kind == models.SubjectKindGroup && grant.Privilege == "connect" {
			grp, grpErr := data.GetGroup(tx, data.GetGroupOptions{ByID: grant.Subject.ID})
			if grpErr != nil || grp == nil {
				continue
			}
			sshGroupNames = append(sshGroupNames, grp.Name)

			// Verify the grant is marked as auto-granted by the engine.
			assert.Assert(t, grant.AutoGrant, "grant for group %s should have AutoGrant=true", grp.Name)

			// Verify CreatedBy is system (the mapping engine's signature, implied by AutoGrant).
			assert.Assert(t, grant.CreatedBy == models.CreatedBySystem, "grant for group %s should have CreatedBy=system", grp.Name)
		}
	}

	// Only team-* groups should have grants; ops-general should not.
	assert.Assert(t, len(sshGroupNames) == 2, "expected exactly 2 SSH group grants, got %d: %+v", len(sshGroupNames), sshGroupNames)
	assert.Assert(t, containsStr(sshGroupNames, "team-platform"), "expected grant for team-platform")
	assert.Assert(t, containsStr(sshGroupNames, "team-devops"), "expected grant for team-devops")

	// Verify the mapping rule itself is queryable.
	rule, err := data.GetMappingRule(tx, data.GetMappingRuleOptions{ByID: mappingRule.ID})
	assert.NilError(t, err)
	assert.Equal(t, "team-access", rule.RuleName)
}

// TestIntegrationDeleteMappingRuleAndCleanup verifies that deleting a mapping rule
// causes the engine to clean up its stale grants on next evaluation.
func TestIntegrationDeleteMappingRuleAndCleanup(t *testing.T) {
	srv, orgID := setupMappingRuleIntegrationTest(t)

	rawTx, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rawTx.Rollback() })
	tx := rawTx.WithOrgID(orgID)

	// 1. Create a mapping rule and matching group.
	mappingRule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "ssh-access",
		SourceGroupRegex:   "^ops-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "$1-host",
	}
	assert.NilError(t, data.CreateMappingRule(tx, mappingRule))

	group := models.Group{
		Name:               "ops-general",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// 2. Run the engine — creates a grant for ops-general.
	assert.NilError(t, EvaluateMappingRules(tx))

	grantsBefore, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)
	var autoGrantCountBefore int
	for _, g := range grantsBefore {
		if g.AutoGrant {
			autoGrantCountBefore++
		}
	}
	assert.Assert(t, autoGrantCountBefore >= 1, "expected at least 1 auto-grant before deletion")

	// 3. Delete the mapping rule (soft-delete).
	assert.NilError(t, data.DeleteMappingRule(tx, mappingRule.ID))

	// 4. Run the engine again — should clean up stale grants for deleted rules.
	assert.NilError(t, EvaluateMappingRules(tx))

	// 5. Verify auto-grants were cleaned up.
	grantsAfter, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)
	var autoGrantCountAfter int
	for _, g := range grantsAfter {
		if g.AutoGrant {
			autoGrantCountAfter++
		}
	}

	assert.Assert(t, autoGrantCountAfter < autoGrantCountBefore,
		"expected fewer auto-grants after cleanup: %d → %d", autoGrantCountBefore, autoGrantCountAfter)
}

// TestIntegrationManualGrantsSurviveCleanup verifies that manually created grants
// (AutoGrant=false) survive the engine's cleanup pass.
func TestIntegrationManualGrantsSurviveCleanup(t *testing.T) {
	srv, orgID := setupMappingRuleIntegrationTest(t)

	rawTx, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rawTx.Rollback() })
	tx := rawTx.WithOrgID(orgID)

	// Create a mapping rule and matching group.
	mappingRule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "manual-survival",
		SourceGroupRegex:   "^test-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "$1-infra",
	}
	assert.NilError(t, data.CreateMappingRule(tx, mappingRule))

	group := models.Group{
		Name:               "test-survival",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// Create a manual grant for the same group (simulates user-created access).
	manualGrant := &models.Grant{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		CreatedBy:          99, // non-system creator
		Subject:            models.NewSubjectForGroup(group.ID),
		Privilege:          "connect",
		Resource:           "manual-host-infra",
	}
	assert.NilError(t, data.CreateGrant(tx, manualGrant))

	// Run the engine — should NOT delete the manual grant.
	assert.NilError(t, EvaluateMappingRules(tx))

	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var manualRemaining int
	for _, g := range allGrants {
		if g.CreatedBy == 99 && g.Privilege == "connect" && g.Resource == "manual-host-infra" {
			manualRemaining++
		}
	}

	assert.Equal(t, 1, manualRemaining, "expected manual grant to survive engine cleanup")
}

// TestIntegrationMultiOrgIsolation verifies that mapping rules only affect their own org.
func TestIntegrationMultiOrgIsolation(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	// Use unique names and domains to avoid collisions with other test runs.
	randomSuffix := fmt.Sprintf("%d", time.Now().UnixNano())
	orgA := &models.Organization{
		Name:   "integration-org-a-" + randomSuffix,
		Domain: "integration-org-a-" + randomSuffix + ".test",
	}
	assert.NilError(t, data.CreateOrganization(srv.db, orgA))

	rawTxA, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rawTxA.Rollback() })
	txA := rawTxA.WithOrgID(orgA.ID)

	// Org A: rule matching "team-*" → creates grants for team-platform.
	ruleA := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgA.ID},
		RuleName:           "org-a-team",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "$1-infra",
	}
	assert.NilError(t, data.CreateMappingRule(txA, ruleA))

	groupPlatform := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgA.ID},
	}
	assert.NilError(t, data.CreateGroup(txA, &groupPlatform))

	assert.NilError(t, EvaluateMappingRules(txA))

	grantsA, err := data.ListGrants(txA, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var orgAGrants int64
	for _, g := range grantsA {
		if g.AutoGrant {
			orgAGrants++
		}
	}
	assert.Assert(t, orgAGrants >= 1, "org-a should have at least 1 auto-grant")

	// Org B: no rules → should NOT get any grants.
	rawTxB, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rawTxB.Rollback() })

	// Use a unique name and domain to avoid collision with other tests.
	uniqueSuffix := fmt.Sprintf("b-%d", time.Now().UnixNano())
	orgB := &models.Organization{
		Name:   "integration-org-" + uniqueSuffix,
		Domain: "integration-org-" + uniqueSuffix + ".test",
	}
	assert.NilError(t, data.CreateOrganization(srv.db, orgB))

	txB := rawTxB.WithOrgID(orgB.ID)

	// Run the engine for org B — no rules to match.
	assert.NilError(t, EvaluateMappingRules(txB))

	grantsB, err := data.ListGrants(txB, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var orgBGrants int64
	for _, g := range grantsB {
		if g.AutoGrant {
			orgBGrants++
		}
	}
	assert.Equal(t, int64(0), orgBGrants, "org-b should have 0 auto-grants (no rules)")
}

// TestIntegrationInvalidRegexGracefulDegradation verifies that the engine skips
// invalid regexes without failing the entire evaluation.
func TestIntegrationInvalidRegexGracefulDegradation(t *testing.T) {
	srv, orgID := setupMappingRuleIntegrationTest(t)

	rawTx, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rawTx.Rollback() })
	tx := rawTx.WithOrgID(orgID)

	// Create a valid rule.
	validRule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "valid-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "$1-infra",
	}
	assert.NilError(t, data.CreateMappingRule(tx, validRule))

	// Create an invalid rule (bad regex) via raw SQL since validation rejects it.
	_, sqlErr := tx.Exec(
		`INSERT INTO mapping_rules (id, created_at, updated_at, deleted_at, organization_id,
		created_by, rule_name, source_group_regex, destination_type, name_template)
		VALUES ($1::int8, NOW(), NOW(), NULL, $2, 0, 'invalid-rule', '[invalid(', 'ssh', '$1-infra')`, int64(99), orgID,
	)
	assert.NilError(t, sqlErr)

	// Create a matching group.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// Run the engine — should NOT panic/fail due to invalid rule.
	err = EvaluateMappingRules(tx)
	assert.NilError(t, err, "engine should succeed even with one invalid rule")

	// Verify the valid rule still produced a grant.
	grants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var autoGrantCount int
	for _, g := range grants {
		if g.AutoGrant {
			autoGrantCount++
		}
	}
	assert.Assert(t, autoGrantCount >= 1, "expected at least 1 grant from valid rule")
}

// containsStr checks if a slice of strings contains a given value.
func containsStr(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
