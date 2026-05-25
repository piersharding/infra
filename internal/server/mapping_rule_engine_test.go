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
		if g.CreatedBy == models.CreatedBySystem && !g.AutoGrant && g.Resource == "ssh-team-ops" {
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
