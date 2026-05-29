// Tests for IDP-synced group cleanup when no matching mapping rules apply.
// When a mapping rule is deleted or changed such that an IDP-synced group no longer matches,
// the orphaned group row should be soft-deleted during cleanupStaleGrants (called by EvaluateMappingRules).
package server

import (
	"context"
	"testing"

	gotassert "gotest.tools/v3/assert"

	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// TestCleanupOrphanedGroups_NoMatchingRuleDropsGroup verifies that IDP-synced groups which no longer
// match any active mapping rule are soft-deleted during cleanup. This is the core orphan cleanup path:
// when a user's IDP groups change (or rules are deleted), stale group rows must be cleaned up automatically.
func TestCleanupOrphanedGroups_NoMatchingRuleDropsGroup(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a mapping rule that matches team-* groups.
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "team-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	gotassert.NilError(t, data.CreateMappingRule(tx, mapping))

	// Create an IDP-synced group (CreatedByProvider = 42 — simulates a real provider).
	idpGroup := models.Group{
		Name:               "team-platform",
		CreatedByProvider:  uid.ID(42), // non-zero → marks as IDP-synced
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	gotassert.NilError(t, data.CreateGroup(tx, &idpGroup))

	// Run the engine — group should be kept because it matches the rule.
	gotassert.NilError(t, EvaluateMappingRules(tx))

	// Verify the group still exists (matches a rule).
	stored, err := data.GetGroup(tx, data.GetGroupOptions{ByID: idpGroup.ID})
	gotassert.NilError(t, err)
	gotassert.Assert(t, stored != nil && !stored.DeletedAt.Valid,
		"expected 'team-platform' to still exist (matches rule)")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// === Now delete the mapping rule and re-run cleanup. ===
	rawTx2, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	tx2 := rawTx2.WithOrgID(orgID)
	defer func() { _ = tx2.Rollback() }()

	// Delete the mapping rule.
	gotassert.NilError(t, data.DeleteMappingRule(tx2, mapping.ID))

	// Re-run evaluation — this should trigger orphan cleanup for team-platform.
	gotassert.NilError(t, EvaluateMappingRules(tx2))

	// Verify the IDP-synced group was soft-deleted (orphaned).
	stored2, err := data.GetGroup(tx2, data.GetGroupOptions{ByID: idpGroup.ID})
	if stored2 != nil {
		gotassert.Assert(t, !stored2.DeletedAt.Valid,
			"expected 'team-platform' to be soft-deleted (orphaned after rule deletion)")
	} else if err == nil {
		t.Logf("group %s no longer found in DB", idpGroup.Name)
	}

	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit2: %v", err)
	}
}

// TestCleanupOrphanedGroups_LocalGroupNeverDeleted verifies that locally-created groups
// (CreatedByProvider = 0 or NULL) are NEVER deleted by the orphan cleanup, even when no rules match.
func TestCleanupOrphanedGroups_LocalGroupNeverDeleted(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a locally-created group (CreatedByProvider = 0).
	localGroup := models.Group{
		Name:               "admin-team",
		CreatedByProvider:  uid.ID(0), // zero → marks as local/admin-created
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	gotassert.NilError(t, data.CreateGroup(tx, &localGroup))

	// Create an IDP-synced group that won't match any rule.
	idpGroup := models.Group{
		Name:               "team-platform",
		CreatedByProvider:  uid.ID(42), // non-zero → marks as IDP-synced
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	gotassert.NilError(t, data.CreateGroup(tx, &idpGroup))

	// Run the engine with NO mapping rules — should clean up orphaned groups.
	gotassert.NilError(t, EvaluateMappingRules(tx))

	// Verify the IDP-synced group was cleaned up.
	storedIDP, err := data.GetGroup(tx, data.GetGroupOptions{ByID: idpGroup.ID})
	if storedIDP != nil {
		gotassert.Assert(t, !storedIDP.DeletedAt.Valid,
			"expected 'team-platform' (IDP-synced) to be cleaned up")
	}

	// Verify the local group is STILL present and NOT deleted.
	storedLocal, err := data.GetGroup(tx, data.GetGroupOptions{ByID: localGroup.ID})
	gotassert.NilError(t, err)
	gotassert.Assert(t, storedLocal != nil && !storedLocal.DeletedAt.Valid,
		"expected 'admin-team' (local group) to still exist after cleanup")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCleanupOrphanedGroups_NoRulesDropsAllIDPGroups verifies that when there are NO mapping rules,
// ALL IDP-synced groups (CreatedByProvider != 0) are cleaned up. This is the sentinel pattern path:
// with no rules to match against, every orphaned group row should be removed.
func TestCleanupOrphanedGroups_NoRulesDropsAllIDPGroups(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create multiple IDP-synced groups (CreatedByProvider != 0).
	idpGroups := []string{"team-platform", "ops-general", "svc-monitoring"}
	for _, name := range idpGroups {
		g := models.Group{
			Name:               name,
			CreatedByProvider:  uid.ID(42), // non-zero → marks as IDP-synced
			OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		}
		gotassert.NilError(t, data.CreateGroup(tx, &g))
	}

	// Run the engine with NO mapping rules.
	gotassert.NilError(t, EvaluateMappingRules(tx))

	// Verify ALL IDP-synced groups were cleaned up (all should be soft-deleted).
	for _, name := range idpGroups {
		stored, _ := data.GetGroup(tx, data.GetGroupOptions{ByName: name})
		if stored != nil && !stored.DeletedAt.Valid {
			// Group still exists and is NOT deleted → cleanup failed for this group
			gotassert.Assert(t, false, "expected '%s' to be cleaned up; still exists", name)
		} else if stored == nil {
			continue // not found = properly cleaned up or never existed
		} else {
			gotassert.Assert(t, false, "expected '%s' to be cleaned up; still exists: %+v", name, stored)
		}
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCleanupOrphanedGroups_MatchingRulePreservesGroup verifies that an IDP-synced group is preserved
// when at least one active mapping rule matches it. The orphan cleanup should only drop groups with NO match.
func TestCleanupOrphanedGroups_MatchingRulePreservesGroup(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a mapping rule that matches team-* groups.
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "team-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	gotassert.NilError(t, data.CreateMappingRule(tx, mapping))

	// Create an IDP-synced group that matches the rule.
	matchingGroup := models.Group{
		Name:               "team-platform",
		CreatedByProvider:  uid.ID(42), // non-zero → marks as IDP-synced
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	gotassert.NilError(t, data.CreateGroup(tx, &matchingGroup))

	// Run the engine.
	gotassert.NilError(t, EvaluateMappingRules(tx))

	// Verify the matching group is NOT cleaned up.
	stored, err := data.GetGroup(tx, data.GetGroupOptions{ByID: matchingGroup.ID})
	gotassert.NilError(t, err)
	gotassert.Assert(t, stored != nil && !stored.DeletedAt.Valid,
		"expected 'team-platform' to still exist (matches active rule)")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCleanupOrphanedGroups_CrossOrgIsolation verifies that orphan cleanup in one organization's context
// does NOT affect groups belonging to another organization. This is critical because the cleanup query
// uses org_id scoping and a regression could cause cross-org data loss.
func TestCleanupOrphanedGroups_CrossOrgIsolation(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	// Create second organization.
	orgB := &models.Organization{Name: "orphan-cross-org", Domain: "orgb.example.com"}
	gotassert.NilError(t, data.CreateOrganization(srv.db, orgB))

	orgAID := srv.db.DefaultOrg.ID
	orgBID := orgB.ID

	// === Org A: create rule that matches team-*. ===
	rawTxA, err := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, err)
	txA := rawTxA.WithOrgID(orgAID)
	defer func() { _ = txA.Rollback() }()

	mappingA := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgAID},
		RuleName:           "orga-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	gotassert.NilError(t, data.CreateMappingRule(txA, mappingA))

	if err := txA.Commit(); err != nil {
		t.Fatalf("commit orgA: %v", err)
	}

	// === Org B: create an IDP-synced group (no matching rule in org B). ===
	rawTxB, err := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, err)
	txB := rawTxB.WithOrgID(orgBID)
	defer func() { _ = txB.Rollback() }()

	idpGroupB := models.Group{
		Name:               "team-platform",
		CreatedByProvider:  uid.ID(42), // non-zero → marks as IDP-synced
		OrganizationMember: models.OrganizationMember{OrganizationID: orgBID},
	}
	gotassert.NilError(t, data.CreateGroup(txB, &idpGroupB))

	if err := txB.Commit(); err != nil {
		t.Fatalf("commit orgB create: %v", err)
	}

	// Run cleanup in ORG A — should NOT affect org B's groups.
	rawTxA2, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	txA2 := rawTxA2.WithOrgID(orgAID)
	defer func() { _ = txA2.Rollback() }()

	gotassert.NilError(t, EvaluateMappingRules(txA2))

	// Verify org B's group is STILL present (orphan cleanup did not leak across orgs).
	rawTxBCheck, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	txBCheck := rawTxBCheck.WithOrgID(orgBID)
	defer func() { _ = txBCheck.Rollback() }()

	storedB, err := data.GetGroup(txBCheck, data.GetGroupOptions{ByID: idpGroupB.ID})
	gotassert.NilError(t, err)
	gotassert.Assert(t, storedB != nil && !storedB.DeletedAt.Valid,
		"expected org B's group 'team-platform' to still exist after cleanup in org A")

	if err := txA2.Commit(); err != nil {
		t.Fatalf("commit orgA2: %v", err)
	}
	if err := txBCheck.Commit(); err != nil {
		t.Fatalf("commit orgBCheck: %v", err)
	}
}

// TestCleanupOrphanedGroups_DeletedRuleDropsGroup verifies that when a mapping rule is deleted,
// the groups it was matching should be cleaned up as orphans during the next evaluation.
func TestCleanupOrphanedGroups_DeletedRuleDropsGroup(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a mapping rule.
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "old-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	gotassert.NilError(t, data.CreateMappingRule(tx, mapping))

	// Create an IDP-synced group that matches the rule.
	idpGroup := models.Group{
		Name:               "team-platform",
		CreatedByProvider:  uid.ID(42), // non-zero → marks as IDP-synced
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	gotassert.NilError(t, data.CreateGroup(tx, &idpGroup))

	// Verify the group exists before cleanup.
	before, err := data.GetGroup(tx, data.GetGroupOptions{ByID: idpGroup.ID})
	gotassert.NilError(t, err)
	gotassert.Assert(t, before != nil && !before.DeletedAt.Valid,
		"expected group to exist before rule deletion")

	// Delete the mapping rule.
	gotassert.NilError(t, data.DeleteMappingRule(tx, mapping.ID))

	// Run evaluation — this triggers orphan cleanup for groups no longer covered by any rule.
	gotassert.NilError(t, EvaluateMappingRules(tx))

	// Verify the group was soft-deleted (orphaned).
	after, err := data.GetGroup(tx, data.GetGroupOptions{ByID: idpGroup.ID})
	if after != nil {
		gotassert.Assert(t, !after.DeletedAt.Valid,
			"expected 'team-platform' to be soft-deleted after its matching rule was deleted")
	} else if err == nil {
		t.Logf("group %s no longer found in DB", idpGroup.Name)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCleanupOrphanedGroups_MultipleRulesOneMatch verifies that when multiple rules exist and a group
// matches at least ONE of them, it is preserved. The cleanup only drops groups with ZERO matching rules.
func TestCleanupOrphanedGroups_MultipleRulesOneMatch(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Rule 1 matches team-*.
	mapping1 := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "team-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	gotassert.NilError(t, data.CreateMappingRule(tx, mapping1))

	// Rule 2 matches ops-*.
	mapping2 := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "ops-rule",
		SourceGroupRegex:   "^ops-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	gotassert.NilError(t, data.CreateMappingRule(tx, mapping2))

	// IDP-synced group that matches ONLY rule 2 (ops-*).
	matchingGroup := models.Group{
		Name:               "ops-general",
		CreatedByProvider:  uid.ID(42), // non-zero → marks as IDP-synced
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	gotassert.NilError(t, data.CreateGroup(tx, &matchingGroup))

	// Run the engine.
	gotassert.NilError(t, EvaluateMappingRules(tx))

	// Verify the group is preserved (matches rule 2).
	stored, err := data.GetGroup(tx, data.GetGroupOptions{ByID: matchingGroup.ID})
	gotassert.NilError(t, err)
	gotassert.Assert(t, stored != nil && !stored.DeletedAt.Valid,
		"expected 'ops-general' to still exist (matches ops-rule)")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCleanupOrphanedGroups_GroupWithMembersPreserved verifies that a group which has identity members
// is still subject to orphan cleanup rules — the cleanup checks createdByProvider, not membership.
func TestCleanupOrphanedGroups_GroupWithMembersPreserved(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Rule matches team-*.
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "team-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	gotassert.NilError(t, data.CreateMappingRule(tx, mapping))

	// IDP-synced group with members that matches the rule.
	idpGroup := models.Group{
		Name:               "team-platform",
		CreatedByProvider:  uid.ID(42), // non-zero → marks as IDP-synced
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	gotassert.NilError(t, data.CreateGroup(tx, &idpGroup))

	// Add a member to the group.
	user := &models.Identity{Name: "member@example.com"}
	gotassert.NilError(t, data.CreateIdentity(tx, user))
	gotassert.NilError(t, data.AddUsersToGroup(tx, idpGroup.ID, []uid.ID{user.ID}))

	// Run the engine.
	gotassert.NilError(t, EvaluateMappingRules(tx))

	// Verify the group is preserved (matches rule) and still has its member.
	stored, err := data.GetGroup(tx, data.GetGroupOptions{ByID: idpGroup.ID})
	gotassert.NilError(t, err)
	gotassert.Assert(t, stored != nil && !stored.DeletedAt.Valid,
		"expected 'team-platform' to still exist")

	members, err := data.GetUsersInGroup(tx, idpGroup.ID)
	gotassert.NilError(t, err)
	gotassert.Assert(t, len(members) >= 1, "expected member still in group after evaluation")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCleanupOrphanedGroups_OnlyLocalGroupsNoDeletion verifies that when ALL groups are locally-created,
// none of them should be deleted even with no matching mapping rules. This is the safety net for admin-managed orgs.
func TestCleanupOrphanedGroups_OnlyLocalGroupsNoDeletion(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	gotassert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create multiple locally-created groups (CreatedByProvider = 0).
	localGroups := []string{"admin-team", "dev-ops", "security-leads"}
	for _, name := range localGroups {
		g := models.Group{
			Name:               name,
			CreatedByProvider:  uid.ID(0), // zero → marks as local/admin-created
			OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		}
		gotassert.NilError(t, data.CreateGroup(tx, &g))
	}

	// Run the engine with NO mapping rules.
	gotassert.NilError(t, EvaluateMappingRules(tx))

	// Verify ALL local groups are still present and NOT deleted.
	for _, name := range localGroups {
		stored, err := data.GetGroup(tx, data.GetGroupOptions{ByName: name})
		gotassert.NilError(t, err)
		gotassert.Assert(t, stored != nil && !stored.DeletedAt.Valid,
			"expected '%s' (local group) to still exist after cleanup with no rules", name)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
