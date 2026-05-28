// Tests for mapping rule cache population and grants count.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"time"

	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
)

// TestEvaluateMappingRulesPopulatesCacheForAllRules verifies that EvaluateMappingRules
// creates a cache entry for EVERY rule (even those with no matching groups).
func TestEvaluateMappingRulesPopulatesCacheForAllRules(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create 3 rules: one will match groups, two won't.
	rules := []*models.MappingRule{
		{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
			RuleName:           "matching-rule",
			SourceGroupRegex:   "^team-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		},
		{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
			RuleName:           "no-match-rule-1",
			SourceGroupRegex:   "^admin-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		},
		{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
			RuleName:           "no-match-rule-2",
			SourceGroupRegex:   "^ops-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-$1",
		},
	}

	for _, r := range rules {
		assert.NilError(t, data.CreateMappingRule(tx, r))
	}

	// Create a group that ONLY matches the first rule.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	// Clear cache before evaluation (simulates fresh start).
	MRGrantsCache = sync.Map{}

	assert.NilError(t, EvaluateMappingRules(tx))

	// Verify ALL 3 rules have a cache entry.
	for _, rule := range rules {
		ruleKey := orgID.String() + ":" + rule.ID.String()
		val, ok := MRGrantsCache.Load(ruleKey)
		if !ok {
			t.Errorf("cache miss for rule %q (key=%s) — expected empty array entry", rule.RuleName, ruleKey)
			continue
		}

		grants := val.([]api.MappingRuleGrant)

		// The matching rule should have exactly 1 matched grant.
		if rule.RuleName == "matching-rule" {
			assert.Equal(t, len(grants), 1,
				"expected 1 matched grant for %q, got %d: %+v", rule.RuleName, len(grants), grants)
			assert.Equal(t, grants[0].GroupName, group.Name)
		} else {
			// Non-matching rules should have an EMPTY grants list (not missing key).
			assert.Equal(t, len(grants), 0,
				"expected empty grants for %q (no matching groups), got: %+v", rule.RuleName, val)
		}
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestListMappingRulesGrantsCountWithMatchingGroups verifies that ListMappingRules
// returns correct grants counts when rules have matching groups with auto-grants.
func TestListMappingRulesGrantsCountWithMatchingGroups(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create 2 rules.
	rules := []*models.MappingRule{
		{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
			RuleName:           "ssh-team-rule",
			SourceGroupRegex:   "^team-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-host-$1",
		},
		{
			Model:              models.Model{},
			OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
			RuleName:           "ssh-ops-rule",
			SourceGroupRegex:   "^ops-(.*)$",
			DestinationType:    models.DestinationTypeSSH,
			NameTemplate:       "ssh-host-$1",
		},
	}

	for _, r := range rules {
		assert.NilError(t, data.CreateMappingRule(tx, r))
	}

	// Create groups that match both rules.
	groups := []models.Group{
		{Name: "team-platform", OrganizationMember: models.OrganizationMember{OrganizationID: orgID}},
		{Name: "ops-general", OrganizationMember: models.OrganizationMember{OrganizationID: orgID}},
	}
	for _, g := range groups {
		assert.NilError(t, data.CreateGroup(tx, &g))
	}

	MRGrantsCache = sync.Map{}
	assert.NilError(t, EvaluateMappingRules(tx))

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// === Hit the HTTP endpoint on THE SAME SERVER (shared DB). ===
	routes := srv.GenerateRoutes()

	resp := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules?limit=10", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	listReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, listReq)

	assert.Equal(t, resp.Code, http.StatusOK, "list status = %d; body: %s", resp.Code, resp.Body.String())

	var listResp struct {
		Count int               `json:"count"`
		Items []api.MappingRule `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	// We have 2 rules. Both should show grants > 0 because both matched groups created auto-grants.
	assert.Equal(t, len(listResp.Items), 2, "expected 2 rules in list response")

	ruleCounts := make(map[string]int)
	for _, item := range listResp.Items {
		ruleCounts[item.RuleName] = len(item.MatchedGrants)
	}

	if count, ok := ruleCounts["ssh-team-rule"]; !ok {
		t.Errorf("missing ssh-team-rule in response")
	} else if count == 0 {
		t.Errorf("expected grants > 0 for ssh-team-rule (team-platform matched), got %d", count)
	}

	if count, ok := ruleCounts["ssh-ops-rule"]; !ok {
		t.Errorf("missing ssh-ops-rule in response")
	} else if count == 0 {
		t.Errorf("expected grants > 0 for ssh-ops-rule (ops-general matched), got %d", count)
	}
}

// TestListMappingRulesMatchedGrantsEmptyForNoMatchingGroups verifies that when rules exist but no groups match,
// matched_grants is an empty array.
func TestListMappingRulesMatchedGrantsEmptyForNoMatchingGroups(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a rule that won't match any groups.
	rule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "no-match-rule",
		SourceGroupRegex:   "^admin-(.*)$", // only matches admin-* groups
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, rule))

	// Create a group that DOESN'T match the rule.
	group := models.Group{
		Name:               "team-platform", // doesn't match ^admin-
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	MRGrantsCache = sync.Map{}
	assert.NilError(t, EvaluateMappingRules(tx))

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	routes := srv.GenerateRoutes()

	resp := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules?limit=10", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	listReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, listReq)

	assert.Equal(t, resp.Code, http.StatusOK, "list status = %d; body: %s", resp.Code, resp.Body.String())

	var listResp struct {
		Count int               `json:"count"`
		Items []api.MappingRule `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	assert.Equal(t, len(listResp.Items), 1)
	if len(listResp.Items[0].MatchedGrants) != 0 {
		t.Errorf("expected empty matched_grants for rule with no matching groups, got %+v", listResp.Items[0].MatchedGrants)
	}
}

// TestGetMappingRuleGrantsDirectly verifies that GetMappingRuleGrants returns correct grant list.
func TestGetMappingRuleGrantsDirectly(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a rule.
	rule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "ssh-team-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-host-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, rule))

	// Create a matching group.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	MRGrantsCache = sync.Map{}
	assert.NilError(t, EvaluateMappingRules(tx))

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	routes := srv.GenerateRoutes()

	resp := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules/"+rule.ID.String()+"/grants", nil)
	getReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	getReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, getReq)

	assert.Equal(t, resp.Code, http.StatusOK, "get-grants status = %d; body: %s", resp.Code, resp.Body.String())

	var grantsResp struct {
		Count int                    `json:"count"`
		Items []api.MappingRuleGrant `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&grantsResp)

	if grantsResp.Count == 0 {
		t.Errorf("expected grants count > 0, got %d", grantsResp.Count)
	} else if len(grantsResp.Items) == 0 {
		t.Errorf("expected grant items but got empty list; count = %d", grantsResp.Count)
	}

	// Verify the grant subject matches the group name.
	if len(grantsResp.Items) > 0 && grantsResp.Items[0].GroupName != "team-platform" {
		t.Errorf("expected grant subject to be 'team-platform', got %q", grantsResp.Items[0].GroupName)
	}

	// Verify the resource matches the template output.
	if len(grantsResp.Items) > 0 && grantsResp.Items[0].Resource != "ssh-host-platform" {
		t.Errorf("expected grant resource to be 'ssh-host-platform', got %q", grantsResp.Items[0].Resource)
	}
}

// TestGetMappingRuleGrantsEmptyForNoMatchingGroups verifies GetMappingRuleGrants returns count=0 (not missing key).
func TestGetMappingRuleGrantsEmptyForNoMatchingGroups(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	rule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "no-match-rule",
		SourceGroupRegex:   "^admin-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, rule))

	MRGrantsCache = sync.Map{}
	assert.NilError(t, EvaluateMappingRules(tx))

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	routes := srv.GenerateRoutes()

	resp := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules/"+rule.ID.String()+"/grants", nil)
	getReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	getReq.Header.Set("Infra-Version", apiVersionLatest)
	routes.ServeHTTP(resp, getReq)

	assert.Equal(t, resp.Code, http.StatusOK, "get-grants status = %d; body: %s", resp.Code, resp.Body.String())

	var grantsResp struct {
		Count int                    `json:"count"`
		Items []api.MappingRuleGrant `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&grantsResp)

	if grantsResp.Count != 0 {
		t.Errorf("expected count = 0 for rule with no matching groups, got %d", grantsResp.Count)
	}
	if len(grantsResp.Items) != 0 {
		t.Errorf("expected empty items list, got %d items: %+v", len(grantsResp.Items), grantsResp.Items)
	}
}

// TestEvaluateMappingRulesAsyncPopulatesCache verifies that the async evaluation path
// (used during startup and CRUD triggers) correctly populates MRGrantsCache.
func TestEvaluateMappingRulesAsyncPopulatesCache(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	rule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "async-test-rule",
		SourceGroupRegex:   "^team-(.*)$",
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, rule))

	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Clear cache and eval status to simulate fresh state.
	MRGrantsCache = sync.Map{}
	evalStatusStore = sync.Map{}

	EvaluateMappingRulesAsync(srv.db, orgID)

	// Wait for async goroutine to complete (up to 2 seconds).
	var status *EvalStatusReport
	for i := 0; i < 40; i++ {
		status = GetEvalStatus(orgID)
		if status != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	assert.Assert(t, status != nil, "expected eval status to be recorded within timeout")
	if !status.Success {
		t.Logf("eval error: %s", status.Error)
	}
	assert.Assert(t, status.Success, "async evaluation should succeed; error: %s", status.Error)

	// Verify the cache has an entry for this specific rule.
	ruleKey := orgID.String() + ":" + rule.ID.String()
	val, ok := MRGrantsCache.Load(ruleKey)
	assert.Assert(t, ok, "expected cache entry for async-evaluated rule %q (key=%s); status success=%v err=%s", rule.RuleName, ruleKey, status.Success, status.Error)

	grants := val.([]api.MappingRuleGrant)
	assert.Equal(t, len(grants), 1, "expected 1 matched grant from async eval, got %d: %+v", len(grants), grants)
	assert.Equal(t, grants[0].GroupName, group.Name)
}

// TestEvaluateMappingRulesPopulatesCacheForInvalidRegexRule verifies that rules with invalid regexes
// still get an empty cache entry (not missing key).
func TestEvaluateMappingRulesPopulatesCacheForInvalidRegexRule(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	orgID := srv.db.DefaultOrg.ID

	rawTx, rawErr := srv.db.Begin(context.Background(), nil)
	assert.NilError(t, rawErr)
	tx := rawTx.WithOrgID(orgID)
	defer func() { _ = tx.Rollback() }()

	// Create a rule with INVALID regex.
	badRule := &models.MappingRule{
		Model:              models.Model{},
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
		RuleName:           "invalid-regex-rule",
		SourceGroupRegex:   "[invalid(", // invalid regex
		DestinationType:    models.DestinationTypeSSH,
		NameTemplate:       "ssh-$1",
	}
	assert.NilError(t, data.CreateMappingRule(tx, badRule))

	// Create a group that would match if the regex were valid.
	group := models.Group{
		Name:               "team-platform",
		OrganizationMember: models.OrganizationMember{OrganizationID: orgID},
	}
	assert.NilError(t, data.CreateGroup(tx, &group))

	MRGrantsCache = sync.Map{}
	assert.NilError(t, EvaluateMappingRules(tx))

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Verify the invalid-regex rule has an empty cache entry (not missing).
	ruleKey := orgID.String() + ":" + badRule.ID.String()
	val, ok := MRGrantsCache.Load(ruleKey)
	if !ok {
		t.Fatalf("cache miss for invalid-regex rule %q (key=%s) — expected empty array entry", badRule.RuleName, ruleKey)
	}

	grants := val.([]api.MappingRuleGrant)
	assert.Equal(t, len(grants), 0, "expected empty grants for invalid-regex rule, got: %+v", grants)
}
