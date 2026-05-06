package cmd

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/infrahq/infra/api"
	humanfmt "github.com/infrahq/infra/internal/format"
)

func newStatusCmd(cli *CLI) *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show the current session status",
		Args:    NoArgs,
		GroupID: groupOther,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cli)
		},
	}
}

func runStatus(cli *CLI) error {
	config, err := currentHostConfig()
	if err != nil {
		return err
	}

	if !config.isLoggedIn() {
		return Error{Message: "Not logged in. Run 'infra login' to continue."}
	}

	w := tabwriter.NewWriter(cli.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()

	fmt.Fprintln(w)
	fmt.Fprintf(w, "User:\t %s\n", config.Name)
	fmt.Fprintf(w, "Server:\t %s\n", config.Host)

	// Verify session and get user details from server.
	client, err := cli.apiClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	user, err := client.GetUserSelf(ctx)
	if err != nil {
		if api.ErrorStatusCode(err) == 401 {
			fmt.Fprintf(w, "Status:\t ✗ Expired — run 'infra login' to continue\n")
			fmt.Fprintln(w)
			return nil
		}
		return err
	}

	org, err := client.GetOrganizationSelf(ctx)
	if err != nil {
		return err
	}

	// Provider name from the server response.
	if len(user.ProviderNames) > 0 {
		fmt.Fprintf(w, "Provider:\t %s\n", user.ProviderNames[0])
	}
	fmt.Fprintf(w, "Organisation:\t %s\n", org.Name)

	// Session hard-expiry from local config.
	expiry := time.Time(config.Expires)
	if !expiry.IsZero() {
		remaining := time.Until(expiry)
		fmt.Fprintf(w, "Session expires:\t %s (%s)\n",
			humanfmt.HumanDurationWithCase(remaining, false)+" remaining",
			expiry.UTC().Format("2006-01-02 15:04 UTC"),
		)
	}

	// Inactivity deadline: only show it when a single key can be matched to the
	// current session with reasonable confidence.
	keys, err := client.ListAccessKeys(ctx, api.ListAccessKeysRequest{
		UserID:      config.UserID,
		ShowExpired: false,
	})
	if err == nil && len(keys.Items) > 0 {
		inactivityDeadline := sessionInactivityDeadline(config, keys.Items)
		if !inactivityDeadline.IsZero() {
			remaining := time.Until(inactivityDeadline)
			if remaining > 0 {
				fmt.Fprintf(w, "Inactivity limit:\t %s remaining\n",
					humanfmt.HumanDurationWithCase(remaining, false),
				)
			} else {
				fmt.Fprintf(w, "Inactivity limit:\t expired\n")
			}
		}
	}

	// Status line.
	expiringSoon := !expiry.IsZero() && time.Until(expiry) < 24*time.Hour
	if expiringSoon {
		fmt.Fprintf(w, "Status:\t ⚠ Active (session expires soon — run 'infra login' to renew)\n")
	} else {
		fmt.Fprintf(w, "Status:\t ✓ Active\n")
	}

	fmt.Fprintln(w)
	return nil
}

func sessionInactivityDeadline(config *ClientHostConfig, keys []api.AccessKey) time.Time {
	candidates := make([]api.AccessKey, 0, len(keys))
	for _, key := range keys {
		if key.IssuedForID != config.UserID {
			continue
		}
		candidates = append(candidates, key)
	}

	if config.ProviderID != 0 {
		providerMatches := make([]api.AccessKey, 0, len(candidates))
		for _, key := range candidates {
			if key.ProviderID == config.ProviderID {
				providerMatches = append(providerMatches, key)
			}
		}
		if len(providerMatches) > 0 {
			candidates = providerMatches
		}
	}

	expiry := time.Time(config.Expires)
	if !expiry.IsZero() {
		expiryMatches := make([]api.AccessKey, 0, len(candidates))
		for _, key := range candidates {
			if time.Time(key.Expires).Equal(expiry) {
				expiryMatches = append(expiryMatches, key)
			}
		}
		switch len(expiryMatches) {
		case 0:
		case 1:
			return time.Time(expiryMatches[0].InactivityTimeout)
		default:
			return time.Time{}
		}
	}

	if len(candidates) == 1 {
		return time.Time(candidates[0].InactivityTimeout)
	}

	return time.Time{}
}
