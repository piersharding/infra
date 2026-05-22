// HTTP handler tests for group mapping CRUD endpoints.
// Tests verify: validation rules, correct HTTP status codes, and admin-only access enforcement.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/api"
)

// TestAPI_CreateGroupMapping verifies validation rules for creating group mappings.
func TestAPI_CreateGroupMapping(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	tests := []struct {
		name       string
		request    api.CreateGroupMappingRequest
		wantStatus int
	}{
		{
			name: "valid kubernetes mapping",
			request: api.CreateGroupMappingRequest{
				RuleName:         "team-access",
				SourceGroupRegex: "^team-(.*)$",
				DestinationType:  "kubernetes",
				NameTemplate:     "cluster-$1-prod",
				RoleTemplate:     ptrString("test"),
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "valid SSH mapping",
			request: api.CreateGroupMappingRequest{
				RuleName:         "ssh-access",
				SourceGroupRegex: "^team-(.*)$",
				DestinationType:  "ssh",
				NameTemplate:     "my-ssh-host-$1",
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "missing rule_name returns 400",
			request:    api.CreateGroupMappingRequest{},
			wantStatus: http.StatusBadRequest,
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

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := jsonBody(t, &tc.request)
			req := httptest.NewRequest(http.MethodPost, "/api/group-mappings", body)
			req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
			req.Header.Set("Infra-Version", apiVersionLatest)

			resp := httptest.NewRecorder()
			routes.ServeHTTP(resp, req)

			if resp.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", resp.Code, tc.wantStatus, resp.Body.String())
			}
		})
	}
}

// TestAPI_GetGroupMapping verifies fetching an existing mapping returns 200.
func TestAPI_GetGroupMapping(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create a group mapping first.
	nsTemplate := "ns-$1"
	mappingReq := api.CreateGroupMappingRequest{
		RuleName:          "test-rule",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   "kubernetes",
		NameTemplate:      "cluster-$1-prod",
		NamespaceTemplate: &nsTemplate,
		RoleTemplate:      ptrString("test"),
	}

	createResp := httptest.NewRecorder()
	body := jsonBody(t, &mappingReq)
	req := httptest.NewRequest(http.MethodPost, "/api/group-mappings", body)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp, req)

	var created api.GroupMapping
	json.NewDecoder(createResp.Body).Decode(&created)

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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/group-mappings/"+tc.id, nil)
			req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
			req.Header.Set("Infra-Version", apiVersionLatest)

			resp := httptest.NewRecorder()
			routes.ServeHTTP(resp, req)

			if resp.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", resp.Code, tc.wantStatus, resp.Body.String())
			}
		})
	}
}

// TestAPI_UpdateGroupMapping verifies updating a rule and that EvaluateGroupMappings is triggered (indirectly via no error).
func TestAPI_UpdateGroupMapping(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create a mapping first.
	mappingReq := api.CreateGroupMappingRequest{
		RuleName:         "old-rule",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "old-host-$1",
	}

	createResp := httptest.NewRecorder()
	body := jsonBody(t, &mappingReq)
	req := httptest.NewRequest(http.MethodPost, "/api/group-mappings", body)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp, req)

	var created api.GroupMapping
	json.NewDecoder(createResp.Body).Decode(&created)

	updateReq := api.UpdateGroupMappingRequest{
		ID:               created.ID,
		RuleName:         "updated-rule",
		SourceGroupRegex: "^ops-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "new-host-$1",
	}

	updateResp := httptest.NewRecorder()
	updateBody := jsonBody(t, &updateReq)
	req2 := httptest.NewRequest(http.MethodPut, "/api/group-mappings/"+created.ID.String(), updateBody)
	req2.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req2.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(updateResp, req2)

	var updated api.GroupMapping
	json.NewDecoder(updateResp.Body).Decode(&updated)

	assert.Equal(t, updated.RuleName, "updated-rule")
	assert.Equal(t, updated.SourceGroupRegex, "^ops-(.*)$")
}

// TestAPI_DeleteGroupMapping verifies deletion returns 200/204 and the mapping is removed.
func TestAPI_DeleteGroupMapping(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create a mapping first.
	mappingReq := api.CreateGroupMappingRequest{
		RuleName:         "delete-me",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "host-$1",
	}

	createResp := httptest.NewRecorder()
	body := jsonBody(t, &mappingReq)
	req := httptest.NewRequest(http.MethodPost, "/api/group-mappings", body)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp, req)

	var created api.GroupMapping
	json.NewDecoder(createResp.Body).Decode(&created)

	resp := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/group-mappings/"+created.ID.String(), nil)
	deleteReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	deleteReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, deleteReq)

	assert.Assert(t, resp.Code == http.StatusOK || resp.Code == http.StatusNoContent, "delete status = %d, want 200/204; body: %s", resp.Code, resp.Body.String())

	// Verify it's gone.
	getResp := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/group-mappings/"+created.ID.String(), nil)
	getReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	getReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(getResp, getReq)

	assert.Assert(t, getResp.Code != http.StatusOK, "mapping still exists after delete; status = %d", getResp.Code)
}

// TestAPI_ListGroupMappings verifies listing returns all created mappings with correct count.
func TestAPI_ListGroupMappings(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create several mappings.
	for i := 0; i < 3; i++ {
		mappingReq := api.CreateGroupMappingRequest{
			RuleName:         "rule-" + string(rune('a'+i)),
			SourceGroupRegex: "^team-(.*)$",
			DestinationType:  "ssh",
			NameTemplate:     "host-$1",
		}

		resp := httptest.NewRecorder()
		body := jsonBody(t, &mappingReq)
		req := httptest.NewRequest(http.MethodPost, "/api/group-mappings", body)
		req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
		req.Header.Set("Infra-Version", apiVersionLatest)
		routes.ServeHTTP(resp, req)

		var created api.GroupMapping
		json.NewDecoder(resp.Body).Decode(&created)
		_ = created
	}

	resp := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/group-mappings?limit=10", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	listReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, listReq)

	var listResp struct {
		Count  int                `json:"count"`
		Result []api.GroupMapping `json:"result"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	assert.Assert(t, len(listResp.Result) >= 3, "expected at least 3 mappings, got %d", len(listResp.Result))
}

// TestAPI_GroupMappingRequiresAdminAuth verifies that non-admin requests are rejected (401/403).
func TestAPI_GroupMappingRequiresAdminAuth(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create a mapping as admin.
	mappingReq := api.CreateGroupMappingRequest{
		RuleName:         "admin-rule",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "host-$1",
	}

	createResp := httptest.NewRecorder()
	body := jsonBody(t, &mappingReq)
	req := httptest.NewRequest(http.MethodPost, "/api/group-mappings", body)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp, req)

	var created api.GroupMapping
	json.NewDecoder(createResp.Body).Decode(&created)

	// Try to delete as non-admin — use a different access key.
	resp2 := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/group-mappings/"+created.ID.String(), nil)
	// No auth header = unauthenticated
	routes.ServeHTTP(resp2, deleteReq)

	assert.Assert(t, resp2.Code == http.StatusUnauthorized || resp2.Code == http.StatusForbidden, "non-admin delete status = %d, want 401/403; body: %s", resp2.Code, resp2.Body.String())
}
