package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal"
	"github.com/infrahq/infra/internal/access"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

// TestWrapRoute_EvaluateMappingRulesAfterCommit verifies that when a route has
// evaluateMappingRules=true, the evaluateMappingRulesAsyncFunc is called after
// the transaction commits successfully.
func TestWrapRoute_EvaluateMappingRulesAfterCommit(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	router := gin.New()

	var mu sync.Mutex
	var calledWithOrgID uid.ID
	var callCount int

	origFunc := evaluateMappingRulesAsyncFunc
	t.Cleanup(func() { evaluateMappingRulesAsyncFunc = origFunc })

	evaluateMappingRulesAsyncFunc = func(db *data.DB, orgID uid.ID) {
		mu.Lock()
		calledWithOrgID = orgID
		callCount++
		mu.Unlock()
	}

	r := route[api.EmptyRequest, *api.EmptyResponse]{
		handler: func(rCtx access.RequestContext, request *api.EmptyRequest) (*api.EmptyResponse, error) {
			return &api.EmptyResponse{}, nil
		},
		routeSettings: routeSettings{
			infraVersionHeaderOptional: true,
			authenticationOptional:     true,
			organizationOptional:       false,
			evaluateMappingRules:       true,
		},
	}

	apiObj := &API{server: srv}
	add(apiObj, newRouterGroup(router.Group("/")), "POST", "/do", r)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/do", nil)
	req.Header.Set("Infra-Version", apiVersionLatest)
	router.ServeHTTP(resp, req)

	assert.Equal(t, resp.Code, http.StatusCreated)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, callCount, 1, "evaluateMappingRulesAsyncFunc should be called once")
	assert.Equal(t, calledWithOrgID, srv.db.DefaultOrg.ID, "should be called with the correct org ID")
}

// TestWrapRoute_EvaluateMappingRulesNotCalledOnError verifies that when a handler
// returns an error, evaluateMappingRulesAsyncFunc is NOT called (transaction is rolled back).
func TestWrapRoute_EvaluateMappingRulesNotCalledOnError(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	router := gin.New()

	var mu sync.Mutex
	var callCount int

	origFunc := evaluateMappingRulesAsyncFunc
	t.Cleanup(func() { evaluateMappingRulesAsyncFunc = origFunc })

	evaluateMappingRulesAsyncFunc = func(db *data.DB, orgID uid.ID) {
		mu.Lock()
		callCount++
		mu.Unlock()
	}

	r := route[api.EmptyRequest, *api.EmptyResponse]{
		handler: func(rCtx access.RequestContext, request *api.EmptyRequest) (*api.EmptyResponse, error) {
			return nil, fmt.Errorf("%w: handler failed", internal.ErrBadRequest)
		},
		routeSettings: routeSettings{
			infraVersionHeaderOptional: true,
			authenticationOptional:     true,
			organizationOptional:       false,
			evaluateMappingRules:       true,
		},
	}

	apiObj := &API{server: srv}
	add(apiObj, newRouterGroup(router.Group("/")), "POST", "/do", r)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/do", nil)
	req.Header.Set("Infra-Version", apiVersionLatest)
	router.ServeHTTP(resp, req)

	assert.Equal(t, resp.Code, http.StatusBadRequest)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, callCount, 0, "evaluateMappingRulesAsyncFunc should NOT be called on handler error")
}

// TestWrapRoute_EvaluateMappingRulesNotCalledWhenFlagFalse verifies that when a route
// does NOT have evaluateMappingRules=true, evaluateMappingRulesAsyncFunc is NOT called.
func TestWrapRoute_EvaluateMappingRulesNotCalledWhenFlagFalse(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	router := gin.New()

	var mu sync.Mutex
	var callCount int

	origFunc := evaluateMappingRulesAsyncFunc
	t.Cleanup(func() { evaluateMappingRulesAsyncFunc = origFunc })

	evaluateMappingRulesAsyncFunc = func(db *data.DB, orgID uid.ID) {
		mu.Lock()
		callCount++
		mu.Unlock()
	}

	r := route[api.EmptyRequest, *api.EmptyResponse]{
		handler: func(rCtx access.RequestContext, request *api.EmptyRequest) (*api.EmptyResponse, error) {
			return &api.EmptyResponse{}, nil
		},
		routeSettings: routeSettings{
			infraVersionHeaderOptional: true,
			authenticationOptional:     true,
			organizationOptional:       false,
			evaluateMappingRules:       false,
		},
	}

	apiObj := &API{server: srv}
	add(apiObj, newRouterGroup(router.Group("/")), "POST", "/do", r)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/do", nil)
	req.Header.Set("Infra-Version", apiVersionLatest)
	router.ServeHTTP(resp, req)

	assert.Equal(t, resp.Code, http.StatusCreated)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, callCount, 0, "evaluateMappingRulesAsyncFunc should NOT be called when flag is false")
}

// TestIntegrationMappingRuleEvaluationAfterHTTPCreate verifies that creating a mapping rule
// via the HTTP API triggers async evaluation, and the evaluation sees the newly created rule.
func TestIntegrationMappingRuleEvaluationAfterHTTPCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("too slow for short run")
	}

	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	tx := txnForTestCase(t, srv.db, srv.db.DefaultOrg.ID)

	group := &models.Group{
		Name: "team-platform",
	}
	assert.NilError(t, data.CreateGroup(tx, group))
	assert.NilError(t, tx.Commit())

	reqBody := jsonBody(t, api.CreateMappingRuleRequest{
		RuleName:         "team-access",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  "ssh",
		NameTemplate:     "$1-infra",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/mapping-rules", reqBody)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)

	resp := httptest.NewRecorder()
	routes.ServeHTTP(resp, req)

	assert.Equal(t, resp.Code, http.StatusCreated, resp.Body.String())

	var createdRule api.MappingRule
	assert.NilError(t, json.Unmarshal(resp.Body.Bytes(), &createdRule))

	var evalStatus *api.MappingRuleEvalStatus
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules/eval-status", nil)
		statusReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
		statusReq.Header.Set("Infra-Version", apiVersionLatest)

		statusResp := httptest.NewRecorder()
		routes.ServeHTTP(statusResp, statusReq)

		if statusResp.Code == http.StatusOK {
			assert.NilError(t, json.Unmarshal(statusResp.Body.Bytes(), &evalStatus))
			if time.Time(evalStatus.LastRunAt).IsZero() == false {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	assert.Assert(t, evalStatus != nil, "eval status should be populated")
	assert.Assert(t, time.Time(evalStatus.LastRunAt).IsZero() == false, "LastRunAt should be set")
	assert.Assert(t, evalStatus.Success, "evaluation should succeed, got error: %v", evalStatus.Error)

	verifyTx := txnForTestCase(t, srv.db, srv.db.DefaultOrg.ID)
	grants, err := data.ListGrants(verifyTx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var foundAutoGrant bool
	for _, grant := range grants {
		if grant.AutoGrant && grant.Subject.Kind == models.SubjectKindGroup {
			grp, grpErr := data.GetGroup(verifyTx, data.GetGroupOptions{ByID: grant.Subject.ID})
			if grpErr == nil && grp != nil && grp.Name == "team-platform" {
				foundAutoGrant = true
				assert.Equal(t, "connect", grant.Privilege)
				break
			}
		}
	}

	assert.Assert(t, foundAutoGrant, "expected to find an auto-grant for team-platform group")
}

// TestIntegrationTransactionVisibilityAfterCommit verifies that the async evaluation
// triggered by the post-commit hook can see data created in the HTTP request transaction.
// This is the core test for the visibility fix.
func TestIntegrationTransactionVisibilityAfterCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("too slow for short run")
	}

	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	tx := txnForTestCase(t, srv.db, srv.db.DefaultOrg.ID)

	existingRule := &models.MappingRule{
		RuleName:         "team-access",
		SourceGroupRegex: "^team-(.*)$",
		DestinationType:  models.DestinationTypeSSH,
		NameTemplate:     "$1-infra",
	}
	assert.NilError(t, data.CreateMappingRule(tx, existingRule))
	assert.NilError(t, tx.Commit())

	reqBody := jsonBody(t, api.CreateGroupRequest{
		Name: "team-devops",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/groups", reqBody)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Set("Infra-Version", apiVersionLatest)

	resp := httptest.NewRecorder()
	routes.ServeHTTP(resp, req)

	assert.Equal(t, resp.Code, http.StatusCreated, resp.Body.String())

	var evalStatus *api.MappingRuleEvalStatus
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusReq := httptest.NewRequest(http.MethodGet, "/api/mapping-rules/eval-status", nil)
		statusReq.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))
		statusReq.Header.Set("Infra-Version", apiVersionLatest)

		statusResp := httptest.NewRecorder()
		routes.ServeHTTP(statusResp, statusReq)

		if statusResp.Code == http.StatusOK {
			assert.NilError(t, json.Unmarshal(statusResp.Body.Bytes(), &evalStatus))
			if time.Time(evalStatus.LastRunAt).IsZero() == false {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	assert.Assert(t, evalStatus != nil, "eval status should be populated")
	assert.Assert(t, time.Time(evalStatus.LastRunAt).IsZero() == false, "LastRunAt should be set")
	assert.Assert(t, evalStatus.Success, "evaluation should succeed, got error: %v", evalStatus.Error)

	verifyTx := txnForTestCase(t, srv.db, srv.db.DefaultOrg.ID)
	grants, err := data.ListGrants(verifyTx, data.ListGrantsOptions{})
	assert.NilError(t, err)

	var foundAutoGrant bool
	for _, grant := range grants {
		if grant.AutoGrant && grant.Subject.Kind == models.SubjectKindGroup {
			grp, grpErr := data.GetGroup(verifyTx, data.GetGroupOptions{ByID: grant.Subject.ID})
			if grpErr == nil && grp != nil && grp.Name == "team-devops" {
				foundAutoGrant = true
				assert.Equal(t, "connect", grant.Privilege)
				break
			}
		}
	}

	assert.Assert(t, foundAutoGrant, "expected to find an auto-grant for team-devops group created via HTTP")
}
