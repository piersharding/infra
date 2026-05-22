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

// TestEvaluateGroupMappingsKubernetes tests k8s mapping with all four fields.
func TestEvaluateGroupMappingsKubernetes(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a test group mapping for kubernetes.
	namespaceTemplate := "ns-$1"
	mapping := &models.GroupMapping{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "team-access",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeKubernetes,
		NameTemplate:       "cluster-$1-prod",
		NamespaceTemplate:  &namespaceTemplate,
		RoleTemplate:       ptrString("$1-admin"),
	}

	assert.NilError(t, data.CreateGroupMapping(tx, mapping))

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
	assert.NilError(t, EvaluateGroupMappings(tx))

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

// TestEvaluateGroupMappingsSSH tests SSH mapping with only regex + name template.
func TestEvaluateGroupMappingsSSH(t *testing.T) {
	srv := setupServer(t, withAdminUser)

	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create SSH group mapping.
	mapping := &models.GroupMapping{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "ssh-access",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "my-ssh-host-$1",
	}

	assert.NilError(t, data.CreateGroupMapping(tx, mapping))

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
	assert.NilError(t, EvaluateGroupMappings(tx))

	// Verify grants were created with privilege = "connect".
	allGrants, err := data.ListGrants(tx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var sshConnectGrants []models.Grant
	for _, g := range allGrants {
		if g.Privilege == "connect" && g.Resource == "my-ssh-host-platform" { // $1 = "platform"
			sshConnectGrants = append(sshConnectGrants, g)
		}
	}

	assert.Assert(t, len(sshConnectGrants) >= 2, "expected at least 2 SSH grants (group + user), got %d; grants: %+v", len(sshConnectGrants), allGrants)

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
	mapping := &models.GroupMapping{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "test-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}

	assert.NilError(t, data.CreateGroupMapping(tx, mapping))

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
	assert.NilError(t, EvaluateGroupMappings(tx))

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
	mapping := &models.GroupMapping{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: org1ID},
		RuleName:           "org1-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}

	assert.NilError(t, data.CreateGroupMapping(tx1, mapping))

	// Create a matching group.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: org1ID},
	}
	assert.NilError(t, data.CreateGroup(tx1, &group))

	// Run the engine.
	assert.NilError(t, EvaluateGroupMappings(tx1))

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
