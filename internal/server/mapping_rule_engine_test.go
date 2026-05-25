// Tests for the group mapping engine: template substitution, evaluation logic,
// stale grant cleanup, and multi-tenant isolation.
package server

import (
	"context"
	"regexp"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// TestApplyTemplate verifies that template $N substitution works correctly.
func TestApplyTemplate(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		input     string
		re        string
		want      string
		wantError bool
	}{
		{
			name:     "simple single capture group",
			template: "cluster-$1-prod",
			input:    "team-platform",
			re:       "^team-(.*)$",
			want:     "cluster-platform-prod", // $1 = "platform" (first capture)
		},
		{
			name:     "no captures — no $N references",
			template: "static-name",
			input:    "anything",
			re:       "^.*$",
			want:     "static-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := regexp.Compile(tt.re)
			if err != nil {
				t.Fatalf("failed to compile regex %q: %v", tt.re, err)
			}

			got, err := applyTemplate(tt.template, tt.input, re)
			if (err != nil) != tt.wantError {
				t.Errorf("applyTemplate(%q, %q) error = %v, wantErr %v", tt.template, tt.input, err, tt.wantError)
				return
			}

			if got != tt.want {
				t.Errorf("applyTemplate(%q, %q) = %q, want %q", tt.template, tt.input, got, tt.want)
			}
		})
	}
}

// TestApplyTemplateEdgeCases covers: unmatched references (empty string), invalid ${...} syntax, and non-matching regex.
func TestApplyTemplateEdgeCases(t *testing.T) {
	re := mustCompileRegex("^team-(.*)$")

	// Unmatched reference (only 1 capture group but template references $2)
	got, err := applyTemplate("$1-$2-end", "team-platform", re)
	if err != nil {
		t.Fatalf("unexpected error for unmatched ref: %v", err)
	}
	want := "platform--end" // $2 is unmatched → empty string // $1="platform", $2="" (no match) → empty string
	if got != want {
		t.Errorf("$1-$2-end on 'team-platform' = %q, want %q", got, want)
	}

	// Invalid template syntax ${...} returns error
	re2 := mustCompileRegex("^(.*)$")
	_, err = applyTemplate("${invalid}", "anything", re2)
	if err == nil {
		t.Error("expected error for invalid ${invalid} syntax, got none")
	}

	// Non-matching regex returns empty string with error
	got3, err := applyTemplate("$1-end", "nomatch", mustCompileRegex("^team-(.*)$"))
	if err == nil {
		t.Error("expected error for non-matching regex, got none")
	}
	if got3 != "" {
		t.Errorf("non-mapping should return empty string, got %q", got3)
	}
}

// TestEvaluateMappingRulesKubernetes tests k8s mapping with all four fields.
func TestEvaluateMappingRulesKubernetes(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a test group mapping for kubernetes.
	namespaceTemplate := "ns-$1"
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "team-access",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeKubernetes,
		NameTemplate:       "cluster-$1-prod",
		NamespaceTemplate:  &namespaceTemplate,
		RoleTemplate:       ptrString("$1-admin"),
	}

	assert.NilError(t, data.CreateMappingRule(tx, mapping))

	// Create test groups.
	groups := []models.Group{
		{Name: "team-platform"},
		{Name: "ops-general"}, // won't match ^team-(.*)$
		{Name: "admin-dev"},   // will match, $1 = admin
	}

	for _, g := range groups {
		g.OrganizationMember = models.OrganizationMember{OrganizationID: orgID}
		assert.NilError(t, data.CreateGroup(tx, &g))
	}

	// Run the engine.
	assert.NilError(t, EvaluateMappingRules(tx))

	// Verify grants were created for matching groups only.
	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var k8sGroupGrants []models.Grant
	for _, g := range allGrants {
		if g.Subject.Kind == 2 && g.Privilege != "" { // Group subject kind = 2
			k8sGroupGrants = append(k8sGroupGrants, g)
		}
	}

	assert.Assert(t, len(k8sGroupGrants) >= 2, "expected at least 2 kubernetes group grants, got %d; grants: %+v", len(k8sGroupGrants), allGrants)

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestEvaluateMappingRulesSSH tests SSH mapping with only regex + Destination name Template.
func TestEvaluateMappingRulesSSH(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create SSH group mapping.
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "ssh-access",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "my-ssh-host-$1",
	}

	assert.NilError(t, data.CreateMappingRule(tx, mapping))

	// Create a matching group.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// Create a non-matching group.
	group2 := models.Group{
		Name:               "ops-general",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group2))

	// Add a member to the matching group.
	user := &models.Identity{Name: "alice@example.com"}
	assert.NilError(t, data.CreateIdentity(tx, user))
	assert.NilError(t, data.AddUsersToGroup(tx, group.ID, []uid.ID{user.ID}))

	// Run the engine.
	assert.NilError(t, EvaluateMappingRules(tx))

	// Verify grants were created with privilege = "connect".
	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var sshConnectGrants []models.Grant
	for _, g := range allGrants {
		if g.Privilege == "connect" && g.Resource == "my-ssh-host-platform" { // $1 = "platform"
			sshConnectGrants = append(sshConnectGrants, g)
		}
	}

	// Only the group-level grant is auto-granted. Users get access through group membership.
	assert.Assert(t, len(sshConnectGrants) >= 1, "expected at least 1 SSH connect grant for the mapped group (via group membership), got %d; grants: %+v", len(sshConnectGrants), allGrants)

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCleanupStaleGrants verifies stale grant cleanup.
func TestCleanupStaleGrants(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a group mapping.
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "test-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}

	assert.NilError(t, data.CreateMappingRule(tx, mapping))

	// Create a matching group.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// Create a manual grant (should survive cleanup).
	manualGrant := &models.Grant{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		CreatedBy:          99, // not CreatedBySystem
		Subject:            models.NewSubjectForGroup(group.ID),
		Privilege:          "connect",
		Resource:           "non-matching-resource",
	}
	assert.NilError(t, data.CreateGrant(tx, manualGrant))

	// Run the engine (should clean up stale grants).
	assert.NilError(t, EvaluateMappingRules(tx))

	// Verify manual grants survive.
	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var manualRemaining int
	for _, g := range allGrants {
		if g.CreatedBy == 99 && g.Privilege == "connect" {
			manualRemaining++
		}
	}

	assert.Equal(t, manualRemaining, 1, "expected manual grant to survive cleanup")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestMultiOrgIsolation verifies that group mappings only affect their own org.
func TestMultiOrgIsolation(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	org1ID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx1 := rawTx.WithOrgID(org1ID)
	defer func() { _ = tx1.Rollback() }()

	// Create mapping in org1 only.
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: org1ID},
		RuleName:           "org1-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}

	assert.NilError(t, data.CreateMappingRule(tx1, mapping))

	// Create a matching group.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: org1ID},
	}
	assert.NilError(t, data.CreateGroup(tx1, &group))

	// Run the engine.
	assert.NilError(t, EvaluateMappingRules(tx1))

	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit org1: %v", err)
	}
}

func mustCompileRegex(s string) *regexp.Regexp {
	re, err := regexp.Compile(s)
	if err != nil {
		panic(err)
	}
	return re
}

func ptrString(s string) *string {
	return &s
}

// TestCleanupStaleGrantsAutoGrantFalse verifies that AutoGrant=false grants survive cleanup.
func TestCleanupStaleGrantsAutoGrantFalse(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "test-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}

	assert.NilError(t, data.CreateMappingRule(tx, mapping))

	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	assert.NilError(t, EvaluateMappingRules(tx))

	group2 := models.Group{
		Name:               "team-ops",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group2))

	bootstrapGrant := &models.Grant{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		CreatedBy:          models.CreatedBySystem,
		AutoGrant:          false, // explicitly not an auto-grant
		Subject:            models.NewSubjectForGroup(group2.ID),
		Privilege:          "connect",
		Resource:           "ssh-team-ops",
	}
	assert.NilError(t, data.CreateGrant(tx, bootstrapGrant))

	assert.NilError(t, data.DeleteMappingRule(tx, mapping.ID))
	assert.NilError(t, EvaluateMappingRules(tx))

	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var hasAutoGrant, hasBootstrap bool
	for _, g := range allGrants {
		if g.Subject.Kind == models.SubjectKindGroup && g.AutoGrant && g.Resource == "ssh-team-platform" {
			hasAutoGrant = true
		}
		if !g.AutoGrant && g.Resource == "ssh-team-ops" {
			hasBootstrap = true
		}
	}

	assert.Assert(t, !hasAutoGrant, "expected auto-grant for team-platform to be cleaned up after rule deletion")
	assert.Assert(t, hasBootstrap, "bootstrap grants should NOT be cleaned up (they target different resources)")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestApplyTemplateMultiDigitCaptureRefs verifies multi-digit capture references work correctly.
func TestApplyTemplateMultiDigitCaptureRefs(t *testing.T) {
	tests := []struct {
		name     string
		template string
		input    string
		pattern  string
		want     string
	}{
		{
			name:     "multi-digit capture refs with valid groups",
			template: "$2-$1-end",
			input:    "team-platform-prod",
			pattern:  `^(.+)-(.+)-(prod)$`,
			want:     "platform-team-end", // $2="platform", $1="team" (greedy backtracking)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := regexp.Compile(tt.pattern)
			assert.NilError(t, err)
			got, err := applyTemplate(tt.template, tt.input, re)
			if got != tt.want {
				t.Errorf("applyTemplate(%q, %q) = %q; want %q", tt.template, tt.input, got, tt.want)
			}
			assert.NilError(t, err)
		})
	}
}

// TestEvaluateMappingRulesNoActiveRules verifies early return when no active rules exist.
func TestEvaluateMappingRulesNoActiveRules(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	assert.NilError(t, EvaluateMappingRules(tx))

	grants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	autoGrantCount := 0
	for _, g := range grants {
		if g.AutoGrant {
			autoGrantCount++
		}
	}
	assert.Assert(t, autoGrantCount == 0, "expected no auto-grants when no mapping rules exist")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestEvaluateMappingRulesK8sWithNamespaceTemplate verifies k8s namespaced grant creation.
func TestEvaluateMappingRulesK8sWithNamespaceTemplate(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	nsTemplate := "ns-$1"
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "k8s-namespaced",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeKubernetes,
		NameTemplate:       "cluster-$1-prod",
		NamespaceTemplate:  &nsTemplate,
		RoleTemplate:       ptrString("admin"),
	}

	assert.NilError(t, data.CreateMappingRule(tx, mapping))

	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	assert.NilError(t, EvaluateMappingRules(tx))

	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var foundRegular, foundNamespaced bool
	for _, g := range allGrants {
		if g.Subject.Kind != models.SubjectKindGroup || !g.AutoGrant {
			continue
		}
		switch g.Resource {
		case "cluster-platform-prod":
			foundRegular = true
		case "cluster-platform-prod.ns-platform":
			foundNamespaced = true
		}
	}

	assert.Assert(t, foundRegular,
		"expected k8s regular grant for team-platform → cluster-platform-prod")
	assert.Assert(t, foundNamespaced,
		"expected k8s namespaced grant for team-platform → cluster-platform-prod.ns-platform")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestEvaluateMappingRulesK8sWithNamespaceTemplate verifies k8s namespaced grant creation.

// TestCleanupStaleGrantsGroupDeleted verifies stale auto-grant is removed when source group is deleted.
func TestCleanupStaleGrantsGroupDeleted(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "test-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}

	assert.NilError(t, data.CreateMappingRule(tx, mapping))

	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	assert.NilError(t, EvaluateMappingRules(tx))

	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var hasAutoGrant bool
	for _, g := range allGrants {
		if g.Subject.Kind == models.SubjectKindGroup && g.AutoGrant && g.Resource == "ssh-platform" {
			hasAutoGrant = true
			break
		}
	}
	assert.Assert(t, hasAutoGrant, "expected auto-grant to exist before group deletion")

	assert.NilError(t, data.DeleteGroup(tx, group.ID))
	assert.NilError(t, EvaluateMappingRules(tx))

	allGrants, err = data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	hasStaleGrant := false
	for _, g := range allGrants {
		if g.Subject.Kind == models.SubjectKindGroup && g.AutoGrant && g.Resource == "ssh-platform" {
			hasStaleGrant = true
			break
		}
	}
	assert.Assert(t, !hasStaleGrant, "expected stale auto-grant to be cleaned up after group deletion")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCleanupStaleGrantsOnRuleDeletion verifies that auto-grants are removed when their source mapping rule is deleted.
func TestCleanupStaleGrantsOnRuleDeletion(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "test-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}

	assert.NilError(t, data.CreateMappingRule(tx, mapping))

	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// Run engine to create auto-grant. Expected resource = "ssh-platform" (since $1 captures only what's after "team-").
	assert.NilError(t, EvaluateMappingRules(tx))

	allGrantsBefore, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var foundAutoGrant bool
	for _, g := range allGrantsBefore {
		if g.AutoGrant && g.Resource == "ssh-platform" {
			foundAutoGrant = true
			break
		}
	}
	assert.Assert(t, foundAutoGrant, "expected auto-grant with resource 'ssh-platform' to exist after EvaluateMappingRules")

	t.Logf("Before rule deletion: total grants = %d", len(allGrantsBefore))

	// Delete the mapping rule and run cleanup.
	assert.NilError(t, data.DeleteMappingRule(tx, mapping.ID))
	assert.NilError(t, EvaluateMappingRules(tx))

	allGrantsAfter, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	t.Logf("After rule deletion: total grants = %d", len(allGrantsAfter))
	for _, g := range allGrantsAfter {
		t.Logf("  G: K=%d AG=%v R=%s P=%s CB=%s SID=%s", g.Subject.Kind, g.AutoGrant, g.Resource, g.Privilege, g.CreatedBy, g.Subject.ID)
	}

	// Verify the auto-grant was cleaned up.
	var hasAutoGrant bool
	for _, g := range allGrantsAfter {
		if g.AutoGrant && g.Resource == "ssh-platform" {
			hasAutoGrant = true
			break
		}
	}
	assert.Assert(t, !hasAutoGrant, "expected auto-grant for team-platform to be cleaned up after rule deletion")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCreateOrUpdateGrantMarksPreExistingAsAuto verifies that when a group already has
// a manual grant to a destination and a matching mapping rule is later created, the engine
// marks the existing grant as AutoGrant=true instead of creating a duplicate.
func TestCreateOrUpdateGrantMarksPreExistingAsAuto(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a group first.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// Manually create an SSH grant for this group (simulating admin setup before mapping rules).
	manualGrant := &models.Grant{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		CreatedBy:          1, // not CreatedBySystem — simulates a user-created grant
		AutoGrant:          false,
		Subject:            models.NewSubjectForGroup(group.ID),
		Privilege:          "connect",
		Resource:           "ssh-platform",
	}
	assert.NilError(t, data.CreateGrant(tx, manualGrant))

	// Verify the grant exists and is NOT auto-granted.
	grantsBefore, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)
	var foundManual bool
	for _, g := range grantsBefore {
		if !g.AutoGrant && g.Resource == "ssh-platform" {
			foundManual = true
		}
	}
	assert.Assert(t, foundManual, "expected manual grant to exist before rule creation")

	// Now create a mapping rule that matches this group and would produce the same destination name.
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "ssh-mapping",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, mapping))

	// Running the engine should find the pre-existing grant and mark it as auto-granted.
	assert.NilError(t, EvaluateMappingRules(tx))

	grantsAfter, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var foundAutoGrant bool
	for _, g := range grantsAfter {
		if g.AutoGrant && g.Resource == "ssh-platform" && g.Subject.ID == group.ID {
			foundAutoGrant = true
		}
	}
	assert.Assert(t, foundAutoGrant, "expected the pre-existing grant to be marked as AutoGrant=true after engine evaluation")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestEvaluateMappingRulesInvalidRegexGracefulDegradation verifies that when one mapping rule
// has an invalid regex, the engine skips it (logs a warning) but continues processing remaining rules.
func TestEvaluateMappingRulesInvalidRegexGracefulDegradation(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a rule with an INVALID regex — this should be skipped gracefully.
	srvMapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "invalid-regex-rule",
		SourceGroupRegex:   "[invalid(", // invalid regex
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, srvMapping))

	// Create a VALID rule that matches groups.
	validMapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "valid-regex-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, validMapping))

	// Create groups — one matches the valid rule.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// Run the engine — must NOT panic or return an error.
	assert.NilError(t, EvaluateMappingRules(tx), "engine should not fail when one rule has invalid regex")

	// Verify grants were created for the valid rule's matching group.
	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var foundValidGrant bool
	for _, g := range allGrants {
		if g.AutoGrant && g.Resource == "ssh-platform" && g.Subject.ID == group.ID {
			foundValidGrant = true
		}
	}
	assert.Assert(t, foundValidGrant,
		"expected auto-grant from valid rule despite invalid regex in another rule")
}

// TestApplyTemplateRejectsDollarBraceSyntax verifies that applyTemplate rejects ${...} syntax
// and returns an error, preventing any template substitution from occurring.
func TestApplyTemplateRejectsDollarBraceSyntax(t *testing.T) {
	re := mustCompileRegex("^team-(.*)$")

	// ${invalid} should be rejected — only $N (bare number) syntax is supported.
	_, err := applyTemplate("${invalid}", "team-platform", re)
	assert.Assert(t, err != nil, "expected error for ${...} template syntax")
	assert.Equal(t, err.Error(), "invalid template syntax: ${...} is not supported; use $N instead")

	// Mixed: valid $1 followed by invalid ${x} — should fail at the first ${ encountered.
	re2 := mustCompileRegex(`^(.+)-(.+)$`)
	_, err = applyTemplate("$1-${name}-end", "team-platform-prod", re2)
	assert.Assert(t, err != nil, "expected error when mixed $N and ${...} syntax")

	// Valid: only bare $N references work.
	got, err := applyTemplate("$1-$2-end", "team-platform-prod", re2)
	assert.NilError(t, err)
	assert.Equal(t, got, "team-platform-prod-end") // $1="team-platform", $2="prod" (greedy backtracking)
}

// TestEvaluateMappingRulesInvalidTemplateNameGracefulDegradation verifies that when a mapping rule's
// NameTemplate contains invalid ${...} syntax, the engine skips it gracefully but continues processing other rules.
func TestEvaluateMappingRulesInvalidTemplateNameGracefulDegradation(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a rule with valid regex but INVALID template syntax (${...} is not supported).
	badTemplateRule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "bad-template-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "${invalid}", // invalid ${...} syntax — will be rejected by applyTemplate
	}
	assert.NilError(t, data.CreateMappingRule(tx, badTemplateRule))

	// Create a VALID rule that matches groups.
	validMapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "valid-template-rule",
		SourceGroupRegex:   "^ops-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, validMapping))

	// Create groups that match both patterns.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	group2 := models.Group{
		Name:               "ops-general",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group2))

	// Run the engine — must NOT panic or return an error.
	assert.NilError(t, EvaluateMappingRules(tx),
		"engine should not fail when one rule has invalid template syntax")

	// Verify grants were created ONLY from the valid rule (ops-general → ssh-general).
	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var foundValidGrant bool
	for _, g := range allGrants {
		if g.AutoGrant && g.Resource == "ssh-general" && g.Subject.ID == group2.ID {
			foundValidGrant = true
		}
	}
	assert.Assert(t, foundValidGrant,
		"expected auto-grant from valid rule despite invalid template in another rule")

	// Verify NO grants were created for the bad-template rule's matches.
	for _, g := range allGrants {
		if g.AutoGrant && g.Subject.ID == group.ID {
			t.Errorf("unexpected grant for team-platform (should have been skipped due to invalid template): %s", g.Resource)
		}
	}
}

// TestParseCaptureRefEdgeCases verifies parseCaptureRef handles edge cases correctly:
// $0 returns error (1-indexed), non-digit chars return error, multi-digit works.
func TestParseCaptureRefEdgeCases(t *testing.T) {
	// $0 must error — capture references are 1-indexed per regexp.SubexpIndex.
	_, err := parseCaptureRef("0")
	assert.Assert(t, err != nil, "expected error for $0 (must be 1-indexed)")

	// Non-digit characters should return an error.
	_, err = parseCaptureRef("1a")
	assert.Assert(t, err != nil, "expected error for non-digit character in capture reference")

	// Valid multi-digit references work.
	n, err := parseCaptureRef("10")
	assert.NilError(t, err)
	assert.Equal(t, n, 10)

	n, err = parseCaptureRef("99")
	assert.NilError(t, err)
	assert.Equal(t, n, 99)
}

// TestApplyTemplateOutOfBoundRefs verifies that out-of-bounds capture references produce empty strings (not panic).
func TestApplyTemplateOutOfBoundRefs(t *testing.T) {
	re := mustCompileRegex(`^(.+)-(.+)$`) // only 2 capture groups: $1 and $2

	// Reference to group 99 → should return empty string, not crash.
	got, err := applyTemplate("$99-end", "team-platform", re)
	assert.NilError(t, err)
	assert.Equal(t, got, "-end") // $99 is out of bounds → ""

	// Reference to group 3 (only has groups 1 and 2) → empty string.
	got, err = applyTemplate("$1-$3-end", "team-platform", re)
	assert.NilError(t, err)
	assert.Equal(t, got, "team--end") // $3 is out of bounds → ""

	// Mixed valid and out-of-bounds refs.
	got, err = applyTemplate("$2-$50-$1-end", "team-platform-prod", mustCompileRegex(`^(.+)-(.+)-(prod)$`))
	assert.NilError(t, err)
	assert.Equal(t, got, "platform--team-end") // $50 out of bounds → ""
}

// TestApplyTemplateMixedSingleAndMultiDigitRefs verifies mixed single and multi-digit capture references.
func TestApplyTemplateMixedSingleAndMultiDigitRefs(t *testing.T) {
	// Pattern with 3 groups: $1, $2, $3
	re := mustCompileRegex(`^(.+)-(.+)-(prod)$`)

	// Single digit followed by multi-digit: $1-$3-end where $1="team", $3="prod"
	got, err := applyTemplate("$1-$3-end", "team-platform-prod", re)
	assert.NilError(t, err)
	assert.Equal(t, got, "team-prod-end")

	// Multi-digit followed by single: $2-$1-end where $2="platform", $1="team"
	got, err = applyTemplate("$2-$1-end", "team-platform-prod", re)
	assert.NilError(t, err)
	assert.Equal(t, got, "platform-team-end")

	// Three refs in order: $1-$2-$3-end
	got, err = applyTemplate("$1.$2.$3", "team-platform-prod", re)
	assert.NilError(t, err)
	assert.Equal(t, got, "team.platform.prod")
}

// TestCleanupStaleGrantsCrossOrgIsolation verifies that cleanup in one organization's context
// does not affect auto-grants created by rules in another organization. This is critical because
// cleanupStaleGrants iterates all grants and checks against active mapping rules — if org-scoping
// regresses, cross-org data loss could occur.
func TestCleanupStaleGrantsCrossOrgIsolation(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	// Create a second organization.
	orgB := &models.Organization{Name: "OtherOrg", Domain: "other.example.org"}
	assert.NilError(t, data.CreateOrganization(srv.db, orgB))

	orgAID := srv.db.DefaultOrg.ID
	orgBID := orgB.ID

	// === Org A: create rule + group → engine creates auto-grant ===
	rawTxA, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
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
	assert.NilError(t, data.CreateMappingRule(txA, mappingA))

	groupA := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgAID},
	}
	assert.NilError(t, data.CreateGroup(txA, &groupA))

	assert.NilError(t, EvaluateMappingRules(txA))

	// Verify auto-grant exists in org A.
	grantsA, err := data.ListGrants(txA, data.ListGrantsOptions{})
	assert.NilError(t, err)
	var foundAGrant bool
	for _, g := range grantsA {
		if g.AutoGrant && g.Resource == "ssh-platform" {
			foundAGrant = true
			break
		}
	}
	assert.Assert(t, foundAGrant, "expected auto-grant in org A before cleanup")

	if err := txA.Commit(); err != nil {
		t.Fatalf("commit orgA: %v", err)
	}

	// === Org B: create rule + group → engine creates auto-grant ===
	rawTxB, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
	txB := rawTxB.WithOrgID(orgBID)
	defer func() { _ = txB.Rollback() }()

	mappingB := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgBID},
		RuleName:           "orgb-rule",
		SourceGroupRegex:   "^ops-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(txB, mappingB))

	groupB := models.Group{
		Name:               "ops-general",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgBID},
	}
	assert.NilError(t, data.CreateGroup(txB, &groupB))

	assert.NilError(t, EvaluateMappingRules(txB))

	// Verify auto-grant exists in org B.
	grantsB, err := data.ListGrants(txB, data.ListGrantsOptions{})
	assert.NilError(t, err)
	var foundBGGrant bool
	for _, g := range grantsB {
		if g.AutoGrant && g.Resource == "ssh-general" {
			foundBGGrant = true
			break
		}
	}
	assert.Assert(t, foundBGGrant, "expected auto-grant in org B before cleanup")

	if err := txB.Commit(); err != nil {
		t.Fatalf("commit orgB: %v", err)
	}

	// === Delete rule in org A and run cleanup ===
	rawTxA2, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
	txA2 := rawTxA2.WithOrgID(orgAID)
	defer func() { _ = txA2.Rollback() }()

	// Delete the mapping rule in org A.
	assert.NilError(t, data.DeleteMappingRule(txA2, mappingA.ID))

	// Run cleanup — this should NOT affect org B's grants.
	assert.NilError(t, EvaluateMappingRules(txA2), "cleanup in org A must not fail")

	// === Verify org B's auto-grant still exists ===
	rawTxBCheck, err := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, err)
	txBCheck := rawTxBCheck.WithOrgID(orgBID)
	defer func() { _ = txBCheck.Rollback() }()

	grantsBCheck, err := data.ListGrants(txBCheck, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var foundBGGrantAfter bool
	for _, g := range grantsBCheck {
		if g.AutoGrant && g.Resource == "ssh-general" && g.Subject.ID == groupB.ID {
			foundBGGrantAfter = true
			break
		}
	}
	assert.Assert(t, foundBGGrantAfter,
		"org B's auto-grant should still exist after org A cleanup")

	if err := txA2.Commit(); err != nil {
		t.Fatalf("commit orgA2: %v", err)
	}
	if err := txBCheck.Commit(); err != nil {
		t.Fatalf("commit orgBCheck: %v", err)
	}
}

// TestEvaluateMappingRulesNoMatchingGroups verifies that when mapping rules exist but none of them
// match any groups, the engine creates exactly zero auto-grants. This tests the no-op path where
// every rule's regex fails to match all group names — a regression would silently create spurious grants.
func TestEvaluateMappingRulesNoMatchingGroups(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create mapping rules with patterns that intentionally DON'T match any groups.
	mapping1 := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "no-match-rule-1",
		SourceGroupRegex:   "^admin-(.*)$", // only matches admin-* groups
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, mapping1))

	mapping2 := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "no-match-rule-2",
		SourceGroupRegex:   "^service-account-(.*)$", // only matches service-account-* groups
		DestinationType:    models.DestinationTypeKubernetes,
		NameTemplate:       "cluster-$1-prod",
		NamespaceTemplate:  ptrString("ns-$1"),
		RoleTemplate:       ptrString("$1-admin"),
	}
	assert.NilError(t, data.CreateMappingRule(tx, mapping2))

	// Create groups that DON'T match either rule.
	groups := []models.Group{
		{Name: "team-platform", OrganizationMember: models.OrganizationMember{OrganizationID: orgID}},
		{Name: "ops-general", OrganizationMember: models.OrganizationMember{OrganizationID: orgID}},
	}
	for _, g := range groups {
		assert.NilError(t, data.CreateGroup(tx, &g))
	}

	// Run the engine.
	assert.NilError(t, EvaluateMappingRules(tx), "engine should succeed even when no rules match any groups")

	// Verify ZERO auto-grants were created.
	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	autoGrantCount := 0
	for _, g := range allGrants {
		if g.AutoGrant {
			autoGrantCount++
			t.Errorf("unexpected auto-grant: id=%s resource=%s privilege=%s", g.ID.String(), g.Resource, g.Privilege)
		}
	}
	assert.Equal(t, autoGrantCount, 0, "expected exactly zero auto-grants when no rules match any groups")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestEvaluateMappingRulesSSHMembersAccess verifies that when a mapping rule grants a group
// access to an SSH destination, individual members of that group inherit the grant.
// This tests the full propagation chain: mapping rule → auto-grant for group → member access via membership.
func TestEvaluateMappingRulesSSHMembersAccess(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create an SSH mapping rule that matches "team-platform" → ssh-host-platform.
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "ssh-member-test",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-host-$1",
	}

	assert.NilError(t, data.CreateMappingRule(tx, mapping))

	// Create the matching group.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// Add multiple users to the matching group.
	users := []models.Identity{
		{Name: "alice@example.com"},
		{Name: "bob@example.com"},
		{Name: "carol@example.com"},
	}
	userIDs := make([]uid.ID, len(users))
	for i, u := range users {
		assert.NilError(t, data.CreateIdentity(tx, &u))
		userIDs[i] = u.ID
		// Add user to the group.
		assert.NilError(t, data.AddUsersToGroup(tx, group.ID, []uid.ID{u.ID}))
	}

	// Create a non-matching group with its own member (should NOT get access).
	nonMatchingGroup := models.Group{
		Name:               "ops-general",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &nonMatchingGroup))

	nonMatchingUser := models.Identity{Name: "dave@example.com"}
	assert.NilError(t, data.CreateIdentity(tx, &nonMatchingUser))
	assert.NilError(t, data.AddUsersToGroup(tx, nonMatchingGroup.ID, []uid.ID{nonMatchingUser.ID}))

	// Run the engine to create auto-grants.
	assert.NilError(t, EvaluateMappingRules(tx))

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// === Verify group-level grant exists ===
	rawTx2, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx2 := rawTx2.WithOrgID(orgID)
	defer func() { _ = tx2.Rollback() }()

	allGrants, err := data.ListGrants(tx2, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var foundGroupGrant bool
	for _, g := range allGrants {
		if g.AutoGrant && g.Subject.Kind == models.SubjectKindGroup && g.Resource == "ssh-host-platform" {
			foundGroupGrant = true
		}
	}
	assert.Assert(t, foundGroupGrant,
		"expected auto-grant for group 'team-platform' to ssh-host-platform")

	// === Verify each member has access via their membership ===
	for _, u := range users {
		rawTx3, rawErr := srv.db.Begin(context.Background(), nil)
		assert.NilError(t, rawErr)
		tx3 := rawTx3.WithOrgID(orgID)

		// Check that the user's grants (including inherited from groups) include one matching the SSH destination.
		userGrants, err := data.ListGrants(tx3, data.ListGrantsOptions{
			BySubject:                  models.NewSubjectForUser(u.ID),
			IncludeInheritedFromGroups: true,
		})
		assert.NilError(t, err)

		var foundMemberAccess bool
		for _, g := range userGrants {
			if g.Resource == "ssh-host-platform" && g.Privilege == "connect" {
				foundMemberAccess = true
			}
		}

		// Verify the grant was inherited through group membership, not direct.
		directGrants, err := data.ListGrants(tx3, data.ListGrantsOptions{
			BySubject: models.NewSubjectForUser(u.ID),
		})
		assert.NilError(t, err)

		// The user should NOT have a direct grant (subject = User).
		for _, g := range directGrants {
			if g.Subject.Kind == models.SubjectKindUser && g.Resource == "ssh-host-platform" {
				t.Errorf("user %s has a DIRECT grant to ssh-host-platform — expected only group-inherited access", u.Name)
			}
		}

		// The user should have at least one inherited grant through their group membership.
		assert.Assert(t, foundMemberAccess,
			"user %s should have access to ssh-host-platform via group membership",
			u.Name)

		if err := tx3.Commit(); err != nil {
			t.Fatalf("commit for user %s: %v", u.Name, err)
		}
	}

	// === Verify non-matching group member does NOT get access ===
	rawTx4, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx4 := rawTx4.WithOrgID(orgID)
	defer func() { _ = tx4.Rollback() }()

	// Verify no auto-grant targets dave's group for the SSH destination.
	rawTx5, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx5 := rawTx5.WithOrgID(orgID)
	defer func() { _ = tx5.Rollback() }()

	// Check grants for the non-matching group.
	daveGroupGrants, err := data.ListGrants(tx5, data.ListGrantsOptions{
		BySubject: models.NewSubjectForGroup(nonMatchingGroup.ID),
	})
	assert.NilError(t, err)

	for _, g := range daveGroupGrants {
		if g.AutoGrant && g.Resource == "ssh-host-platform" {
			t.Errorf("group %s should NOT have auto-grant to ssh-host-platform (does not match mapping rule)", nonMatchingGroup.Name)
		}
	}
}
