package connector

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/cmd/cliopts"
	"github.com/infrahq/infra/internal/linux"
	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/internal/repeat"
	"github.com/infrahq/infra/uid"
)

type SSHOptions struct {
	// Group is the group to assign to all local users created by the infra connector.
	Group string

	// SSHDConfigPath is the path to the sshd_config file that is used by the
	// ssh server that will call infra to authenticate users. Defaults to
	// /etc/ssh/sshd_config.
	SSHDConfigPath string `config:"sshdConfigPath"`
}

func runSSHConnector(ctx context.Context, opts Options) error {
	if err := validateOptionsSSH(opts); err != nil {
		return err
	}

	client := opts.APIClient()

	// TODO: any reason to keep registering in the background?
	destination, err := registerSSHConnector(ctx, client, opts)
	if err != nil {
		return fmt.Errorf("failed to register destination: %w", err)
	}

	con := &connector{
		client:      client,
		destination: destination,
		options:     opts,
	}

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		backOff := &backoff.ExponentialBackOff{
			InitialInterval:     2 * time.Second,
			MaxInterval:         time.Minute,
			RandomizationFactor: 0.2,
			Multiplier:          1.5,
		}
		waiter := repeat.NewWaiter(backOff)
		fn := func(ctx context.Context, grants []api.Grant) error {
			return updateLocalUsers(ctx, client, opts.SSH, grants)
		}
		return syncGrantsToDestination(ctx, con, waiter, fn)
	})

	return group.Wait()
}

// validateOptionsSSH validates that all settings required for the infra
// ssh connector and 'infra sshd auth-keys' have non-zero values.
func validateOptionsSSH(opts Options) error {
	switch {
	case opts.Server.URL.Host == "":
		return fmt.Errorf("missing server.url")
	case opts.Server.AccessKey == "":
		return fmt.Errorf("missing server.accessKey")
	case opts.Name == "":
		return fmt.Errorf("missing name")

	// TODO: we can remove this when we add auto-detect
	case opts.EndpointAddr.Host == "":
		return fmt.Errorf("missing endpointAddr")

	case opts.SSH.Group == "":
		return fmt.Errorf("missing ssh.group")
	case opts.SSH.SSHDConfigPath == "":
		return fmt.Errorf("missing ssh.sshd_config_path")
	}
	return nil
}

func registerSSHConnector(ctx context.Context, client apiClient, opts Options) (*api.Destination, error) {
	config, err := readSSHDConfig(opts.SSH.SSHDConfigPath, "/etc/ssh")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		config = sshdConfig{}
	case err != nil:
		return nil, err
	}

	hostKeys, err := readSSHHostKeys(config.HostKeys, "/etc/ssh")
	if err != nil {
		return nil, err
	}

	// TODO: support looking up IP address using net.InterfaceAddrs

	destination := &api.Destination{
		Name: opts.Name,
		Kind: "ssh",
		Connection: api.DestinationConnection{
			URL: opts.EndpointAddr.String(),
			CA:  api.PEM(hostKeys),
		},
		// TODO: Roles - the groups available on the system,
	}
	err = createOrUpdateDestination(ctx, client, destination)
	if err != nil {
		return nil, err
	}
	return destination, nil
}

// readSSHHostKeys reads the HostKey settings used by the ssh server. If there are no host keys
// set in config,  readSSHHostKeys reads all files that match /etc/ssh/host_*_key.pub.
// readSSHHostKeys does not honor the HostKeyAlgorithms sshd_config setting.
func readSSHHostKeys(hostKeys []string, dir string) (string, error) {
	buf := new(strings.Builder)

	if len(hostKeys) > 0 {
		for _, name := range hostKeys {
			if !filepath.IsAbs(name) {
				name = filepath.Join(dir, name)
			}
			name += ".pub" // the config lists private keys, we want the public one
			if err := readHostKeyFile(name, buf); err != nil {
				return "", err
			}
		}
		return buf.String(), nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read dir: %w", err)
	}

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "ssh_host_") {
			continue
		}
		if !strings.HasSuffix(entry.Name(), "_key.pub") {
			continue
		}

		if err := readHostKeyFile(filepath.Join(dir, entry.Name()), buf); err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

// readHostKeyFile opens a file, parses it to ensure that it's an SSH host key,
// and then writes the key to out. It is important to parse the key to prevent
// accidentally sending a private key to the API.
func readHostKeyFile(filename string, out io.Writer) error {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(raw)
	if err != nil {
		return err
	}
	_, err = out.Write(ssh.MarshalAuthorizedKey(pub))
	return err
}

// etcPasswdFilename is a shim for testing.
var etcPasswdFilename = "/etc/passwd"

// resolveUsersFromGrants resolves all user IDs from a list of grants.
// It handles both per-user grants (where grant.User is set) and group-based grants
// (where grant.Group is set), resolving each group to its member users via the API.
func resolveUsersFromGrants(ctx context.Context, client apiClient, grants []api.Grant) (map[string]api.Grant, error) {
	result := make(map[string]api.Grant, len(grants))

	// Track which groups we've already resolved to avoid repeated API calls.
	resolvedGroups := map[uid.ID][]uid.ID{}

	for _, grant := range grants {
		// Per-user grant — add directly.
		if grant.User != 0 {
			result[grant.User.String()] = grant
			continue
		}

		// Group-based grant — resolve the group members.
		if grant.Group == 0 {
			continue // neither user nor group set, skip
		}

		// Check if we already resolved this group.
		groupUsers, ok := resolvedGroups[grant.Group]
		if !ok {
			var err error
			groupUsers, err = client.GetUsersInGroup(ctx, grant.Group)
			if err != nil {
				return nil, fmt.Errorf("get users in group %s: %w", grant.Group.String(), err)
			}
			resolvedGroups[grant.Group] = groupUsers

			logging.L.Debug().Str("groupID", grant.Group.String()).Int("members", len(groupUsers)).Msg("resolved group to users for grants")
		}

		// Add each group member to the result
		for _, userID := range groupUsers {
			resolvedGrant := grant // shallow copy
			resolvedGrant.User = userID
			result[userID.String()] = resolvedGrant
		}
	}

	return result, nil
}

func updateLocalUsers(ctx context.Context, client apiClient, opts SSHOptions, grants []api.Grant) error {
	byUserID, err := resolveUsersFromGrants(ctx, client, grants)
	if err != nil {
		return fmt.Errorf("resolve users from grants: %w", err)
	}

	localUsers, err := linux.ReadLocalUsers(etcPasswdFilename)
	if err != nil {
		return err
	}

	// Compare that list to the grants to get a list to remove and a list to add
	var toDelete []linux.LocalUser
	for _, user := range localUsers {
		if !user.IsManagedByInfra() {
			continue
		}
		infraUID := user.Info[0]
		if _, ok := byUserID[infraUID]; !ok {
			toDelete = append(toDelete, user)
			continue
		}
		delete(byUserID, infraUID)
	}

	// Update password expiration for all existing managed users (not just newly created ones).
	// This ensures existing users are fixed without manual intervention.
	var errs []error
	if err := updateManagedUsersPasswordExpiration(ctx, localUsers); err != nil {
		errs = append(errs, fmt.Errorf("update password expiration: %w", err))
	}
	// attempt to kill any active sessions first, so that processes have time to
	// exit before we try to remove the user.
	for _, user := range toDelete {
		if err := linux.KillUserProcesses(user); err != nil {
			errs = append(errs, fmt.Errorf("kill user session %v: %w", user.Username, err))
			continue
		}
	}
	// now attempt to remove the user. If this fails it will be attempted again
	for _, user := range toDelete {
		if err := linux.RemoveUser(user); err != nil {
			errs = append(errs, fmt.Errorf("remove user %v: %w", user.Username, err))
			continue
		}
		logging.L.Info().Str("username", user.Username).Msg("removed user")
	}

	for _, grant := range byUserID {
		user, err := client.GetUser(ctx, grant.User)
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}

		if user.SSHLoginName == "" {
			logging.L.Error().Str("user", user.Name).Msg("missing SSHLoginName")
			continue
		}

		if err := linux.AddUser(user, opts.Group); err != nil {
			errs = append(errs, fmt.Errorf("create user %v: %w", user.SSHLoginName, err))
			continue
		}
		logging.L.Info().Str("username", user.SSHLoginName).Msg("created user")
	}

	if len(errs) > 0 {
		return cliopts.MultiError(errs)
	}
	return nil
}

// updateManagedUsersPasswordExpiration updates password expiration for all
// managed users on this system. It is called on every sync cycle so that any
// pre-existing infra-managed user eventually gets the long expiry set without
// requiring manual intervention.
func updateManagedUsersPasswordExpiration(ctx context.Context, localUsers []linux.LocalUser) error {
	var errs []error
	for _, lu := range localUsers {
		if !lu.IsManagedByInfra() {
			continue
		}
		logging.L.Debug().
			Str("operation", "update_password_expiration").
			Str("username", sanitizeUsernameForLogging(lu.Username)).
			Msg("updating_managed_user_password_expiration")

		if err := linux.SetPasswordExpiration(lu.Username, linux.MaxPasswordAgeDays); err != nil {
			errs = append(errs, fmt.Errorf("update password expiration for %s: %w", lu.Username, err))
		}
	}

	if len(errs) > 0 {
		return cliopts.MultiError(errs)
	}
	return nil
}

type sshdConfig struct {
	HostKeys []string
}

// Merge merges the other config into this config.
func (c *sshdConfig) Merge(other sshdConfig) {
	c.HostKeys = append(c.HostKeys, other.HostKeys...)
}

// readSSHDConfig reads sshd_config at filepath and returns some of the values.
// This parse is limited to the few fields we care about.
// See https://man.openbsd.org/sshd_config for details about the file format.
func readSSHDConfig(filename string, includeBasePath string) (sshdConfig, error) {
	fh, err := os.Open(filename)
	if err != nil {
		return sshdConfig{}, err
	}

	var result sshdConfig

	scan := bufio.NewScanner(fh)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		keyword := strings.ToLower(fields[0])

		switch keyword {
		case "include":
			includeName := fields[1]
			if !filepath.IsAbs(includeName) {
				includeName = filepath.Join(includeBasePath, includeName)
			}
			includedCfg, err := readSSHDConfig(includeName, includeBasePath)
			if err != nil {
				return sshdConfig{}, err
			}

			result.Merge(includedCfg)

		case "hostkey":
			result.HostKeys = append(result.HostKeys, fields[1])

			// TODO: end reading at the first Match keyword
		}
	}
	if err := scan.Err(); err != nil {
		return sshdConfig{}, fmt.Errorf("read sshd_config: %w", err)
	}
	return result, nil
}
