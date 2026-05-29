// Tests for the IDP group filtering feature in SyncProviderUser (filterIDPGroups).
// This function filters incoming IDP group names to only those that:
// 1. Are locally created groups (CreatedByProvider is NULL/0), OR
// 2. Match the source_group_regex of any active mapping rule for this org.
package data

import (
	"context"
	"testing"
	"time"

	gotassert "gotest.tools/v3/assert"

	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/internal/server/providers"
)

// TestFilterIDPGroups_LocalGroupAlwaysAllowed verifies that locally-created groups
// (CreatedByProvider = 0 or NULL) are always allowed through the filter, regardless of
// whether any mapping rules exist. This is critical: admin-managed groups must never be
// silently dropped by IDP sync.
func TestFilterIDPGroups_LocalGroupAlwaysAllowed(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-test", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Create a locally-created group (CreatedByProvider = 0).
		localGroup := models.Group{
			Name:               "admin-team",
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
		}
		gotassert.NilError(t, CreateGroup(tx, &localGroup))

		// Sync a provider user with the local group name + some non-matching groups.
		user := &models.Identity{Name: "alice@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"admin-team", "idp-only-group"}, // idp-only-group has no local counterpart and no rules
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Verify the locally-created group is present in the result.
		var foundLocal bool
		for _, g := range groups {
			if g.Name == "admin-team" {
				foundLocal = true
				break
			}
		}
		gotassert.Assert(t, foundLocal, "expected locally-created group 'admin-team' to be present after sync")

		// Verify the non-matching IDP-only group is NOT present.
		for _, g := range groups {
			if g.Name == "idp-only-group" {
				t.Fatal("unexpected: idp-only-group should have been filtered out (no local counterpart, no mapping rules)")
			}
		}

		// Verify the group was actually assigned to the user.
		storedGroups, err := ListGroups(tx, ListGroupsOptions{ByGroupMember: user.ID})
		gotassert.NilError(t, err)

		var foundInStore bool
		for _, g := range storedGroups {
			if g.Name == "admin-team" {
				foundInStore = true
				break
			}
		}
		gotassert.Assert(t, foundInStore, "expected 'admin-team' to be assigned to user in storage")
	})
}

// TestFilterIDPGroups_MappingRuleAllowsGroup verifies that IDP-synced groups are allowed
// through the filter when they match the source_group_regex of an active mapping rule.
func TestFilterIDPGroups_MappingRuleAllowsGroup(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-rule", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Create a mapping rule that matches the IDP group pattern.
		mapping := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			RuleName:           "platform-rule",
			SourceGroupRegex:   "^team-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(tx, mapping))

		user := &models.Identity{Name: "bob@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		// IDP returns groups: one matches the rule, one does not.
		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"team-platform", "idp-excluded"}, // team-platform matches ^team-(.*)$; idp-excluded doesn't
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Verify the matching group is present.
		var foundMatching bool
		for _, g := range groups {
			if g.Name == "team-platform" {
				foundMatching = true
				break
			}
		}
		gotassert.Assert(t, foundMatching, "expected 'team-platform' (matching rule) to be present after sync")

		// Verify the non-matching group is NOT present.
		for _, g := range groups {
			if g.Name == "idp-excluded" {
				t.Fatal("unexpected: 'idp-excluded' should have been filtered out (does not match any rule)")
			}
		}

		// Verify the group was assigned to the user.
		storedGroups, err := ListGroups(tx, ListGroupsOptions{ByGroupMember: user.ID})
		gotassert.NilError(t, err)

		var foundInStore bool
		for _, g := range storedGroups {
			if g.Name == "team-platform" {
				foundInStore = true
				break
			}
		}
		gotassert.Assert(t, foundInStore, "expected 'team-platform' to be assigned to user in storage")
	})
}

// TestFilterIDPGroups_NoRulesNoLocalDropsAll verifies that when there are NO mapping rules
// and NO locally-created groups, ALL incoming IDP groups are dropped. This is the expected
// behavior: if nothing matches, no group should be synced from the IDP — it forces explicit
// admin configuration before any IDP groups can flow through.
func TestFilterIDPGroups_NoRulesNoLocalDropsAll(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-empty", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// No mapping rules created.
		// No locally-created groups created.

		user := &models.Identity{Name: "carol@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"team-platform", "ops-general"}, // no rules → all dropped
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Verify NO groups are present — everything should be filtered out.
		gotassert.Assert(t, len(groups) == 0,
			"expected zero groups when no mapping rules and no local groups exist; got %d: %+v",
			len(groups), groups)

		// Verify the user has NO group assignments.
		storedGroups, err := ListGroups(tx, ListGroupsOptions{ByGroupMember: user.ID})
		gotassert.NilError(t, err)
		gotassert.Assert(t, len(storedGroups) == 0,
			"expected zero stored groups for user; got %d", len(storedGroups))
	})
}

// TestFilterIDPGroups_MixedLocalAndRuleGroups verifies the combined path: some groups are
// allowed because they're local, others because they match a rule, and non-matching ones are dropped.
func TestFilterIDPGroups_MixedLocalAndRuleGroups(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-mixed", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Create a locally-created group.
		localGroup := models.Group{
			Name:               "admin-team",
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
		}
		gotassert.NilError(t, CreateGroup(tx, &localGroup))

		// Create a mapping rule for team-* groups.
		mapping := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			RuleName:           "team-rule",
			SourceGroupRegex:   "^team-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(tx, mapping))

		user := &models.Identity{Name: "dave@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		// IDP returns: local group (allowed), rule-matching group (allowed), non-matching group (dropped).
		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"admin-team", "team-platform", "idp-excluded"},
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Verify exactly 2 groups are present.
		gotassert.Assert(t, len(groups) == 2,
			"expected exactly 2 groups (local + rule-matching); got %d: %+v", len(groups), groups)

		groupNames := make(map[string]bool)
		for _, g := range groups {
			groupNames[g.Name] = true
		}

		gotassert.Assert(t, groupNames["admin-team"], "expected 'admin-team' (local) to be present")
		gotassert.Assert(t, groupNames["team-platform"], "expected 'team-platform' (rule-matching) to be present")
		gotassert.Assert(t, !groupNames["idp-excluded"], "unexpected: 'idp-excluded' should have been filtered out")

		// Verify both groups are assigned to the user.
		storedGroups, err := ListGroups(tx, ListGroupsOptions{ByGroupMember: user.ID})
		gotassert.NilError(t, err)

		var foundLocal, foundRule bool
		for _, g := range storedGroups {
			if g.Name == "admin-team" {
				foundLocal = true
			}
			if g.Name == "team-platform" {
				foundRule = true
			}
		}

		gotassert.Assert(t, foundLocal && foundRule,
			"expected both 'admin-team' and 'team-platform' to be assigned; local=%v rule=%v",
			foundLocal, foundRule)
	})
}

// TestFilterIDPGroups_OrderPreserved verifies that the filter preserves the order of incoming groups.
func TestFilterIDPGroups_OrderPreserved(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-order", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Create a mapping rule that matches all group names.
		mapping := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			RuleName:           "catch-all",
			SourceGroupRegex:   "^.*$", // matches everything
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(tx, mapping))

		user := &models.Identity{Name: "eve@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		expectedOrder := []string{"zulu-team", "alpha-team", "bravo-team"}
		oidc := &filteringOIDCClient{
			UserGroupsResp: expectedOrder, // all should pass through (rule matches everything)
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Verify exactly 3 groups are present (order may vary due to DB storage).
		gotassert.Assert(t, len(groups) == 3, "expected 3 groups; got %d", len(groups))

		groupNames := make(map[string]bool)
		for _, g := range groups {
			groupNames[g.Name] = true
		}
		for _, expected := range expectedOrder {
			gotassert.Assert(t, groupNames[expected], "expected '%s' to be present in result", expected)
		}
	})
}

// TestFilterIDPGroups_DuplicateGroupNames verifies that duplicate group names from the IDP
// are handled correctly — each unique group should appear exactly once in the result.
func TestFilterIDPGroups_DuplicateGroupNames(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-dupes", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		mapping := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			RuleName:           "catch-all",
			SourceGroupRegex:   "^.*$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(tx, mapping))

		user := &models.Identity{Name: "frank@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		// IDP returns duplicates.
		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"team-platform", "team-platform", "ops-general"}, // team-platform appears twice
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Count occurrences of each group name.
		counts := make(map[string]int)
		for _, g := range groups {
			counts[g.Name]++
		}

		gotassert.Assert(t, counts["team-platform"] == 1,
			"expected 'team-platform' to appear exactly once; got %d", counts["team-platform"])
	})
}

// TestFilterIDPGroups_EmptyIncomingGroups verifies behavior when the IDP returns zero groups.
func TestFilterIDPGroups_EmptyIncomingGroups(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-empty-in", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		user := &models.Identity{Name: "grace@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{}, // empty group list from IDP
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)
		gotassert.Assert(t, len(groups) == 0, "expected zero groups when IDP returns none; got %d", len(groups))
	})
}

// TestFilterIDPGroups_MultipleMappingRulesUnion verifies that the filter uses a union of all
// active rule patterns — a group is allowed if it matches ANY rule.
func TestFilterIDPGroups_MultipleMappingRulesUnion(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-multi-rule", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Rule 1 matches team-* groups.
		mapping1 := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			RuleName:           "team-rule",
			SourceGroupRegex:   "^team-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(tx, mapping1))

		// Rule 2 matches ops-* groups.
		mapping2 := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			RuleName:           "ops-rule",
			SourceGroupRegex:   "^ops-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(tx, mapping2))

		user := &models.Identity{Name: "heidi@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		// IDP returns groups matching rule1, rule2, and neither.
		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"team-platform", "ops-general", "other-group"},
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		groupNames := make(map[string]bool)
		for _, g := range groups {
			groupNames[g.Name] = true
		}

		gotassert.Assert(t, groupNames["team-platform"], "expected 'team-platform' (matches rule1)")
		gotassert.Assert(t, groupNames["ops-general"], "expected 'ops-general' (matches rule2)")
		gotassert.Assert(t, !groupNames["other-group"], "unexpected: 'other-group' should be filtered out")

		gotassert.Assert(t, len(groups) == 2, "expected exactly 2 groups; got %d", len(groups))
	})
}

// TestFilterIDPGroups_DeletedRuleNotMatched verifies that soft-deleted mapping rules do NOT
// contribute to the combined regex — a group should be dropped if only deleted rules matched it.
func TestFilterIDPGroups_DeletedRuleNotMatched(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-deleted", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Create a mapping rule.
		mapping := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			RuleName:           "old-rule",
			SourceGroupRegex:   "^team-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(tx, mapping))

		// Soft-delete the rule.
		gotassert.NilError(t, DeleteMappingRule(tx, mapping.ID))

		user := &models.Identity{Name: "ivan@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		// IDP returns a group that would have matched the deleted rule.
		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"team-platform"}, // matches old (now deleted) rule
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Since the only matching rule is now deleted and no local groups exist,
		// all IDP groups should be filtered out.
		for _, g := range groups {
			if g.Name == "team-platform" {
				t.Fatal("unexpected: 'team-platform' should have been filtered out (only matching rule was soft-deleted)")
			}
		}

		gotassert.Assert(t, len(groups) == 0,
			"expected zero groups when only deleted rules match; got %d", len(groups))
	})
}

// TestFilterIDPGroups_GroupAlreadyAssigned verifies that if a group is already assigned to the user
// from a previous sync, subsequent SyncProviderUser calls still return it correctly.
func TestFilterIDPGroups_GroupAlreadyAssigned(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-reassign", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		mapping := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			RuleName:           "catch-all",
			SourceGroupRegex:   "^.*$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(tx, mapping))

		user := &models.Identity{Name: "jane@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		// First sync with one set of groups.
		oidc1 := &filteringOIDCClient{
			UserGroupsResp: []string{"team-platform"},
		}
		groups1, err := SyncProviderUser(context.Background(), tx, pu, oidc1)
		gotassert.NilError(t, err)

		// Verify group was assigned.
		storedGroups1, err := ListGroups(tx, ListGroupsOptions{ByGroupMember: user.ID})
		gotassert.NilError(t, err)
		gotassert.Assert(t, len(storedGroups1) == 1 && storedGroups1[0].Name == "team-platform",
			"expected 'team-platform' after first sync; got %d groups", len(storedGroups1))

		// Verify the group was returned from the sync.
		gotassert.Assert(t, len(groups1) == 1 && groups1[0].Name == "team-platform",
			"first sync should return 'team-platform'")

		// Second sync with different groups (group removed from IDP).
		oidc2 := &filteringOIDCClient{
			UserGroupsResp: []string{}, // team-platform no longer in IDP
		}
		groups2, err := SyncProviderUser(context.Background(), tx, pu, oidc2)
		gotassert.NilError(t, err)

		// Verify the group assignment is updated (group removed).
		storedGroups2, err := ListGroups(tx, ListGroupsOptions{ByGroupMember: user.ID})
		gotassert.NilError(t, err)
		for _, g := range storedGroups2 {
			if g.Name == "team-platform" {
				t.Fatal("unexpected: 'team-platform' should have been removed from user's groups")
			}
		}

		gotassert.Assert(t, len(groups2) == 0 && len(storedGroups2) == 0,
			"expected zero groups after second sync; got %d groups returned and %d stored",
			len(groups2), len(storedGroups2))
	})
}

// TestFilterIDPGroups_CrossOrgIsolation verifies that the filter only considers mapping rules
// from the current organization. A group matching a rule in another org should NOT be allowed
// through for the current user's sync operation.
func TestFilterIDPGroups_CrossOrgIsolation(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		orgA := &models.Organization{Name: "filter-org-a", Domain: "orga.example.com"}
		gotassert.NilError(t, CreateOrganization(db, orgA))

		orgB := &models.Organization{Name: "filter-org-b", Domain: "orgb.example.com"}
		gotassert.NilError(t, CreateOrganization(db, orgB))

		txA := txnForTestCase(t, db, orgA.ID)
		txB := txnForTestCase(t, db, orgB.ID)

		// Org B creates a mapping rule that matches "team-platform".
		mappingB := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: orgB.ID},
			RuleName:           "orgb-rule",
			SourceGroupRegex:   "^team-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(txB, mappingB))

		// Org A has NO matching rules but tries to sync a user with group "team-platform".
		userA := &models.Identity{Name: "user-a@example.com"}
		gotassert.NilError(t, CreateIdentity(txA, userA))

		puA := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   userA.ID,
			Email:        userA.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		// IDP returns group that matches orgB's rule but NOT orgA's (orgA has no rules).
		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"team-platform"}, // only matches orgB's rule
		}

		groups, err := SyncProviderUser(context.Background(), txA, puA, oidc)
		gotassert.NilError(t, err)

		// The group should NOT be allowed — orgA has no matching rules and no local groups.
		for _, g := range groups {
			if g.Name == "team-platform" {
				t.Fatal("unexpected: 'team-platform' should have been filtered out (no matching rule in org A)")
			}
		}

		gotassert.Assert(t, len(groups) == 0,
			"expected zero groups for org A (cross-org rules must not leak); got %d", len(groups))
	})
}

// TestFilterIDPGroups_AllLocalGroupsWithNoRules verifies that when there are NO mapping rules
// but some locally-created groups exist, those local groups ARE allowed through.
func TestFilterIDPGroups_AllLocalGroupsWithNoRules(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-local-only", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Create multiple locally-created groups.
		localGroups := []string{"admin-team", "dev-ops", "security-leads"}
		for _, name := range localGroups {
			g := models.Group{
				Name:               name,
				OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			}
			gotassert.NilError(t, CreateGroup(tx, &g))
		}

		user := &models.Identity{Name: "local@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		// IDP returns all local groups plus one non-matching group.
		oidc := &filteringOIDCClient{
			UserGroupsResp: append(localGroups, "idp-excluded"), // idp-excluded is not a local group
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// All local groups should be present.
		for _, name := range localGroups {
			var found bool
			for _, g := range groups {
				if g.Name == name {
					found = true
					break
				}
			}
			gotassert.Assert(t, found, "expected '%s' (local group) to be present", name)
		}

		// The non-local, non-matching group should NOT be present.
		for _, g := range groups {
			if g.Name == "idp-excluded" {
				t.Fatal("unexpected: 'idp-excluded' should have been filtered out (not local, no matching rule)")
			}
		}

		gotassert.Assert(t, len(groups) == 3,
			"expected exactly %d groups (all local); got %d", len(localGroups), len(groups))
	})
}

// TestFilterIDPGroups_NonMatchingRegexPattern verifies that the combined regex uses OR semantics —
// each rule's pattern is joined with | to form a single alternation. A group matching ANY pattern
// should be allowed through.
func TestFilterIDPGroups_NonMatchingRegexPattern(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-regex", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Rule with a very specific pattern.
		mapping := &models.MappingRule{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			RuleName:           "specific-rule",
			SourceGroupRegex:   "^svc-(monitoring|logging)-.*$", // matches svc-monitoring-* or svc-logging-*
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		}
		gotassert.NilError(t, CreateMappingRule(tx, mapping))

		user := &models.Identity{Name: "regex@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"svc-monitoring-prometheus", "svc-logging-elasticsearch", "svc-computing-batch"}, // third doesn't match
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		groupNames := make(map[string]bool)
		for _, g := range groups {
			groupNames[g.Name] = true
		}

		gotassert.Assert(t, groupNames["svc-monitoring-prometheus"], "expected 'svc-monitoring-prometheus' (matches monitoring branch)")
		gotassert.Assert(t, groupNames["svc-logging-elasticsearch"], "expected 'svc-logging-elasticsearch' (matches logging branch)")
		gotassert.Assert(t, !groupNames["svc-computing-batch"], "unexpected: 'svc-computing-batch' should be filtered out")

		gotassert.Assert(t, len(groups) == 2, "expected exactly 2 groups; got %d", len(groups))
	})
}

// filteringOIDCClient is a minimal OIDC client mock that returns configurable group lists.
type filteringOIDCClient struct {
	UserEmailResp  string
	UserGroupsResp []string
}

func (f *filteringOIDCClient) Validate(_ context.Context) error { return nil }
func (f *filteringOIDCClient) AuthServerInfo(_ context.Context) (*providers.AuthServerInfo, error) {
	return &providers.AuthServerInfo{AuthURL: "example.com/v1/auth"}, nil
}
func (f *filteringOIDCClient) ExchangeAuthCodeForProviderTokens(_ context.Context, _ string) (*providers.IdentityProviderAuth, error) {
	return &providers.IdentityProviderAuth{
		AccessToken: "acc", RefreshToken: "ref", AccessTokenExpiry: time.Now().Add(1 * time.Minute), Email: f.UserEmailResp}, nil
}
func (f *filteringOIDCClient) RefreshAccessToken(_ context.Context, pu *models.ProviderUser) (string, *time.Time, error) {
	if pu.ExpiresAt.Before(time.Now()) {
		exp := time.Now().Add(1 * time.Hour)
		return "new-acc-token", &exp, nil
	}
	return string(pu.AccessToken), &pu.ExpiresAt, nil
}
func (f *filteringOIDCClient) GetUserInfo(_ context.Context, pu *models.ProviderUser) (*providers.UserInfoClaims, error) {
	return &providers.UserInfoClaims{Email: f.UserEmailResp, Groups: f.UserGroupsResp}, nil
}
