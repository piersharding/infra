package server

import (
	"fmt"
	"net"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/uid"
)

func TestIsIDPRevocationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "invalid_grant is a revocation",
			err:      &oauth2.RetrieveError{ErrorCode: "invalid_grant"},
			expected: true,
		},
		{
			name:     "token_revoked is a revocation",
			err:      &oauth2.RetrieveError{ErrorCode: "token_revoked"},
			expected: true,
		},
		{
			name:     "account_disabled is a revocation",
			err:      &oauth2.RetrieveError{ErrorCode: "account_disabled"},
			expected: true,
		},
		{
			name:     "user_disabled is a revocation",
			err:      &oauth2.RetrieveError{ErrorCode: "user_disabled"},
			expected: true,
		},
		{
			name:     "access_denied is a revocation",
			err:      &oauth2.RetrieveError{ErrorCode: "access_denied"},
			expected: true,
		},
		{
			name:     "unrecognised OAuth error is not a revocation",
			err:      &oauth2.RetrieveError{ErrorCode: "server_error"},
			expected: false,
		},
		{
			name:     "network timeout is not a revocation",
			err:      &net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")},
			expected: false,
		},
		{
			name:     "plain error is not a revocation",
			err:      fmt.Errorf("some generic error"),
			expected: false,
		},
		{
			name:     "nil is not a revocation",
			err:      nil,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isIDPRevocationError(tc.err)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestSyncFailureTracker_BackstopTriggersAtMaxFailures(t *testing.T) {
	tracker := newSyncFailureTracker(3, 24*time.Hour)
	key := syncFailureKey{identityID: uid.New(), providerID: uid.New()}

	assert.Equal(t, false, tracker.RecordFailure(key), "first failure should not trigger backstop")
	assert.Equal(t, false, tracker.RecordFailure(key), "second failure should not trigger backstop")
	assert.Equal(t, true, tracker.RecordFailure(key), "third failure should trigger backstop")
}

func TestSyncFailureTracker_WindowReset(t *testing.T) {
	tracker := newSyncFailureTracker(3, 1*time.Hour)
	key := syncFailureKey{identityID: uid.New(), providerID: uid.New()}

	// Record 2 failures
	tracker.RecordFailure(key)
	tracker.RecordFailure(key)

	// Wind back firstFailureAt beyond the window
	tracker.mu.Lock()
	entry := tracker.entries[key]
	entry.firstFailureAt = time.Now().Add(-2 * time.Hour)
	tracker.entries[key] = entry
	tracker.mu.Unlock()

	// This failure should reset the window and not trigger the backstop
	assert.Equal(t, false, tracker.RecordFailure(key), "failure after window expiry should reset counter")

	// Should now need maxFailures more to trigger
	assert.Equal(t, false, tracker.RecordFailure(key))
	assert.Equal(t, true, tracker.RecordFailure(key))
}

func TestSyncFailureTracker_RecordSuccess_ResetsCounter(t *testing.T) {
	tracker := newSyncFailureTracker(3, 24*time.Hour)
	key := syncFailureKey{identityID: uid.New(), providerID: uid.New()}

	tracker.RecordFailure(key)
	tracker.RecordFailure(key)
	tracker.RecordSuccess(key)

	// Counter should be reset; needs maxFailures again to trigger
	assert.Equal(t, false, tracker.RecordFailure(key))
	assert.Equal(t, false, tracker.RecordFailure(key))
	assert.Equal(t, true, tracker.RecordFailure(key))
}

func TestSyncFailureTracker_IndependentKeys(t *testing.T) {
	tracker := newSyncFailureTracker(2, 24*time.Hour)
	key1 := syncFailureKey{identityID: uid.New(), providerID: uid.New()}
	key2 := syncFailureKey{identityID: uid.New(), providerID: uid.New()}

	tracker.RecordFailure(key1)
	tracker.RecordFailure(key1) // key1 backstop triggered

	// key2 should be independent
	assert.Equal(t, false, tracker.RecordFailure(key2), "key2 counter should be independent of key1")
}
