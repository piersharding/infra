package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	survey "github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/cli/browser"
	"github.com/goware/urlx"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/certs"
	"github.com/infrahq/infra/internal/cmd/cliopts"
	"github.com/infrahq/infra/internal/cmd/types"
	"github.com/infrahq/infra/internal/format"
	"github.com/infrahq/infra/internal/logging"
)

type loginCmdOptions struct {
	Server              string
	AccessKey           string
	SkipTLSVerify       bool
	TrustedCertificate  string
	TrustedFingerprint  string
	NonInteractive      bool
	NoAgent             bool
	User                string
	Password            string
	InjectUserSSHConfig bool
}

func newLoginCmd(cli *CLI) *cobra.Command {
	var options loginCmdOptions

	cmd := &cobra.Command{
		Use:     "login [SERVER]",
		Short:   "Login to Infra",
		Args:    MaxArgs(1),
		GroupID: groupCore,
		Example: `# Login
infra login example.infrahq.com

# Login with username and password (prompt for password)
infra login example.infrahq.com --user user@example.com

# Login with access key
export INFRA_SERVER=example.infrahq.com
export INFRA_ACCESS_KEY=2vrEbqFEUr.jtTlxkgYdvghJNdEa8YoUxN0
infra login example.infrahq.com

# Login with username and password
export INFRA_SERVER=example.infrahq.com
export INFRA_USER=user@example.com
export INFRA_PASSWORD=p4ssw0rd
infra login`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cliopts.DefaultsFromEnv("INFRA", cmd.Flags()); err != nil {
				return err
			}
			// There is no flag for server, so we check it separately
			if server, ok := os.LookupEnv("INFRA_SERVER"); ok {
				options.Server = server
			}

			if len(args) == 1 {
				options.Server = args[0]
			}

			if password, ok := os.LookupEnv("INFRA_PASSWORD"); ok {
				options.Password = password
			}

			if options.AccessKey == "" {
				options.AccessKey = os.Getenv("INFRA_ACCESS_KEY")
			}

			return login(cli, options)
		},
	}

	cmd.Flags().StringVar(&options.AccessKey, "key", "", "Login with an access key")
	cmd.Flags().StringVar(&options.User, "user", "", "User email")
	cmd.Flags().BoolVar(&options.SkipTLSVerify, "skip-tls-verify", false, "Skip verifying server TLS certificates")
	cmd.Flags().Var((*types.StringOrFile)(&options.TrustedCertificate), "tls-trusted-cert", "TLS certificate or CA used by the server")
	cmd.Flags().StringVar(&options.TrustedFingerprint, "tls-trusted-fingerprint", "", "SHA256 fingerprint of the server TLS certificate")
	cmd.Flags().BoolVar(&options.NoAgent, "no-agent", false, "Skip starting the Infra agent in the background")
	cmd.Flags().BoolVar(&options.InjectUserSSHConfig, "enable-ssh", false, "Update ~/.ssh/config after login to use infra for ssh (technical preview)")
	cmd.Flags().Lookup("enable-ssh").Hidden = true
	addNonInteractiveFlag(cmd.Flags(), &options.NonInteractive)
	return cmd
}

func login(cli *CLI, options loginCmdOptions) error {
	ctx := context.Background()
	config, err := readConfig()
	if err != nil {
		return err
	}

	if options.Server == "" {
		if options.NonInteractive {
			return Error{Message: "Non-interactive login requires the [SERVER] argument or the INFRA_SERVER environment variable to be set"}
		}

		options.Server, err = promptServer(cli, config)
		if err != nil {
			return err
		}
	}

	options.Server = strings.TrimPrefix(options.Server, "https://")
	options.Server = strings.TrimPrefix(options.Server, "http://")

	fmt.Fprintf(cli.Stderr, "  Logging in to %s\n", termenv.String(options.Server).Bold().String())

	if len(options.TrustedCertificate) == 0 {
		// Attempt to find a previously trusted certificate
		for _, hc := range config.Hosts {
			if equalHosts(hc.Host, options.Server) {
				options.TrustedCertificate = hc.TrustedCertificate
			}
		}
	}

	lc, err := newLoginClient(cli, options)
	if err != nil {
		return err
	}

	var loginRes *api.LoginResponse

	switch {
	case options.AccessKey != "":
		loginRes, err = lc.APIClient.Login(ctx, &api.LoginRequest{
			AccessKey: options.AccessKey,
		})
		if err != nil {
			return err
		}
	case options.User != "":
		fmt.Fprintf(cli.Stderr, "  Logging in as user %s\n", termenv.String(options.User).Bold().String())

		if options.Password == "" {
			if options.NonInteractive {
				return Error{Message: "Non-interactive login requires setting the INFRA_PASSWORD environment variable"}
			}

			if err := survey.AskOne(&survey.Password{Message: "Password:"}, &options.Password, cli.surveyIO, survey.WithValidator(survey.Required)); err != nil {
				return err
			}
		}

		loginRes, err = lc.APIClient.Login(ctx, &api.LoginRequest{
			PasswordCredentials: &api.LoginRequestPasswordCredentials{
				Name:     options.User,
				Password: options.Password,
			},
		})
		if err != nil {
			if api.ErrorStatusCode(err) == http.StatusUnauthorized {
				return &LoginError{Message: "your username or password may be invalid"}
			}

			return err
		}
	default:
		if options.NonInteractive {
			return Error{Message: "Non-interactive login requires setting either the INFRA_ACCESS_KEY or both the INFRA_USER and INFRA_PASSWORD environment variables"}
		}

		loginRes, err = deviceFlowLogin(ctx, lc.APIClient, cli)
		if err != nil {
			return err
		}
	}

	// Update the API client with the new access key from login
	lc.APIClient.AccessKey = loginRes.AccessKey

	if loginRes.PasswordUpdateRequired {
		fmt.Fprintf(cli.Stderr, "  Your password has expired. Please update your password.\n")

		for {
			password, err := promptSetPassword(cli, options.Password)
			if err != nil {
				return err
			}

			logging.Debugf("call server: update user %s", loginRes.UserID)
			if _, err := lc.APIClient.UpdateUser(ctx, &api.UpdateUserRequest{
				ID:          loginRes.UserID,
				Password:    password,
				OldPassword: options.Password,
			}); err != nil {
				if passwordError(cli, err) {
					continue
				}
				return err
			}

			fmt.Fprintf(os.Stderr, "  Updated password\n")
			break
		}
	}

	if err := updateInfraConfig(lc, loginRes); err != nil {
		return err
	}

	if err := updateKubeconfig(lc.APIClient); err != nil {
		return err
	}

	backgroundAgentRunning, err := configAgentRunning()
	if err != nil {
		// do not block login, just proceed, potentially without the agent
		logging.Errorf("unable to check background agent: %v", err)
	}

	if !backgroundAgentRunning && !options.NoAgent {
		// the agent is started in a separate command so that it continues after the login command has finished
		if err := execAgent(); err != nil {
			// user still has a valid session, so do not fail
			logging.Errorf("Unable to start agent, destinations will not be updated automatically: %v", err)
		}
	}

	fmt.Fprintf(cli.Stderr, "  Logged in as %s\n", termenv.String(loginRes.Name).Bold().String())

	if options.InjectUserSSHConfig {
		return updateUserSSHConfig(cli)
	}

	return nil
}

func equalHosts(x, y string) bool {
	return strings.TrimPrefix(x, "https://") == strings.TrimPrefix(y, "https://")
}

// Updates all configs with the current logged in session
func updateInfraConfig(lc loginClient, loginRes *api.LoginResponse) error {
	clientHostConfig := ClientHostConfig{
		Current:   true,
		UserID:    loginRes.UserID,
		Name:      loginRes.Name,
		AccessKey: loginRes.AccessKey,
		Expires:   loginRes.Expires,
	}

	t, ok := lc.APIClient.HTTP.Transport.(*http.Transport)
	if !ok {
		return fmt.Errorf("could not update infra config")
	}
	clientHostConfig.SkipTLSVerify = t.TLSClientConfig.InsecureSkipVerify
	if lc.TrustedCertificate != "" {
		clientHostConfig.TrustedCertificate = lc.TrustedCertificate
	}

	u, err := urlx.Parse(lc.APIClient.URL)
	if err != nil {
		return err
	}
	clientHostConfig.Host = u.Host

	if err := saveHostConfig(clientHostConfig); err != nil {
		return err
	}

	return nil
}

type loginClient struct {
	APIClient *api.Client
	// TrustedCertificate is a PEM encoded certificate that has been trusted by
	// the user for TLS communication with the server.
	TrustedCertificate string
}

// Only used when logging in or switching to a new session, since user has no credentials. Otherwise, use defaultAPIClient().
func newLoginClient(cli *CLI, options loginCmdOptions) (loginClient, error) {
	cfg := &ClientHostConfig{
		TrustedCertificate: options.TrustedCertificate,
		SkipTLSVerify:      options.SkipTLSVerify,
	}
	client, err := NewAPIClient(&APIClientOpts{
		Host:                     options.Server,
		Transport:                httpTransportForHostConfig(cfg),
		SkipLogoutOnUnauthorized: true, // if a user fails to login, any current sessions they have should remain active
	},
	)
	if err != nil {
		return loginClient{}, err
	}
	if err := cli.serverCompatible(client); err != nil {
		return loginClient{}, err
	}
	c := loginClient{
		APIClient:          client,
		TrustedCertificate: options.TrustedCertificate,
	}
	if options.SkipTLSVerify {
		return c, nil
	}

	// Prompt user only if server fails the TLS verification
	if err := attemptTLSRequest(options); err != nil {
		var uaErr x509.UnknownAuthorityError
		if !errors.As(err, &uaErr) {
			return c, err
		}

		if !fingerprintMatch(cli, options.TrustedFingerprint, uaErr.Cert) {
			if options.NonInteractive {
				if options.TrustedCertificate != "" {
					return c, err
				}
				return c, Error{
					Message: "The authenticity of the server could not be verified. " +
						"Use the --tls-trusted-cert flag to specify a trusted CA, or run " +
						"in interactive mode.",
				}
			}

			if err := promptVerifyTLSCert(cli, uaErr.Cert); err != nil {
				return c, err
			}
		}

		pool, err := x509.SystemCertPool()
		if err != nil {
			return c, err
		}
		pool.AddCert(uaErr.Cert)
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				// set min version to the same as default to make gosec linter happy
				MinVersion: tls.VersionTLS12,
				RootCAs:    pool,
			},
		}
		client, err := NewAPIClient(&APIClientOpts{
			Host:      options.Server,
			Transport: transport,
		})
		if err != nil {
			return c, err
		}
		c.APIClient = client
		c.TrustedCertificate = string(certs.PEMEncodeCertificate(uaErr.Cert.Raw))
	}
	return c, nil
}

func fingerprintMatch(cli *CLI, fingerprint string, cert *x509.Certificate) bool {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return false
	}

	if strings.EqualFold(fingerprint, certs.Fingerprint(cert.Raw)) {
		return true
	}

	fmt.Fprintf(cli.Stderr, `
%v TLS fingerprint from server does not match the trusted fingerprint.

Trusted: %v
Server:  %v
`,
		termenv.String("WARNING").Bold().String(),
		fingerprint,
		certs.Fingerprint(cert.Raw))
	return false
}

func attemptTLSRequest(options loginCmdOptions) error {
	reqURL := "https://" + options.Server
	// First attempt with the system cert pool
	req, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	logging.Debugf("call server: test tls for %q", reqURL)
	httpClient := http.Client{Timeout: 60 * time.Second}
	res, err := httpClient.Do(req)
	if err == nil {
		res.Body.Close()
		return nil
	}

	// Second attempt with an empty cert pool. This is necessary because at least
	// on darwin, the error is the wrong type when using the system cert pool.
	// See https://github.com/golang/go/issues/52010.
	req, err = http.NewRequestWithContext(context.TODO(), http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	pool := x509.NewCertPool()
	if options.TrustedCertificate != "" {
		pool.AppendCertsFromPEM([]byte(options.TrustedCertificate))
	}

	httpClient = http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}

	res, err = httpClient.Do(req)

	if err == nil {
		res.Body.Close()
		return nil
	}

	if connError := api.HandleConnError(err); connError != nil {
		return connError
	}

	return err
}

const spinChars = `\|/-`

func deviceFlowLogin(ctx context.Context, client *api.Client, cli *CLI) (*api.LoginResponse, error) {
	resp, err := client.StartDeviceFlow(ctx)
	if err != nil {
		return nil, err
	}

	url := resp.VerificationURI + "?code=" + resp.UserCode

	// display to user
	fmt.Fprintf(cli.Stderr, "  Navigate to %s and verify your code:\n\n", termenv.String(url).Underline().String())
	fmt.Fprintf(cli.Stderr, "\t\t%s\n\n", termenv.String(resp.UserCode).Bold().String())

	// we don't care if this fails. some devices won't be able to open the browser
	_ = browser.OpenURL(url)

	// poll for response
	timeout := time.NewTimer(time.Duration(resp.ExpiresInSeconds) * time.Second)
	defer timeout.Stop()
	poll := time.NewTicker(time.Duration(resp.PollIntervalSeconds) * time.Second)
	defer poll.Stop()
	spinner := time.NewTicker(1000 * time.Millisecond)
	defer spinner.Stop()

	var spinnerCount int = 0

	for {
		select {
		case <-spinner.C:
			spinnerCount++
			fmt.Printf("  %s\r", string(spinChars[spinnerCount%len(spinChars)]))
		case <-timeout.C:
			// too late. return an error
			return nil, api.ErrDeviceLoginTimeout
		case <-poll.C:
			// check to see if user is authed yet
			pollResp, err := client.GetDeviceFlowStatus(ctx, &api.DeviceFlowStatusRequest{DeviceCode: resp.DeviceCode})
			if err != nil {
				return nil, err
			}
			switch pollResp.Status {
			case api.DeviceFlowStatusExpired:
				return nil, Error{Message: "device approval request expired"}
			case api.DeviceFlowStatusConfirmed:
				return pollResp.LoginResponse, nil
			case api.DeviceFlowStatusPending:
			default:
				logging.Warnf("%s", "unexpected response status: "+pollResp.Status)
			}
		}
	}
}

func promptVerifyTLSCert(cli *CLI, cert *x509.Certificate) error {
	formatTime := func(t time.Time) string {
		return fmt.Sprintf("%v (%v)", format.HumanTime(t, "none"), t.Format(time.RFC1123))
	}
	title := "Certificate"
	if cert.IsCA {
		title = "Certificate Authority"
	}

	// TODO: improve this message
	// TODO: use color/bold to highlight important parts
	fmt.Fprintf(cli.Stderr, `
The certificate presented by the server is not trusted by your operating system.

%[6]v

Subject: %[1]s
Issuer: %[2]s

Validity
  Not Before: %[3]v
  Not After:  %[4]v

SHA256 Fingerprint
  %[5]v

Compare the SHA256 fingerprint to the one provided by your administrator to
manually verify the certificate can be trusted.

`,
		cert.Subject,
		cert.Issuer,
		formatTime(cert.NotBefore),
		formatTime(cert.NotAfter),
		certs.Fingerprint(cert.Raw),
		title,
	)
	confirmPrompt := &survey.Select{
		Message: "Options:",
		Options: []string{
			"NO",
			"TRUST",
		},
		Description: func(value string, index int) string {
			switch value {
			case "NO":
				return "I do not trust this certificate"
			case "TRUST":
				return "Trust and save the certificate"
			default:
				return ""
			}
		},
	}
	var selection string
	if err := survey.AskOne(confirmPrompt, &selection, cli.surveyIO); err != nil {
		return err
	}
	if selection == "TRUST" {
		return nil
	}
	return terminal.InterruptErr
}

// Returns the host address of the Infra server that user would like to log into
func promptServer(cli *CLI, config *ClientConfig) (string, error) {
	servers := config.Hosts

	if len(servers) == 0 {
		return promptNewServer(cli)
	}

	return promptServerList(cli, servers)
}

func promptNewServer(cli *CLI) (string, error) {
	var server string
	err := survey.AskOne(
		&survey.Input{Message: "Server:"},
		&server,
		cli.surveyIO,
		survey.WithValidator(survey.Required),
	)
	return strings.TrimSpace(server), err
}

func promptServerList(cli *CLI, servers []ClientHostConfig) (string, error) {
	var promptOptions []string
	for _, server := range servers {
		promptOptions = append(promptOptions, server.Host)
	}

	defaultOption := "Connect to a new server"
	promptOptions = append(promptOptions, defaultOption)

	prompt := &survey.Select{
		Message: "Select a server:",
		Options: promptOptions,
	}

	filter := func(filterValue string, optValue string, optIndex int) bool {
		return strings.Contains(optValue, filterValue) || strings.EqualFold(optValue, defaultOption)
	}

	var i int
	if err := survey.AskOne(prompt, &i, survey.WithFilter(filter), cli.surveyIO); err != nil {
		return "", err
	}

	if promptOptions[i] == defaultOption {
		return promptNewServer(cli)
	}

	return servers[i].Host, nil
}
