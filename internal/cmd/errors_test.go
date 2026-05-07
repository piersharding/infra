package cmd

import (
	"errors"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/api"
)

func TestFormatAuthError(t *testing.T) {
	tests := []struct {
		name        string
		input       error
		wantMessage string
		wantWrapped bool // true if result should be cmd.Error
	}{
		{
			name:        "nil error unchanged",
			input:       nil,
			wantWrapped: false,
		},
		{
			name:        "non-api error unchanged",
			input:       errors.New("some other error"),
			wantMessage: "some other error",
			wantWrapped: false,
		},
		{
			name:        "non-401 api error unchanged",
			input:       api.Error{Code: 403, Message: "forbidden"},
			wantMessage: "forbidden",
			wantWrapped: false,
		},
		{
			name:        "inactivity timeout",
			input:       api.Error{Code: 401, Message: "access key has expired due to inactivity"},
			wantMessage: "Your Infra session expired due to inactivity.\nRun 'infra login' to continue.",
			wantWrapped: true,
		},
		{
			name:        "hard expiry",
			input:       api.Error{Code: 401, Message: "access key has expired"},
			wantMessage: "Your Infra session has expired.\nRun 'infra login' to continue.",
			wantWrapped: true,
		},
		{
			name:        "idp revocation",
			input:       api.Error{Code: 401, Message: "session in identity provider expired or revoked"},
			wantMessage: "Your identity provider session has been revoked.\nRun 'infra login' to continue.",
			wantWrapped: true,
		},
		{
			name:        "idp backstop",
			input:       api.Error{Code: 401, Message: "session could not be verified with identity provider"},
			wantMessage: "Your session could not be verified with your identity provider.\nRun 'infra login' to continue.",
			wantWrapped: true,
		},
		{
			name:        "unknown 401",
			input:       api.Error{Code: 401, Message: "something unexpected"},
			wantMessage: "Your session is no longer valid.\nRun 'infra login' to continue.",
			wantWrapped: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := formatAuthError(tc.input)
			if tc.input == nil {
				assert.Assert(t, result == nil)
				return
			}
			assert.Assert(t, result != nil)
			assert.Equal(t, result.Error(), tc.wantMessage)

			var cmdErr Error
			if tc.wantWrapped {
				assert.Assert(t, errors.As(result, &cmdErr), "expected cmd.Error, got %T", result)
			} else {
				assert.Assert(t, !errors.As(result, &cmdErr), "expected original error type, got cmd.Error")
			}
		})
	}
}
