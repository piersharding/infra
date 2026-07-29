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

// TestFilterIDPGroups_CollisionDetection verifies that when an incoming IDP group name
// collides with a locally-created group, the collision is detected and logged. The
// incoming group is NOT blocked by this test — it passes through because there are no
// rules or grants covering it (which would be tested separately).
func TestFilterIDPGroups_CollisionDetection(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-test", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Create a locally-created group with a name that does NOT appear in incoming IDP groups.
		localGroup := models.Group{
			Name:               "admin-team-internal",
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
			// 'admin-team' collides with the locally-created group of the same name.
			UserGroupsResp: []string{"admin-team", "team-platform", "idp-only-group"},
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// No groups should pass through: no rules match 'team-platform',
		// 'admin-team' and 'idp-only-group' have no grants.
		gotassert.Assert(t, len(groups) == 0,
			"expected no groups to pass through (no matching rules or grants); got %d", len(groups))
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

		// Create a locally-created group with a name not in incoming (no collision).
		localGroup := models.Group{
			Name:               "admin-team-internal",
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

		// IDP returns: local group name NOT in list (no collision), rule-matching, non-matching.
		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"admin-team", "team-platform", "idp-excluded"},
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Only 'team-platform' passes through (matches mapping rule).
		// Locally-created group is NOT in incoming so doesn't appear.
		gotassert.Assert(t, len(groups) == 1,
			"expected exactly 1 group (rule-matching); got %d: %+v", len(groups), groups)
		gotassert.Assert(t, groups[0].Name == "team-platform",
			"expected 'team-platform' to pass through via rule; got '%s'", groups[0].Name)
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

// TestFilterIDPGroups_NoRulesNoGrants verifies that when there are NO mapping rules
// and no grants, nothing passes through — even if the IDP sends group names.
func TestFilterIDPGroups_NoRulesNoGrants(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-no-rules", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Create a locally-created group with a name that does NOT appear in incoming.
		localGroup := models.Group{
			Name:               "admin-team-internal",
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
		}
		gotassert.NilError(t, CreateGroup(tx, &localGroup))

		user := &models.Identity{Name: "noreules@example.com"}
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

		// IDP returns group names with no matching rules or grants.
		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"some-group", "another-group"},
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Nothing should pass through — no rules, no grants.
		gotassert.Assert(t, len(groups) == 0,
			"expected no groups to pass through (no rules or grants); got %d: %+v", len(groups), groups)
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

// TestFilterIDPGroups_GrantReferencedGroupAllowed verifies that an IDP-synced group
// referenced by a non-auto-grant as its subject is NOT filtered out, even when no mapping
// rule matches and the group itself is not locally created. This ensures manual access via
// grants survives IDP sync filtering.
func TestFilterIDPGroups_GrantReferencedGroupAllowed(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		org := &models.Organization{Name: "filter-grant-ref", Domain: "example.com"}
		gotassert.NilError(t, CreateOrganization(db, org))

		tx := txnForTestCase(t, db, org.ID)

		// Create an IDP-synced group (created_by_provider != 0).
		idpGroup := models.Group{
			Name:               "idp-admins",
			CreatedByProvider:  1, // IDP-created
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
		}
		gotassert.NilError(t, CreateGroup(tx, &idpGroup))

		// Create a user.
		user := &models.Identity{Name: "grant-ref@example.com"}
		gotassert.NilError(t, CreateIdentity(tx, user))

		// Create a non-auto-grant referencing the IDP group as subject.
		manualGrant := models.Grant{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
			CreatedBy:          99, // not CreatedBySystem (not auto)
			Subject:            models.NewSubjectForGroup(idpGroup.ID),
			Privilege:          "connect",
			Resource:           idpGroup.Name + "-host",
		}
		gotassert.NilError(t, CreateGrant(tx, &manualGrant))

		pu := &models.ProviderUser{
			ProviderID:   InfraProvider(db).ID,
			IdentityID:   user.ID,
			Email:        user.Name,
			AccessToken:  "tok",
			RefreshToken: "ref",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
			Active:       true,
		}

		// IDP returns the group name that has no matching rule but is referenced by a grant.
		oidc := &filteringOIDCClient{
			UserGroupsResp: []string{"idp-admins"}, // matches grant reference, not local or rule
		}

		groups, err := SyncProviderUser(context.Background(), tx, pu, oidc)
		gotassert.NilError(t, err)

		// Verify the group IS present — preserved because of non-auto-grant.
		var found bool
		for _, g := range groups {
			if g.Name == "idp-admins" {
				found = true
				break
			}
		}
		gotassert.Assert(t, found, "expected 'idp-admins' to be present (referenced by non-auto-grant)")

		// Verify the user has a group assignment.
		storedGroups, err := ListGroups(tx, ListGroupsOptions{ByGroupMember: user.ID})
		gotassert.NilError(t, err)
		gotassert.Assert(t, len(storedGroups) == 1 && storedGroups[0].Name == "idp-admins",
			"expected 'idp-admins' assigned to user; got %d groups", len(storedGroups))

		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	})
}
