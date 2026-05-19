package server

import (
	"net/http"
	"testing"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/test"
)

func TestAPI_CreateGroupMapping(t *testing.T) {
	srv := test.NewServer(t)
	admin := test.CreateIdentity(t, srv.DB(), "admin@example.com")
	test.AddUserToOrg(t, srv.DB(), admin.ID, models.InfraAdminRole, &srv.Options().Internal.OrgID)

	tests := []struct {
		name         string
		request      api.CreateGroupMappingRequest
		wantStatus   int
	}{
		{
			name: "valid kubernetes mapping",
			request: api.CreateGroupMappingRequest{
				RuleName:          "team-access",
				SourceGroupRegex:  "^team-(.*)$",
				DestinationType:   "kubernetes",
				NameTemplate:      "cluster-$1-prod",
				RoleTemplate:      stringPtr("$1-admin"),
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "valid SSH mapping",
			request: api.CreateGroupMappingRequest{
				RuleName:          "ssh-access",
				SourceGroupRegex:  "^team-(.*)$",
				DestinationType:   "ssh",
				NameTemplate:      "my-ssh-host-$1",
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:         "missing rule_name returns 400",
			request:      api.CreateGroupMappingRequest{},
			wantStatus:   http.StatusBadRequest,
		},
		{
			name: "missing source_group_regex returns 400",
			request: api.CreateGroupMappingRequest{
				RuleName: "test-rule",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing name_template returns 400",
			request: api.CreateGroupMappingRequest{
				RuleName:         "test-rule",
				SourceGroupRegex: "^team-(.*)$",
				DestinationType:  "ssh",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "invalid regex returns 400",
			request: api.CreateGroupMappingRequest{
				RuleName:         "test-rule",
				SourceGroupRegex: "[invalid(",
				DestinationType:  "ssh",
				NameTemplate:     "ssh-$1",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "kubernetes without role_template returns 400",
			request: api.CreateGroupMappingRequest{
				RuleName:         "team-access",
				SourceGroupRegex: "^team-(.*)$",
				DestinationType:  "kubernetes",
				NameTemplate:     "cluster-$1-prod",
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := test.DoRequest(srv.API(), http.MethodPost, "/api/group-mappings", tt.request, nil)
			if err != nil {
				t.Fatalf("request error: %v", err)
			}

			if resp.StatusCode != tt.wantStatus {
				body, _ := test.ReadBody(resp)
				t.Errorf("status = %d, want %d; body: %s", resp.StatusCode, tt.wantStatus, string(body))
			}
		})
	}
}

func TestAPI_GetGroupMapping(t *testing.T) {
	srv := test.NewServer(t)
	admin := test.CreateIdentity(t, srv.DB(), "admin@example.com")
	test.AddUserToOrg(t, srv.DB(), admin.ID, models.InfraAdminRole, &srv.Options().Internal.OrgID)

	// Create a group mapping first.
	nsTemplate := "ns-$1"
	mappingReq := api.CreateGroupMappingRequest{
		RuleName:          "test-rule",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   "kubernetes",
		NameTemplate:      "cluster-$1-prod",
		NamespaceTemplate: &nsTemplate,
		RoleTemplate:      stringPtr("$1-admin"),
	}

	resp, err := test.DoRequest(srv.API(), http.MethodPost, "/api/group-mappings", mappingReq, nil)
	if err != nil {
		t.Fatalf("create request error: %v", err)
	}

	var created api.GroupMapping
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		test.ReadJSON(t, resp, &created)
	} else {
		body, _ := test.ReadBody(resp)
		t.Fatalf("unexpected status creating mapping: %d; body: %s", resp.StatusCode, string(body))
	}

	tests := []struct {
		name       string
		id         string
		wantStatus int
	}{
		{
			name:       "get existing mapping returns 200",
			id:         created.ID.String(),
			wantStatus: http.StatusOK,
		},
		{
			name:       "get non-existent mapping returns 404",
			id:         "nonexistent-id-1234567890",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := test.DoRequest(srv.API(), http.MethodGet, "/api/group-mappings/"+tt.id, nil, nil)
			if err != nil {
				t.Fatalf("request error: %v", err)
			}

			if resp.StatusCode != tt.wantStatus {
				body, _ := test.ReadBody(resp)
				t.Errorf("status = %d, want %d; body: %s", resp.StatusCode, tt.wantStatus, string(body))
			}
		})
	}
}

func TestAPI_UpdateGroupMapping(t *testing.T) {
	srv := test.NewServer(t)
	admin := test.CreateIdentity(t, srv.DB(), "admin@example.com")
	test.AddUserToOrg(t, srv.DB(), admin.ID, models.InfraAdminRole, &srv.Options().Internal.OrgID)

	// Create a mapping first.
	mappingReq := api.CreateGroupMappingRequest{
		RuleName:          "old-rule",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   "ssh",
		NameTemplate:      "old-host-$1",
	}

	resp, err := test.DoRequest(srv.API(), http.MethodPost, "/api/group-mappings", mappingReq, nil)
	if err != nil {
		t.Fatalf("create request error: %v", err)
	}

	var created api.GroupMapping
	test.ReadJSON(t, resp, &created)

	updateReq := api.UpdateGroupMappingRequest{
		ID:                created.ID,
		RuleName:          "updated-rule",
		SourceGroupRegex:  "^ops-(.*)$",
		DestinationType:   "ssh",
		NameTemplate:      "new-host-$1",
	}

	resp, err = test.DoRequest(srv.API(), http.MethodPut, "/api/group-mappings/"+created.ID.String(), updateReq, nil)
	if err != nil {
		t.Fatalf("update request error: %v", err)
	}

	var updated api.GroupMapping
	test.ReadJSON(t, resp, &updated)

	if updated.RuleName != "updated-rule" {
		t.Errorf("rule_name = %q, want 'updated-rule'", updated.RuleName)
	}
	if updated.SourceGroupRegex != "^ops-(.*)$" {
		t.Errorf("source_group_regex = %q, want '^ops-(.*)$'", updated.SourceGroupRegex)
	}
}

func TestAPI_DeleteGroupMapping(t *testing.T) {
	srv := test.NewServer(t)
	admin := test.CreateIdentity(t, srv.DB(), "admin@example.com")
	test.AddUserToOrg(t, srv.DB(), admin.ID, models.InfraAdminRole, &srv.Options().Internal.OrgID)

	// Create a mapping first.
	mappingReq := api.CreateGroupMappingRequest{
		RuleName:          "delete-me",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   "ssh",
		NameTemplate:      "host-$1",
	}

	resp, err := test.DoRequest(srv.API(), http.MethodPost, "/api/group-mappings", mappingReq, nil)
	if err != nil {
		t.Fatalf("create request error: %v", err)
	}

	var created api.GroupMapping
	test.ReadJSON(t, resp, &created)

	resp, err = test.DoRequest(srv.API(), http.MethodDelete, "/api/group-mappings/"+created.ID.String(), nil, nil)
	if err != nil {
		t.Fatalf("delete request error: %v", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := test.ReadBody(resp)
		t.Errorf("status = %d, want 200/204; body: %s", resp.StatusCode, string(body))
	}

	// Verify it's gone.
	resp, err = test.DoRequest(srv.API(), http.MethodGet, "/api/group-mappings/"+created.ID.String(), nil, nil)
	if err != nil {
		t.Fatalf("get request error: %v", err)
	}

	if resp.StatusCode == http.StatusOK {
		body, _ := test.ReadBody(resp)
		t.Errorf("mapping still exists after delete; body: %s", string(body))
	}
}

func TestAPI_ListGroupMappings(t *testing.T) {
	srv := test.NewServer(t)
	admin := test.CreateIdentity(t, srv.DB(), "admin@example.com")
	test.AddUserToOrg(t, srv.DB(), admin.ID, models.InfraAdminRole, &srv.Options().Internal.OrgID)

	// Create several mappings.
	for i := 0; i < 3; i++ {
		mappingReq := api.CreateGroupMappingRequest{
			RuleName:          "rule-" + string(rune('a'+i)),
			SourceGroupRegex:  "^team-(.*)$",
			DestinationType:   "ssh",
			NameTemplate:      "host-$1",
		}

		resp, err := test.DoRequest(srv.API(), http.MethodPost, "/api/group-mappings", mappingReq, nil)
		if err != nil {
			t.Fatalf("create request error for rule-%s: %v", string(rune('a'+i)), err)
		}

		var created api.GroupMapping
		test.ReadJSON(t, resp, &created)
		_ = created
	}

	resp, err := test.DoRequest(srv.API(), http.MethodGet, "/api/group-mappings?limit=10", nil, nil)
	if err != nil {
		t.Fatalf("list request error: %v", err)
	}

	var listResp api.ListGroupMappingsResponse
	test.ReadJSON(t, resp, &listResp)

	if len(listResp.Result) < 3 {
		t.Errorf("expected at least 3 mappings, got %d", len(listResp.Result))
	}
}

func TestAPI_GroupMappingRequiresAdminAuth(t *testing.T) {
	srv := test.NewServer(t)
	admin := test.CreateIdentity(t, srv.DB(), "admin@example.com")
	test.AddUserToOrg(t, srv.DB(), admin.ID, models.InfraAdminRole, &srv.Options().Internal.OrgID)

	viewer := test.CreateIdentity(t, srv.DB(), "viewer@example.com")
	test.AddUserToOrg(t, srv.DB(), viewer.ID, models.InfraViewRole, &srv.Options().Internal.OrgID)

	// Create a mapping as admin.
	mappingReq := api.CreateGroupMappingRequest{
		RuleName:          "admin-rule",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   "ssh",
		NameTemplate:      "host-$1",
	}

	resp, err := test.DoRequest(srv.API(), http.MethodPost, "/api/group-mappings", mappingReq, nil)
	if err != nil {
		t.Fatalf("create request error: %v", err)
	}

	var created api.GroupMapping
	test.ReadJSON(t, resp, &created)

	// Now try to delete as non-admin.
	resp2 := test.DoRequestAsUser(srv.API(), http.MethodDelete, "/api/group-mappings/"+created.ID.String(), nil, viewer)
	if resp2.StatusCode != http.StatusForbidden {
		body, _ := test.ReadBody(resp2)
		t.Errorf("non-admin delete status = %d, want 403; body: %s", resp2.StatusCode, string(body))
	}

	// Try to list as non-admin.
	resp3 := test.DoRequestAsUser(srv.API(), http.MethodGet, "/api/group-mappings?limit=10", nil, viewer)
	if resp3.StatusCode != http.StatusForbidden {
		body, _ := test.ReadBody(resp3)
		t.Errorf("non-admin list status = %d, want 403; body: %s", resp3.StatusCode, string(body))
	}

	// Verify admin can still see the mapping.
	respAdmin, err := test.DoRequest(srv.API(), http.MethodGet, "/api/group-mappings?limit=10", nil, nil)
	if err != nil {
		t.Fatalf("admin list request error: %v", err)
	}

	var adminListResp api.ListGroupMappingsResponse
	test.ReadJSON(t, respAdmin, &adminListResp)

	if len(adminListResp.Result) < 1 {
		t.Errorf("admin should see at least 1 mapping, got %d", len(adminListResp.Result))
	}
}

// stringPtr helper for tests.
func testStringPtr(s string) *string {
	return &s
}
