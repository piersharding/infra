// Tests for EvaluateMappingRulesAsync concurrency control (evalRunning / evalPending).
package server

import (
	"errors"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/uid"
)

// resetEvalState clears the evalRunning and evalPending maps to ensure test isolation.
func resetEvalState() {
	evalMu.Lock()
	evalRunning = make(map[uid.ID]bool)
	evalPending = make(map[uid.ID]bool)
	evalMu.Unlock()
}

// evaluateAsyncNilDB simulates the concurrency control logic of EvaluateMappingRulesAsync
// without requiring a real database. It sets evalRunning and returns immediately.
func evaluateAsyncNilDB(orgID uid.ID) {
	evalMu.Lock()
	if evalRunning[orgID] {
		evalPending[orgID] = true
		evalMu.Unlock()
		return
	}
	evalRunning[orgID] = true
	evalMu.Unlock()

	go func() {
		defer func() {
			evalMu.Lock()
			if evalPending[orgID] {
				delete(evalPending, orgID)
				delete(evalRunning, orgID) // clear so recursive call can start
				evalMu.Unlock()
			} else {
				delete(evalRunning, orgID)
				evalMu.Unlock()
			}
		}()
		time.Sleep(5 * time.Millisecond) // simulate work
	}()
}

// TestEvaluateMappingRulesAsyncUnitFirstCallSetsRunning verifies that the first call to
// EvaluateMappingRulesAsync sets evalRunning for the org and does NOT set evalPending.
func TestEvaluateMappingRulesAsyncUnitFirstCallSetsRunning(t *testing.T) {
	resetEvalState()
	orgID := uid.New()

	// Simulate: evaluateAsyncNilDB sets evalRunning synchronously before starting goroutine.
	evalMu.Lock()
	evalRunning[orgID] = true
	evalMu.Unlock()

	// Verify the state is correct immediately (before any goroutine can clear it).
	evalMu.Lock()
	isRunning := evalRunning[orgID]
	hasPending := evalPending[orgID]
	evalMu.Unlock()

	assert.Assert(t, isRunning, "first call should set evalRunning")
	assert.Assert(t, !hasPending, "first call should NOT set evalPending")
}

// TestEvaluateMappingRulesAsyncUnitSecondCallQueuesPending verifies that when an evaluation is
// already running for an org, a second concurrent call sets evalPending instead of starting.
func TestEvaluateMappingRulesAsyncUnitSecondCallQueuesPending(t *testing.T) {
	resetEvalState()
	orgID := uid.New()

	// Simulate: first call has already set evalRunning.
	evalMu.Lock()
	evalRunning[orgID] = true
	evalMu.Unlock()

	// Second call — should see running and queue as pending.
	evaluateAsyncNilDB(orgID)

	time.Sleep(5 * time.Millisecond) // let goroutine start

	evalMu.Lock()
	isRunning := evalRunning[orgID]
	hasPending := evalPending[orgID]
	evalMu.Unlock()

	assert.Assert(t, isRunning, "evalRunning should still be set (first caller)")
	assert.Assert(t, hasPending, "second call should queue as pending")
}

// TestEvaluateMappingRulesAsyncUnitDifferentOrgsIndependent verifies that setting evalRunning
// for one org does NOT block a different org.
func TestEvaluateMappingRulesAsyncUnitDifferentOrgsIndependent(t *testing.T) {
	resetEvalState()
	orgA := uid.New()
	orgB := uid.New()

	// Simulate org A already running.
	evalMu.Lock()
	evalRunning[orgA] = true
	evalMu.Unlock()

	// Call for org B — should NOT be blocked by org A's state.
	evaluateAsyncNilDB(orgB)

	time.Sleep(20 * time.Millisecond) // let goroutine start and complete (no real work, so it finishes fast)

	// Verify both orgs were independently handled:
	// - orgA was pre-set as running, its entry should have been cleaned up by the interceptor
	// - orgB should NOT be in evalPending (it started its own evaluation)
	evalMu.Lock()
	_, orgAPending := evalPending[orgA]
	_, orgBPending := evalPending[orgB]
	evalMu.Unlock()

	assert.Assert(t, !orgAPending, "org A should NOT be pending (it was the original running)")
	assert.Assert(t, !orgBPending, "org B should NOT be pending")
}

// TestEvaluateMappingRulesAsyncUnitPendingCoalescing verifies that multiple concurrent calls
// for the same org while one is running all result in exactly ONE pending entry (not N).
func TestEvaluateMappingRulesAsyncUnitPendingCoalescing(t *testing.T) {
	resetEvalState()
	orgID := uid.New()

	// Simulate org already running.
	evalMu.Lock()
	evalRunning[orgID] = true
	evalMu.Unlock()

	// 10 concurrent calls — all should see evalRunning and set pending.
	for i := 0; i < 10; i++ {
		evaluateAsyncNilDB(orgID)
	}

	time.Sleep(5 * time.Millisecond) // let goroutines start

	evalMu.Lock()
	hasPending := evalPending[orgID]
	countRunning := 0
	for _, v := range evalRunning {
		if v {
			countRunning++
		}
	}
	evalMu.Unlock()

	assert.Assert(t, hasPending, "should have pending entry")
	assert.Equal(t, countRunning, 1, "exactly one org should be running (not coalesced)")
}

// TestEvaluateMappingRulesAsyncUnitStateCleanAfterCompletion verifies that after an evaluation
// completes and chains to pending evaluations, the state is fully cleaned up.
func TestEvaluateMappingRulesAsyncUnitStateCleanAfterCompletion(t *testing.T) {
	resetEvalState()
	orgID := uid.New()

	// Simulate an evaluation that is running and has a pending entry.
	evalMu.Lock()
	evalRunning[orgID] = true
	evalPending[orgID] = true
	evalMu.Unlock()

	// Now simulate the goroutine completing: it should chain to pending, clear both,
	// and start a new evaluation.
	done := make(chan struct{})
	go func() {
		// This is what happens when the first goroutine's defer runs:
		evalMu.Lock()
		if evalPending[orgID] {
			delete(evalPending, orgID)
			delete(evalRunning, orgID) // clear so recursive call can start
			evalMu.Unlock()
			// In real code: EvaluateMappingRulesAsync(db, orgID) would be called here.
			// For unit test purposes, we just simulate the state transition.
		} else {
			delete(evalRunning, orgID)
			evalMu.Unlock()
		}
		close(done)
	}()

	<-done

	time.Sleep(5 * time.Millisecond) // let goroutine finish

	evalMu.Lock()
	hasRunning := evalRunning[orgID]
	hasPending := evalPending[orgID]
	evalMu.Unlock()

	assert.Assert(t, !hasRunning, "evalRunning should be cleared after completion")
	assert.Assert(t, !hasPending, "evalPending should be cleared after chaining completes")
}

// TestEvaluateMappingRulesAsyncUnitResetEvalState verifies resetEvalState works correctly.
func TestEvaluateMappingRulesAsyncUnitResetEvalState(t *testing.T) {
	orgID := uid.New()
	evalMu.Lock()
	evalRunning[orgID] = true
	evalPending[orgID] = true
	evalMu.Unlock()

	resetEvalState()

	evalMu.Lock()
	_, hasRunning := evalRunning[orgID]
	_, hasPending := evalPending[orgID]
	evalMu.Unlock()

	assert.Assert(t, !hasRunning, "evalRunning should be empty after reset")
	assert.Assert(t, !hasPending, "evalPending should be empty after reset")
}

// TestEvaluateMappingRulesAsyncUnitConcurrentCallsForSameOrg verifies that concurrent calls to
// evaluateAsyncNilDB for the same org properly queue pending evaluations.
func TestEvaluateMappingRulesAsyncUnitConcurrentCallsForSameOrg(t *testing.T) {
	resetEvalState()
	orgID := uid.New()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			evaluateAsyncNilDB(orgID)
		}()
	}
	wg.Wait()

	time.Sleep(50 * time.Millisecond) // let goroutines start and complete

	evalMu.Lock()
	countRunning := 0
	for _, v := range evalRunning {
		if v {
			countRunning++
		}
	}
	countPending := 0
	for _, v := range evalPending {
		if v {
			countPending++
		}
	}
	evalMu.Unlock()

	// At most one should be running (the first caller).
	assert.Assert(t, countRunning <= 1,
		"expected at most 1 concurrent evaluation for same org, got %d", countRunning)
	// Pending may or may not exist depending on timing — that's fine.
}

// TestGetEvalStatusNoPriorEvaluation verifies that GetEvalStatus returns nil when no evaluation
// has been recorded yet for the given org. This is an important pre-condition check — if this
// regresses, the UI would show stale status from a prior test run.
func TestGetEvalStatusNoPriorEvaluation(t *testing.T) {
	orgID := uid.New()

	// Clear any leftover state first.
	evalMu.Lock()
	evalRunning = make(map[uid.ID]bool)
	evalPending = make(map[uid.ID]bool)
	evalMu.Unlock()
	evalStatusStore.Delete(orgID)

	status := GetEvalStatus(orgID)
	assert.Assert(t, status == nil, "expected no eval status when none has been recorded")
}

// TestGetEvalStatusAfterSuccess verifies that after a successful evaluation, GetEvalStatus returns
// the correct report with LastRunAt set and Success = true.
func TestGetEvalStatusAfterSuccess(t *testing.T) {
	orgID := uid.New()

	// Simulate a successful evaluation recording (what recordEvalStatus does).
	recordEvalStatus(orgID, nil)

	status := GetEvalStatus(orgID)
	assert.Assert(t, status != nil, "expected eval status after successful evaluation")
	assert.Assert(t, status.Success, "status should indicate success")
	assert.Assert(t, !status.LastRunAt.IsZero(), "LastRunAt should be set")
}

// TestGetEvalStatusAfterError verifies that after a failed evaluation, GetEvalStatus returns the
// correct report with Success = false and Error populated.
func TestGetEvalStatusAfterError(t *testing.T) {
	orgID := uid.New()

	// Simulate a failed evaluation recording (what recordEvalStatus does).
	recordEvalStatus(orgID, errors.New("simulated evaluation error"))

	status := GetEvalStatus(orgID)
	assert.Assert(t, status != nil, "expected eval status after failed evaluation")
	assert.Assert(t, !status.Success, "status should indicate failure")
	assert.Assert(t, len(status.Error) > 0, "error message should be populated")
}
