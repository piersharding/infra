package server

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/infrahq/infra/uid"
)

// ErrSyncBackstop is wrapped inside ErrSyncFailed when the session is destroyed
// because repeated transient IDP errors exceeded the failure backstop threshold.
// Middleware uses this to produce a different user-facing message than for genuine
// IDP revocations.
var ErrSyncBackstop = fmt.Errorf("IDP sync backstop exceeded")

// isIDPRevocationError returns true if the error represents an explicit rejection
// by the identity provider (as opposed to a transient network or server failure).
// Only errors that definitively indicate the user's IDP session is invalid should
// return true; all others are treated as transient and do not immediately destroy
// the Infra session.
func isIDPRevocationError(err error) bool {
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) {
		return false
	}
	switch retrieveErr.ErrorCode {
	case "invalid_grant",
		"token_revoked",
		"account_disabled",
		"user_disabled",
		"access_denied":
		return true
	}
	return false
}

// syncFailureKey uniquely identifies a user+provider combination for failure tracking.
type syncFailureKey struct {
	identityID uid.ID
	providerID uid.ID
}

type syncFailureEntry struct {
	count          int
	firstFailureAt time.Time
}

// syncFailureTracker tracks consecutive transient IDP sync failures per user/provider
// pair in memory. It is used as a backstop to invalidate sessions that cannot be
// verified with the IDP for an extended period.
//
// State resets on server restart; this is intentional — the tracker is a safety net,
// not a hard security control.
type syncFailureTracker struct {
	mu      sync.Mutex
	entries map[syncFailureKey]syncFailureEntry

	maxFailures   int
	failureWindow time.Duration
}

func newSyncFailureTracker(maxFailures int, window time.Duration) *syncFailureTracker {
	// Apply defaults matching the documented Options values.
	if maxFailures <= 0 {
		maxFailures = 3
	}
	if window == 0 {
		window = 24 * time.Hour
	}
	return &syncFailureTracker{
		entries:       make(map[syncFailureKey]syncFailureEntry),
		maxFailures:   maxFailures,
		failureWindow: window,
	}
}

// RecordFailure records a transient sync failure for the given key and returns
// true if the backstop threshold has been exceeded (i.e. the session should be
// invalidated). The window resets if the first failure is older than failureWindow.
func (t *syncFailureTracker) RecordFailure(key syncFailureKey) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry := t.entries[key]

	// Reset the window if the first failure is older than failureWindow.
	if !entry.firstFailureAt.IsZero() && time.Since(entry.firstFailureAt) > t.failureWindow {
		entry = syncFailureEntry{}
	}

	if entry.firstFailureAt.IsZero() {
		entry.firstFailureAt = time.Now()
	}
	entry.count++
	t.entries[key] = entry

	return entry.count >= t.maxFailures
}

// RecordSuccess resets the failure counter for the given key.
func (t *syncFailureTracker) RecordSuccess(key syncFailureKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}
