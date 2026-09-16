package connector

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/infrahq/infra/internal/certs"
	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/metrics"
)

// bearerTokenSource provides the credentials the connector uses to authenticate
// to the kubernetes API. It is called per request rather than read once, so
// that a rotated service account token is picked up without a restart.
type bearerTokenSource func() (string, error)

func proxyMiddleware(
	proxy *httputil.ReverseProxy,
	authn *authenticator,
	bearerToken bearerTokenSource,
) func(resp http.ResponseWriter, req *http.Request) {
	return func(resp http.ResponseWriter, req *http.Request) {
		start := time.Now()
		status := http.StatusOK
		defer func() {
			metrics.RequestDuration.With(prometheus.Labels{
				"host":     req.Host,
				"method":   req.Method,
				"path":     "proxy",
				"status":   strconv.Itoa(status),
				"blocking": "false",
			}).Observe(time.Since(start).Seconds())
		}()

		claim, err := authn.Authenticate(req)
		if err != nil {
			logging.L.Info().Err(err).Msgf("failed to authenticate request")
			resp.WriteHeader(http.StatusUnauthorized)
			status = http.StatusUnauthorized
			return
		}

		token, err := bearerToken()
		if err != nil {
			logging.L.Error().Err(err).Msgf("failed to load kubernetes credentials")
			resp.WriteHeader(http.StatusInternalServerError)
			status = http.StatusInternalServerError
			return
		}

		req.Header.Set("Impersonate-User", claim.Name)
		for _, g := range claim.Groups {
			req.Header.Add("Impersonate-Group", g)
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		proxy.ServeHTTP(resp, req)
	}
}

// logUpstreamErrors reports responses from the kubernetes API that indicate a
// problem with the connector's own credentials. The reverse proxy passes the
// upstream response straight back to the client, so without this a connector
// that cannot authenticate looks healthy while every proxied request fails.
func logUpstreamErrors(res *http.Response) error {
	var path string
	if res.Request != nil {
		path = res.Request.URL.Path
	}

	switch {
	case res.StatusCode == http.StatusUnauthorized, res.StatusCode == http.StatusForbidden:
		// the end user was already authenticated by proxyMiddleware, so a
		// rejection here is the connector's credentials being refused.
		logging.L.Error().
			Int("status", res.StatusCode).
			Str("path", path).
			Msgf("kubernetes API rejected the connector credentials")
	case res.StatusCode >= 400:
		logging.L.Debug().
			Int("status", res.StatusCode).
			Str("path", path).
			Msgf("kubernetes API request failed")
	}

	return nil
}

// credentialCheck reports whether the kubernetes API still accepts the
// connector's credentials. Results are cached for interval so that probe
// traffic does not reach the API server on every request.
type credentialCheck struct {
	client      *http.Client
	url         string
	bearerToken bearerTokenSource
	interval    time.Duration

	mu      sync.Mutex
	checked time.Time
	err     error
}

// selfSubjectReviewRequest is the body of a SelfSubjectReview creation. The
// type carries no spec, only the status the API server fills in, so the body
// is just the type identifier.
const selfSubjectReviewRequest = `{"apiVersion":"authentication.k8s.io/v1","kind":"SelfSubjectReview"}`

// selfSubjectReviewResponse is the part of the SelfSubjectReview status the
// check reads: the identity the API server resolved the credentials to.
type selfSubjectReviewResponse struct {
	Status struct {
		UserInfo struct {
			Username string   `json:"username"`
			Groups   []string `json:"groups"`
		} `json:"userInfo"`
	} `json:"status"`
}

func (c *credentialCheck) Check(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.checked.IsZero() && time.Since(c.checked) < c.interval {
		return c.err
	}

	c.err = c.check(ctx)
	c.checked = time.Now()
	return c.err
}

func (c *credentialCheck) check(ctx context.Context) error {
	token, err := c.bearerToken()
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(selfSubjectReviewRequest))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}()

	// create on authentication.k8s.io selfsubjectreviews is granted to every
	// authenticated identity by the default system:basic-user role, so a
	// rejection is the credentials being refused rather than a missing
	// permission.
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return fmt.Errorf("kubernetes API rejected the connector credentials: %v", res.Status)
	}

	// every other non-success is also reported as not ready: a 404 means the
	// check cannot detect an expired token at all, and an API server serving
	// 5xx cannot serve proxied requests either, so 503 is the honest state.
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("kubernetes API credential check failed: %v", res.Status)
	}

	// the response reports the identity the token resolved to, so a request
	// that reached the API server without credentials is caught here rather
	// than passing as an anonymous success.
	var review selfSubjectReviewResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&review); err != nil {
		return fmt.Errorf("reading kubernetes API identity: %w", err)
	}

	if !slices.Contains(review.Status.UserInfo.Groups, "system:authenticated") {
		return fmt.Errorf("kubernetes API did not authenticate the connector credentials: identity is %q", review.Status.UserInfo.Username)
	}

	return nil
}

func readyHandler(check *credentialCheck) func(resp http.ResponseWriter, req *http.Request) {
	return func(resp http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			resp.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		if err := check.Check(req.Context()); err != nil {
			logging.L.Warn().Err(err).Msgf("connector is not ready")
			resp.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		resp.WriteHeader(http.StatusOK)
	}
}

type CertCache struct {
	mu     sync.Mutex
	caCert []byte
	caKey  []byte
	hosts  []string
	cert   *tls.Certificate
}

func (c *CertCache) AddHost(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, h := range c.hosts {
		if h == host {
			return c.cert, nil
		}
	}

	c.hosts = append(c.hosts, host)

	logging.Debugf("generating certificate for: %v", c.hosts)

	ca, err := tls.X509KeyPair(c.caCert, c.caKey)
	if err != nil {
		return nil, err
	}

	caCert, err := x509.ParseCertificate(ca.Certificate[0])
	if err != nil {
		return nil, err
	}

	certPEM, keyPEM, err := certs.GenerateCertificate(c.hosts, caCert, ca.PrivateKey)
	if err != nil {
		return nil, err
	}

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	c.cert = &tlsCert

	return c.cert, nil
}

// readCertificate is a threadsafe way to read the certificate
func (c *CertCache) readCertificate() *tls.Certificate {
	c.mu.Lock()
	cert := c.cert
	c.mu.Unlock()
	return cert
}

// Certificate returns a TLS certificate for the connector, if one does not exist it is generated for the empty host
func (c *CertCache) Certificate() (*tls.Certificate, error) {
	cert := c.readCertificate()
	if cert == nil {
		// the host is not available externally, or this would have been set
		// set to an empty host for the liveness check to resolve from the same host
		return c.AddHost("")
	}

	return cert, nil
}

func NewCertCache(caCertPEM []byte, caKeyPem []byte) *CertCache {
	return &CertCache{caCert: caCertPEM, caKey: caKeyPem}
}
