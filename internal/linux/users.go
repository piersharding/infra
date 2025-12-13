package linux

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"
	"time"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/logging"
)

type LocalUser struct {
	Username string
	UID      string
	GID      string
	Info     []string
	HomeDir  string
}

const sentinelManagedByInfra = "managed by infra"

var ValidUsernameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,32}$`)

func validateUsername(username string) error {
	if !ValidUsernameRegex.MatchString(username) {
		return fmt.Errorf("invalid username format: must be 1-32 characters and contain only letters, numbers, dots, underscores, or hyphens")
	}
	return nil
}

func sanitizeUsernameForLogging(username string) string {
	if len(username) <= 2 {
		return "***"
	}
	return username[:1] + "***" + username[len(username)-1:]
}

func (u LocalUser) IsManagedByInfra() bool {
	return len(u.Info) > 1 && u.Info[1] == sentinelManagedByInfra
}

// ReadLocalUsers reads a file in /etc/passwd format and returns the list of
// users in that file.
func ReadLocalUsers(filename string) ([]LocalUser, error) {
	fh, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fh.Close() // read-only file, safe to ignore errors
	scan := bufio.NewScanner(fh)

	var result []LocalUser
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			return nil, fmt.Errorf("invalid line contains less than 7 fields")
		}
		result = append(result, LocalUser{
			Username: fields[0],
			// field 1 is not used
			UID:     fields[2],
			GID:     fields[3],
			Info:    strings.FieldsFunc(fields[4], isRuneComma),
			HomeDir: fields[5],
			// field 6 is login shell
		})
	}
	return result, scan.Err()
}

func isRuneComma(r rune) bool {
	return r == ','
}

func AddUser(apiUser *api.User, group string) error {
	if err := validateUsername(apiUser.SSHLoginName); err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	// Try to use native Go user package first
	if _, err := user.Lookup(apiUser.SSHLoginName); err == nil {
		return fmt.Errorf("user %s already exists", apiUser.SSHLoginName)
	}

	// Fallback to useradd command with validated input
	args := []string{
		"--comment", fmt.Sprintf("%v,%v", apiUser.ID, sentinelManagedByInfra),
		"-m", "-p", "*", "-g", group, apiUser.SSHLoginName,
	}
	cmd := exec.Command("useradd", args...)
	cmd.Stdout = logging.L
	cmd.Stderr = logging.L

	logging.L.Info().
		Str("operation", "add_user").
		Str("username", sanitizeUsernameForLogging(apiUser.SSHLoginName)).
		Str("group", group).
		Time("timestamp", time.Now()).
		Msg("user_add_attempt")

	err := cmd.Run()
	if err != nil {
		logging.L.Error().
			Str("operation", "add_user").
			Str("username", sanitizeUsernameForLogging(apiUser.SSHLoginName)).
			Str("group", group).
			Err(err).
			Msg("user_add_failed")
		return fmt.Errorf("failed to add user: %w", err)
	}

	logging.L.Info().
		Str("operation", "add_user").
		Str("username", sanitizeUsernameForLogging(apiUser.SSHLoginName)).
		Str("group", group).
		Time("timestamp", time.Now()).
		Msg("user_added_successfully")

	return nil
}

func KillUserProcesses(localUser LocalUser) error {
	if err := validateUsername(localUser.Username); err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	// Try to use native Go process management first
	// Note: This is a simplified implementation - full implementation would require
	// more complex process enumeration and signaling

	// Fallback to pkill command with validated input
	//nolint:gosec
	cmd := exec.Command("pkill", "--signal", "KILL", "--uid", localUser.Username)
	cmd.Stdout = logging.L
	cmd.Stderr = logging.L

	logging.L.Info().
		Str("operation", "kill_user_processes").
		Str("username", sanitizeUsernameForLogging(localUser.Username)).
		Time("timestamp", time.Now()).
		Msg("process_kill_attempt")

	err := cmd.Run()

	var exitError *exec.ExitError
	switch {
	// if no processes are running, pkill exits with 1
	case errors.As(err, &exitError) && exitError.ExitCode() == 1:
		logging.L.Info().
			Str("operation", "kill_user_processes").
			Str("username", sanitizeUsernameForLogging(localUser.Username)).
			Msg("no_processes_running")
		return nil
	case err != nil:
		logging.L.Error().
			Str("operation", "kill_user_processes").
			Str("username", sanitizeUsernameForLogging(localUser.Username)).
			Err(err).
			Msg("process_kill_failed")
		return fmt.Errorf("kill processes: %w", err)
	}

	logging.L.Info().
		Str("operation", "kill_user_processes").
		Str("username", sanitizeUsernameForLogging(localUser.Username)).
		Time("timestamp", time.Now()).
		Msg("processes_killed_successfully")

	return nil
}

func RemoveUser(localUser LocalUser) error {
	if err := validateUsername(localUser.Username); err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	// Try to use native Go user package first
	if _, err := user.Lookup(localUser.Username); err != nil {
		if errors.Is(err, user.UnknownUserError(localUser.Username)) {
			logging.L.Info().
				Str("operation", "remove_user").
				Str("username", sanitizeUsernameForLogging(localUser.Username)).
				Msg("user_not_found")
			return nil // User doesn't exist, nothing to do
		}
		return fmt.Errorf("failed to lookup user: %w", err)
	}

	// Fallback to userdel command with validated input
	//nolint:gosec
	cmd := exec.Command("userdel", "--remove", localUser.Username)
	cmd.Stdout = logging.L
	cmd.Stderr = logging.L

	logging.L.Info().
		Str("operation", "remove_user").
		Str("username", sanitizeUsernameForLogging(localUser.Username)).
		Time("timestamp", time.Now()).
		Msg("user_remove_attempt")

	if err := cmd.Run(); err != nil {
		logging.L.Error().
			Str("operation", "remove_user").
			Str("username", sanitizeUsernameForLogging(localUser.Username)).
			Err(err).
			Msg("user_remove_failed")
		return fmt.Errorf("userdel: %w", err)
	}

	logging.L.Info().
		Str("operation", "remove_user").
		Str("username", sanitizeUsernameForLogging(localUser.Username)).
		Time("timestamp", time.Now()).
		Msg("user_removed_successfully")

	return nil
}
