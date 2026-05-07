package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/muesli/termenv"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/logging"
)

var (
	//lint:ignore ST1005, user facing error
	ErrConfigNotFound   = errors.New(`Could not read local credentials. Are you logged in? Use "infra login" to login`)
	ErrUserNotFound     = errors.New(`user not found`)
	ErrGroupNotFound    = errors.New(`group not found`)
	ErrAccessKeyExpired = errors.New(`access key expired`)
	ErrAccessKeyMissing = errors.New(`access key missing`)
)

type LoginError struct {
	Message string
}

func (e *LoginError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Login error: %s.", e.Message)

	hostConfig, err := currentHostConfig()
	if err != nil {
		logging.Debugf("current host config: %v", err)
		return sb.String()
	}

	if hostConfig.isLoggedIn() {
		fmt.Fprintf(&sb, " Your session as %s to %s is still active.", termenv.String(hostConfig.Name).Bold().String(), termenv.String(hostConfig.Host).Bold().String())
	}

	return sb.String()
}

// formatAuthError converts a 401 api.Error into a user-friendly cmd.Error.
// If err is not a 401 api.Error, it is returned unchanged.
func formatAuthError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != 401 {
		return err
	}
	return Error{Message: sessionExpiredMessage(apiErr.Message)}
}

func sessionExpiredMessage(serverMsg string) string {
	switch serverMsg {
	case "access key has expired due to inactivity":
		return "Your Infra session expired due to inactivity.\nRun 'infra login' to continue."
	case "access key has expired":
		return "Your Infra session has expired.\nRun 'infra login' to continue."
	case "session in identity provider expired or revoked":
		return "Your identity provider session has been revoked.\nRun 'infra login' to continue."
	case "session could not be verified with identity provider":
		return "Your session could not be verified with your identity provider.\nRun 'infra login' to continue."
	default:
		return "Your session is no longer valid.\nRun 'infra login' to continue."
	}
}
