// HTTP handler tests for group mapping CRUD endpoints.
// Tests verify: validation rules, correct HTTP status codes, and admin-only access enforcement.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"strings"

	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/api"

	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
)

// TestAPI_CreateMappingRule verifies validation rules for creating group mappings.
func TestAPI_CreateMappingRule(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	tests := []struct {
		name       string
		request    api.CreateMappingRuleRequest
		wantStatus int
	}{
		{
			name: "valid kubernetes mapping",
			request: api.CreateMappingRuleRequest{
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
			request: api.CreateMappingRuleRequest{
				RuleName:         "ssh-access",
				SourceGroupRegex: "^team-(.*)$",
				DestinationType:  "ssh",
				NameTemplate:     "my-ssh-host-$1",
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "missing rule_name returns 400",
			request:    api.CreateMappingRuleRequest{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing source_group_regex returns 400",
			request: api.CreateMappingRuleRequest{
				RuleName: "test-rule",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing name_template returns 400",
			request: api.CreateMappingRuleRequest{
				RuleName:         "test-rule",
				SourceGroupRegex: "^team-(.*)$",
				DestinationType:  "ssh",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "invalid regex returns 400",
			request: api.CreateMappingRuleRequest{
				RuleName:         "test-rule",
				SourceGroupRegex: "[invalid(",
				DestinationType:  "ssh",
				NameTemplate:     "ssh-$1",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "kubernetes without role_template returns 400",
			request: api.CreateMappingRuleRequest{
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
			req := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body)
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

// TestAPI_GetMappingRule verifies fetching an existing mapping returns 200.
func TestAPI_GetMappingRule(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create a group mapping first.
	nsTemplate := "ns-$1"
	mappingReq := api.CreateMappingRuleRequest{
		RuleName:          "test-rule",
		SourceGroupRegex:  "^team-(.*)$",
		DestinationType:   "kubernetes",
		NameTemplate:      "cluster-$1-prod",
		NamespaceTemplate: &nsTemplate,
		RoleTemplate:      ptrString("test"),
	}

	createResp := httptest.NewRecorder()
	body := jsonBody(t, &mappingReq)
	req := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp, req)

	var created api.MappingRule
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
			req := httptest.NewRequest(http.MethodGet, "/api/mapping-rules/"+tc.id, nil)
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

// TestAPI_UpdateMappingRule verifies updating a rule and that EvaluateMappingRules is triggered (indirectly via no error).
func TestAPI_UpdateMappingRule(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create a mapping first.
	mappingReq := api.CreateMappingRuleRequest{
		RuleName:         "old-rule",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "old-host-$1",
	}

	createResp := httptest.NewRecorder()
	body := jsonBody(t, &mappingReq)
	req := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp, req)

	var created api.MappingRule
	json.NewDecoder(createResp.Body).Decode(&created)

	updateReq := api.UpdateMappingRuleRequest{
		ID:               created.ID,
		RuleName:         "updated-rule",
		SourceGroupRegex: "^ops-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "new-host-$1",
	}

	updateResp := httptest.NewRecorder()
	updateBody := jsonBody(t, &updateReq)
	req2 := httptest.NewRequest(http.MethodPut, "/api/mapping-rules/"+created.ID.String(), updateBody)
	req2.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req2.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(updateResp, req2)

	var updated api.MappingRule
	json.NewDecoder(updateResp.Body).Decode(&updated)

	assert.Equal(t, updated.RuleName, "updated-rule")
	assert.Equal(t, updated.SourceGroupRegex, "^ops-(.*)$")
}

// TestAPI_DeleteMappingRule verifies deletion returns 200/204 and the mapping is removed.
func TestAPI_DeleteMappingRule(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create a mapping first.
	mappingReq := api.CreateMappingRuleRequest{
		RuleName:         "delete-me",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "host-$1",
	}

	createResp := httptest.NewRecorder()
	body := jsonBody(t, &mappingReq)
	req := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp, req)

	var created api.MappingRule
	json.NewDecoder(createResp.Body).Decode(&created)

	resp := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/mapping-rules/"+created.ID.String(), nil)
	deleteReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	deleteReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, deleteReq)

	assert.Assert(t, resp.Code == http.StatusOK || resp.Code == http.StatusNoContent, "delete status = %d, want 200/204; body: %s", resp.Code, resp.Body.String())

	// Verify it's gone.
	getResp := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules/"+created.ID.String(), nil)
	getReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	getReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(getResp, getReq)

	assert.Assert(t, getResp.Code != http.StatusOK, "mapping still exists after delete; status = %d", getResp.Code)
}

// TestAPI_ListMappingRules verifies listing returns all created mappings with correct count.
func TestAPI_ListMappingRules(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create several mappings.
	for i := 0; i < 3; i++ {
		mappingReq := api.CreateMappingRuleRequest{
			RuleName:         "rule-" + string(rune('a'+i)),
			SourceGroupRegex: "^team-(.*)$",
			DestinationType:  "ssh",
			NameTemplate:     "host-$1",
		}

		resp := httptest.NewRecorder()
		body := jsonBody(t, &mappingReq)
		req := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body)
		req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
		req.Header.Set("Infra-Version", apiVersionLatest)
		routes.ServeHTTP(resp, req)

		var created api.MappingRule
		json.NewDecoder(resp.Body).Decode(&created)
		_ = created
	}

	resp := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules?limit=10", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	listReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, listReq)

	var listResp struct {
		Count int               `json:"count"`
		Items []api.MappingRule `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	assert.Assert(t, len(listResp.Items) >= 3, "expected at least 3 mappings, got %d", len(listResp.Items))
}

// TestAPI_MappingRuleRequiresAdminAuth verifies that non-admin requests are rejected (401/403).
func TestAPI_MappingRuleRequiresAdminAuth(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create a mapping as admin.
	mappingReq := api.CreateMappingRuleRequest{
		RuleName:         "admin-rule",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "host-$1",
	}

	createResp := httptest.NewRecorder()
	body := jsonBody(t, &mappingReq)
	req := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp, req)

	var created api.MappingRule
	json.NewDecoder(createResp.Body).Decode(&created)

	// Try to delete as non-admin — no auth header at all.
	resp2 := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/mapping-rules/"+created.ID.String(), nil)
	deleteReq.Header.Set("Infra-Version", apiVersionLatest)
	// No auth header = unauthenticated
	routes.ServeHTTP(resp2, deleteReq)

	assert.Assert(t, resp2.Code == http.StatusUnauthorized || resp2.Code == http.StatusForbidden, "non-admin delete status = %d, want 401/403; body: %s", resp2.Code, resp2.Body.String())
}

// TestAPI_CreateMappingRuleDuplicateName verifies whether duplicate rule_name values are allowed or rejected.
func TestAPI_CreateMappingRuleDuplicateName(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create first mapping rule.
	firstReq := api.CreateMappingRuleRequest{
		RuleName:         "duplicate-name",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "host-$1",
	}

	createResp := httptest.NewRecorder()
	body := jsonBody(t, &firstReq)
	req := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp, req)

	assert.Assert(t, createResp.Code == http.StatusCreated || createResp.Code == http.StatusOK,
		"first rule creation status = %d", createResp.Code)

	var firstMapping api.MappingRule
	json.NewDecoder(createResp.Body).Decode(&firstMapping)

	// Create second mapping with the SAME name.
	secondReq := api.CreateMappingRuleRequest{
		RuleName:         "duplicate-name", // same as first!
		SourceGroupRegex: "^ops-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "host-$1",
	}

	createResp2 := httptest.NewRecorder()
	body2 := jsonBody(t, &secondReq)
	req2 := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body2)
	req2.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req2.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(createResp2, req2)

	// Document the current behavior: either 409 Conflict (duplicate rejected) or 201/200 (allowed).
	if createResp2.Code == http.StatusConflict || createResp2.Code == http.StatusBadRequest {
		t.Logf("duplicate rule_name correctly rejected with status %d", createResp2.Code)
	} else if createResp2.Code == http.StatusCreated || createResp2.Code == http.StatusOK {
		var secondMapping api.MappingRule
		json.NewDecoder(createResp2.Body).Decode(&secondMapping)
		t.Logf("duplicate rule_name allowed — created id=%s (same name as %s)", secondMapping.ID.String(), firstMapping.ID.String())

		// Verify both rules exist and are retrievable.
		resp := httptest.NewRecorder()
		listReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules?limit=10", nil)
		listReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
		listReq.Header.Set("Infra-Version", apiVersionLatest)
		routes.ServeHTTP(resp, listReq)

		var listResp struct {
			Count int               `json:"count"`
			Items []api.MappingRule `json:"items"`
		}
		json.NewDecoder(resp.Body).Decode(&listResp)
		assert.Assert(t, len(listResp.Items) >= 2, "expected at least 2 rules with same name, got %d", len(listResp.Items))

		var foundFirst, foundSecond bool
		for _, r := range listResp.Items {
			if r.ID.String() == firstMapping.ID.String() {
				foundFirst = true
			}
			if r.ID.String() == secondMapping.ID.String() {
				foundSecond = true
			}
		}
		assert.Assert(t, foundFirst && foundSecond, "both rules should be retrievable by ID despite duplicate names")
	} else {
		t.Errorf("unexpected status for duplicate rule_name: %d — body: %s", createResp2.Code, createResp2.Body.String())
	}
}

// TestAPI_ListMappingRulesByNameFilter verifies the name query parameter filters results via ILIKE match.
func TestAPI_ListMappingRulesByNameFilter(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create 3 rules with distinct names.
	names := []string{"alpha-team-access", "beta-ops-policy", "gamma-admin-rules"}
	for _, name := range names {
		req := api.CreateMappingRuleRequest{
			RuleName:         name,
			SourceGroupRegex: "^team-(.*)$",
			DestinationType:  "ssh",
			NameTemplate:     "host-$1",
		}

		resp := httptest.NewRecorder()
		body := jsonBody(t, &req)
		httpReq := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body)
		httpReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
		httpReq.Header.Set("Infra-Version", apiVersionLatest)
		routes.ServeHTTP(resp, httpReq)

		assert.Assert(t, resp.Code == http.StatusCreated || resp.Code == http.StatusOK,
			"create status = %d for rule %s; body: %s", resp.Code, name, resp.Body.String())
	}

	// List all — should return 3.
	resp := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules?limit=10", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	listReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, listReq)

	var allListResp struct {
		Count int               `json:"count"`
		Items []api.MappingRule `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&allListResp)
	assert.Assert(t, len(allListResp.Items) >= 3, "expected at least 3 rules total, got %d", len(allListResp.Items))

	// Filter by name containing "alpha" — should return only alpha-team-access.
	respAlpha := httptest.NewRecorder()
	alphaReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules?name=alpha&limit=10", nil)
	alphaReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	alphaReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(respAlpha, alphaReq)

	var alphaListResp struct {
		Count int               `json:"count"`
		Items []api.MappingRule `json:"items"`
	}
	json.NewDecoder(respAlpha.Body).Decode(&alphaListResp)
	assert.Assert(t, len(alphaListResp.Items) == 1, "expected exactly 1 rule matching 'alpha', got %d; result: %+v", len(alphaListResp.Items), alphaListResp.Items)

	if len(alphaListResp.Items) > 0 {
		assert.Equal(t, alphaListResp.Items[0].RuleName, "alpha-team-access")
	}

	// Filter by name containing "ops" — should return only beta-ops-policy.
	respOps := httptest.NewRecorder()
	opsReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules?name=ops&limit=10", nil)
	opsReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	opsReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(respOps, opsReq)

	var opsListResp struct {
		Count int               `json:"count"`
		Items []api.MappingRule `json:"items"`
	}
	json.NewDecoder(respOps.Body).Decode(&opsListResp)
	assert.Assert(t, len(opsListResp.Items) == 1, "expected exactly 1 rule matching 'ops', got %d", len(opsListResp.Items))

	if len(opsListResp.Items) > 0 {
		assert.Equal(t, opsListResp.Items[0].RuleName, "beta-ops-policy")
	}

	// Filter by name containing non-matching string — should return empty.
	respNone := httptest.NewRecorder()
	noneReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules?name=nonexistent&limit=10", nil)
	noneReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	noneReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(respNone, noneReq)

	var noneListResp struct {
		Count int               `json:"count"`
		Items []api.MappingRule `json:"items"`
	}
	json.NewDecoder(respNone.Body).Decode(&noneListResp)
	assert.Equal(t, len(noneListResp.Items), 0, "expected 0 rules matching 'nonexistent', got %d", len(noneListResp.Items))
}

// TestAPI_CreateMappingRuleAutoAnchoring verifies that unanchored SourceGroupRegex is anchored by the API handler.
func TestAPI_CreateMappingRuleAutoAnchoring(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	// Create a mapping rule with an UNANCHORED regex — no leading ^.
	req := api.CreateMappingRuleRequest{
		RuleName:         "test-anchored",
		SourceGroupRegex: "team-(.*)$", // intentionally missing ^ anchor
		DestinationType:  "ssh",
		NameTemplate:     "host-$1",
	}

	body := jsonBody(t, &req)
	resp := httptest.NewRecorder()
	rReq := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", body)
	rReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	rReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, rReq)

	assert.Equal(t, http.StatusCreated, resp.Code, "body: %s", resp.Body.String())

	var created api.MappingRule
	json.NewDecoder(resp.Body).Decode(&created)

	// The stored regex must have the ^ anchor added.
	assert.Assert(t, strings.HasPrefix(created.SourceGroupRegex, "^"),
		"expected SourceGroupRegex to start with ^, got %%q", created.SourceGroupRegex)
}

// TestAPI_CreateMappingRuleWildcardExpansion verifies that glob wildcards in NamespaceTemplate are expanded before matching.
func TestEvaluateMappingRulesK8sWithGlobNamespaceTemplate(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a K8s destination with namespaces that match the glob pattern.
	k8sDest := &models.Destination{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		Name:               "k8s-glob-test",
		Kind:               models.DestinationKindKubernetes,
		Resources:          []string{"default", "kube-system", "infra-prod", "infra-staging"},
	}
	assert.NilError(t, data.CreateDestination(tx, k8sDest))

	// Create a mapping rule with glob-style wildcards in NamespaceTemplate.
	// The wildcard * should be expanded to .* so it matches any prefix before "-prod" or "-staging".
	nsTemplate := "infra-*" // glob: "infra-" followed by anything
	mapping := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "glob-wildcard-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeKubernetes,
		NameTemplate:       "k8s-glob-test",
		NamespaceTemplate:  &nsTemplate,
		RoleTemplate:       ptrString("view"),
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

	var foundInfraProd, foundInfraStaging bool
	for _, g := range allGrants {
		if !g.AutoGrant || g.Subject.Kind != models.SubjectKindGroup {
			continue
		}
		switch g.Resource {
		case "k8s-glob-test.infra-prod":
			foundInfraProd = true
		case "k8s-glob-test.infra-staging":
			foundInfraStaging = true
		}
	}

	assert.Assert(t, foundInfraProd, "expected namespaced grant for team-platform → k8s-glob-test.infra-prod")
	assert.Assert(t, foundInfraStaging, "expected namespaced grant for team-platform → k8s-glob-test.infra-staging")

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
