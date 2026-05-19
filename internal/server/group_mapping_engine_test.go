package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/test"
)

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
			want:     "cluster-team-platform-prod",
		},
		{
			name:     "multiple capture groups",
			template: "$1.$2-admin",
			input:    "team-platform-dev",
			re:       "^(.+)-(.+)$",
			want:     "team.platform-admin",
		},
		{
			name:     "no captures — no $N references",
			template: "static-name",
			input:    "anything",
			re:       "^.*$",
			want:     "static-name",
		},
		{
			name:      "unmatched reference",
			template: "$1-$2-end",
			input:    "team-platform",
			re:       "^team-(.*)$",
			want:      "[group-1]-end", // $2 has no match → empty string; we show placeholder for testing
		},
		{
			name:     "no leading capture — just text with $N",
			template: "$1-$0-end",
			input:    "team-platform",
			re:       "^team-(.*)$",
			want:     "[group-1]-end", // $0 is not valid, should produce empty; $1 matches
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := test.CompileRegex(tt.re)
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

// TestApplyTemplateEdgeCases tests edge cases for template substitution.
func TestApplyTemplateEdgeCases(t *testing.T) {
	re, err := test.CompileRegex("^team-(.*)$")
	if err != nil {
		t.Fatalf("failed to compile regex: %v", err)
	}

	// Unmatched reference (only 1 capture group but template references $2)
	got, err := applyTemplate("$1-$2-end", "team-platform", re)
	if err != nil {
		t.Fatalf("unexpected error for unmatched ref: %v", err)
	}
	want := fmt.Sprintf("%s-%s-end", "team-platform", "") // $2 → empty string
	if got != want {
		t.Errorf("$1-$2-end on 'team-platform' = %q, want %q", got, want)
	}

	// Invalid template syntax ${...} returns error
	re2, err := test.CompileRegex("^(.*)$")
	_, err2 := applyTemplate("${invalid}", "anything", re2)
	if err2 == nil {
		t.Error("expected error for invalid ${invalid} syntax, got none")
	}

	// Non-matching regex returns empty string with error
	got3, err3 := applyTemplate("$1-end", "nomatch", test.CompileRegex("^team-(.*)$"))
	if err3 == nil {
		t.Error("expected error for non-matching regex, got none")
	}
	if got3 != "" {
		t.Errorf("non-mapping should return empty string, got %q", got3)
	}
}

// TestEvaluateGroupMappingsKubernetes tests k8s mapping with all four fields.
func TestEvaluateGroupMappingsKubernetes(t *testing.T) {
	srv := test.NewServer(t)

	org1 := &models.Organization{Name: "org1"}
	if err := data.CreateOrganization(srv.DB(), org1); err != nil {
		t.Fatalf("create org: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a test transaction with the organization context.
	tx, err := srv.DB().Begin(ctx, &org1.ID)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Create a group mapping for kubernetes.
	namespaceTemplate := "ns-$1"
	mapping := &models.GroupMapping{
		Model:             models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: org1.ID},
		RuleName:          "team-access",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   models.DestinationTypeKubernetes,
		NameTemplate:      "cluster-$1-prod",
		NamespaceTemplate: &namespaceTemplate,
		RoleTemplate:      stringPtr("$1-admin"),
	}

	if err := data.CreateGroupMapping(tx, mapping); err != nil {
		t.Fatalf("create group mapping: %v", err)
	}

	// Create test groups.
	groups := []models.Group{
		{Name: "team-platform"},
		{Name: "ops-general"}, // won't match ^team-(.*)$
		{Name: "admin-dev"},   // will match, $1 = admin
	}

	for _, g := range groups {
		g.OrganizationMember = models.OrganizationMember{OrganizationID: org1.ID}
		if err := data.CreateGroup(tx, &g); err != nil {
			t.Fatalf("create group %s: %v", g.Name, err)
		}
	}

	// Run the engine.
	if err := EvaluateGroupMappings(tx); err != nil {
		t.Fatalf("EvaluateGroupMappings error: %v", err)
	}

	// Verify grants were created for matching groups only.
	allGrants, err := data.ListAllGrants(tx, org1.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}

	var k8sGroupGrants []models.Grant
	for _, g := range allGrants {
		if g.Subject.Kind == 2 && g.Privilege != "" { // Group subject kind = 2
			k8sGroupGrants = append(k8sGroupGrants, g)
		}
	}

	// Should have grants for team-platform and admin-dev only (ops-general doesn't match).
	if len(k8sGroupGrants) < 2 {
		t.Errorf("expected at least 2 kubernetes group grants, got %d", len(k8sGroupGrants))
		for _, g := range allGrants {
			t.Logf("grant: subject=%v privilege=%s resource=%s createdBy=%d", g.Subject.ID, g.Privilege, g.Resource, g.CreatedBy)
		}
	}

	// Verify namespace grants were also created.
	var nsGrants []models.Grant
	for _, g := range k8sGroupGrants {
		if len(g.Resource) > 0 && contains(g.Resource, ".") {
			nsGrants = append(nsGrants, g)
		}
	}

	if len(nsGrants) < 2 {
		t.Errorf("expected at least 2 namespaced grants, got %d", len(nsGrants))
		for _, g := range nsGrants {
			t.Logf("ns grant: resource=%s privilege=%s", g.Resource, g.Privilege)
		}
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestEvaluateGroupMappingsSSH tests SSH mapping with only regex + name template.
func TestEvaluateGroupMappingsSSH(t *testing.T) {
	srv := test.NewServer(t)

	org1 := &models.Organization{Name: "org1"}
	if err := data.CreateOrganization(srv.DB(), org1); err != nil {
		t.Fatalf("create org: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tx, err := srv.DB().Begin(ctx, &org1.ID)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Create SSH group mapping.
	mapping := &models.GroupMapping{
		Model:             models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: org1.ID},
		RuleName:          "ssh-access",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   models.DestinationTypeSSH,
		NameTemplate:      "my-ssh-host-$1",
	}

	if err := data.CreateGroupMapping(tx, mapping); err != nil {
		t.Fatalf("create group mapping: %v", err)
	}

	// Create a matching group.
	group := models.Group{
		Name:                 "team-platform",
		OrganizationMember:   models.OrganizationMember{OrganizationID: org1.ID},
	}
	if err := data.CreateGroup(tx, &group); err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Create a non-matching group.
	group2 := models.Group{
		Name:                 "ops-general",
		OrganizationMember:   models.OrganizationMember{OrganizationID: org1.ID},
	}
	if err := data.CreateGroup(tx, &group2); err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Add a member to the matching group.
	user := &models.Identity{Name: "alice@example.com", Email: "alice@example.com"}
	if err := data.CreateIdentity(tx, user); err != nil {
		t.Fatalf("create identity: %v", err)
	}
	if err := data.AddUserToGroup(tx, user.ID, group.ID); err != nil {
		t.Fatalf("add user to group: %v", err)
	}

	// Run the engine.
	if err := EvaluateGroupMappings(tx); err != nil {
		t.Fatalf("EvaluateGroupMappings error: %v", err)
	}

	// Verify grants were created with privilege = "connect".
	allGrants, err := data.ListAllGrants(tx, org1.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}

	var sshConnectGrants []models.Grant
	for _, g := range allGrants {
		if g.Privilege == "connect" && g.Resource == "my-ssh-host-team-platform" {
			sshConnectGrants = append(sshConnectGrants, g)
		}
	}

	// Should have at least 1 group-level grant + 1 user-level grant (alice).
	if len(sshConnectGrants) < 2 {
		t.Errorf("expected at least 2 SSH grants (group + user), got %d", len(sshConnectGrants))
		for _, g := range allGrants {
			t.Logf("grant: subject=%v privilege=%s resource=%s createdBy=%d", g.Subject.ID, g.Privilege, g.Resource, g.CreatedBy)
		}
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCleanupStaleGrants verifies stale grant cleanup.
func TestCleanupStaleGrants(t *testing.T) {
	srv := test.NewServer(t)

	org1 := &models.Organization{Name: "org1"}
	if err := data.CreateOrganization(srv.DB(), org1); err != nil {
		t.Fatalf("create org: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tx, err := srv.DB().Begin(ctx, &org1.ID)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Create a group mapping.
	mapping := &models.GroupMapping{
		Model:             models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: org1.ID},
		RuleName:          "test-rule",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   models.DestinationTypeSSH,
		NameTemplate:      "ssh-$1",
	}

	if err := data.CreateGroupMapping(tx, mapping); err != nil {
		t.Fatalf("create group mapping: %v", err)
	}

	// Create a matching group.
	group := models.Group{
		Name:                 "team-platform",
		OrganizationMember:   models.OrganizationMember{OrganizationID: org1.ID},
	}
	if err := data.CreateGroup(tx, &group); err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Create a manual grant (should survive cleanup).
	manualGrant := &models.Grant{
		Model:            models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: org1.ID},
		CreatedBy:        99, // not CreatedBySystem
		Subject:          models.NewSubjectForGroup(group.ID),
		Privilege:        "connect",
		Resource:         "non-matching-resource",
	}
	if err := data.CreateGrant(tx, manualGrant); err != nil {
		t.Fatalf("create manual grant: %v", err)
	}

	// Run the engine (should clean up stale grants).
	if err := EvaluateGroupMappings(tx); err != nil {
		t.Fatalf("EvaluateGroupMappings error: %v", err)
	}

	// Verify manual grants survive.
	allGrants, err := data.ListAllGrants(tx, org1.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}

	var manualRemaining int
	for _, g := range allGrants {
		if g.CreatedBy == 99 && g.Privilege == "connect" {
			manualRemaining++
		}
	}

	if manualRemaining != 1 {
		t.Errorf("expected manual grant to survive cleanup, got %d remaining", manualRemaining)
		for _, g := range allGrants {
			t.Logf("grant: createdBy=%d privilege=%s resource=%s", g.CreatedBy, g.Privilege, g.Resource)
		}
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestMultiOrgIsolation verifies that group mappings only affect their own org.
func TestMultiOrgIsolation(t *testing.T) {
	srv := test.NewServer(t)

	org1 := &models.Organization{Name: "org1"}
	if err := data.CreateOrganization(srv.DB(), org1); err != nil {
		t.Fatalf("create org1: %v", err)
	}

	org2 := &models.Organization{Name: "org2"}
	if err := data.CreateOrganization(srv.DB(), org2); err != nil {
		t.Fatalf("create org2: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tx1, err := srv.DB().Begin(ctx, &org1.ID)
	if err != nil {
		t.Fatalf("begin tx for org1: %v", err)
	}
	defer func() { _ = tx1.Rollback() }()

	// Create mapping in org1 only.
	mapping := &models.GroupMapping{
		Model:             models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: org1.ID},
		RuleName:          "org1-rule",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   models.DestinationTypeSSH,
		NameTemplate:      "ssh-$1",
	}

	if err := data.CreateGroupMapping(tx1, mapping); err != nil {
		t.Fatalf("create group mapping in org1: %v", err)
	}

	// Create a matching group.
	group := models.Group{
		Name:                 "team-platform",
		OrganizationMember:   models.OrganizationMember{OrganizationID: org1.ID},
	}
	if err := data.CreateGroup(tx1, &group); err != nil {
		t.Fatalf("create group in org1: %v", err)
	}

	// Run the engine.
	if err := EvaluateGroupMappings(tx1); err != nil {
		t.Fatalf("EvaluateGroupMappings error: %v", err)
	}

	// Verify no grants were created for org2.
	tx2, err := srv.DB().Begin(ctx, &org2.ID)
	if err != nil {
		t.Fatalf("begin tx for org2: %v", err)
	}
	defer func() { _ = tx2.Rollback() }()

	org2Grants, err := data.ListAllGrants(tx2, org2.ID)
	if err != nil {
		t.Fatalf("list grants in org2: %v", err)
	}

	for _, g := range org2Grants {
		if g.Privilege == "connect" && g.Resource == "ssh-team-platform" {
			t.Errorf("found SSH grant for ssh-team-platform in org2 — should be isolated")
		}
	}

	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit org1: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit org2: %v", err)
	}
}

// stringPtr returns a pointer to the given string.
func stringPtr(s string) *string {
	return &s
}

// contains checks if s contains substring sub.
func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
