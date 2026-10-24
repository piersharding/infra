package connector

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/infrahq/infra/internal/certs"
	"github.com/infrahq/infra/internal/claims"
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
	authz *impersonationAuthorizer,
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

		// the connector's service account is the identity impersonating, so
		// impersonation headers from the client must never reach the API
		// server as sent: they would be checked against the connector's
		// permissions rather than the user's.
		requested, err := takeImpersonationHeaders(req.Header)
		if err != nil {
			status = http.StatusBadRequest
			writeStatus(resp, status, err.Error())
			return
		}

		if requested == nil {
			req.Header.Set("Impersonate-User", claim.Name)
			for _, g := range claim.Groups {
				req.Header.Add("Impersonate-Group", g)
			}
		} else {
			if err := authz.Authorize(req.Context(), token, claim, requested); err != nil {
				var denied impersonationDeniedError
				if errors.As(err, &denied) {
					status = http.StatusForbidden
					writeStatus(resp, status, err.Error())
					return
				}
				logging.L.Error().Err(err).Msgf("failed to authorize impersonation")
				status = http.StatusInternalServerError
				resp.WriteHeader(status)
				return
			}
			logging.L.Info().Str("user", claim.Name).Str("impersonated", requested.user).Msgf("user impersonating")
			for k, v := range requested.headers {
				req.Header[k] = v
			}
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		proxy.ServeHTTP(resp, req)
	}
}

// impersonationRequest is the identity a client asked to act as with the
// kubectl --as, --as-group, --as-uid and --as-user-extra flags.
type impersonationRequest struct {
	user   string
	groups []string
	uid    string
	extra  map[string][]string

	// headers are the impersonation headers as the client sent them, forwarded
	// once the request is authorized.
	headers http.Header
}

// takeImpersonationHeaders removes every impersonation header from h and
// returns the identity they requested, or nil when there were none.
func takeImpersonationHeaders(h http.Header) (*impersonationRequest, error) {
	r := &impersonationRequest{headers: http.Header{}}
	for k, v := range h {
		key := http.CanonicalHeaderKey(k)
		switch {
		case key == "Impersonate-User":
			r.user = v[0]
		case key == "Impersonate-Group":
			r.groups = append(r.groups, v...)
		case key == "Impersonate-Uid":
			r.uid = v[0]
		case strings.HasPrefix(key, "Impersonate-Extra-"):
			// matches the API server, which lowercases and unescapes the key
			extraKey, err := url.PathUnescape(strings.ToLower(strings.TrimPrefix(key, "Impersonate-Extra-")))
			if err != nil {
				return nil, fmt.Errorf("invalid impersonation header %q: %w", k, err)
			}
			if r.extra == nil {
				r.extra = map[string][]string{}
			}
			r.extra[extraKey] = append(r.extra[extraKey], v...)
		default:
			continue
		}
		r.headers[key] = append(r.headers[key], v...)
		delete(h, k)
	}

	if len(r.headers) == 0 {
		return nil, nil
	}
	if r.user == "" {
		return nil, errors.New("requested impersonation of groups, uid or extra without a user")
	}
	return r, nil
}

// impersonationAuthorizer checks with the kubernetes API whether a user may
// impersonate the identity they requested, as the API server would if the
// user had connected directly.
type impersonationAuthorizer struct {
	client *http.Client
	url    string
}

type impersonationDeniedError struct {
	user string
	attr resourceAttributes
}

func (e impersonationDeniedError) Error() string {
	resource := e.attr.Resource
	if e.attr.Subresource != "" {
		resource += "/" + e.attr.Subresource
	}
	return fmt.Sprintf("user %q cannot impersonate %s %q", e.user, resource, e.attr.Name)
}

type resourceAttributes struct {
	Namespace   string `json:"namespace,omitempty"`
	Verb        string `json:"verb"`
	Group       string `json:"group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Name        string `json:"name"`
}

// Authorize returns an impersonationDeniedError when claim may not
// impersonate any part of requested. It makes the same checks as the API
// server's impersonation filter, one SubjectAccessReview for each.
// ponytail: one review per requested attribute on every request, cache the
// results if --as traffic becomes significant.
func (a *impersonationAuthorizer) Authorize(ctx context.Context, token string, claim claims.Custom, requested *impersonationRequest) error {
	var attrs []resourceAttributes

	if ns, name, ok := serviceAccountName(requested.user); ok {
		attrs = append(attrs, resourceAttributes{Resource: "serviceaccounts", Namespace: ns, Name: name})
	} else {
		attrs = append(attrs, resourceAttributes{Resource: "users", Name: requested.user})
	}
	for _, g := range requested.groups {
		attrs = append(attrs, resourceAttributes{Resource: "groups", Name: g})
	}
	if requested.uid != "" {
		attrs = append(attrs, resourceAttributes{Group: "authentication.k8s.io", Resource: "uids", Name: requested.uid})
	}
	for key, values := range requested.extra {
		for _, v := range values {
			attrs = append(attrs, resourceAttributes{Group: "authentication.k8s.io", Resource: "userextras", Subresource: key, Name: v})
		}
	}

	// the API server adds system:authenticated to the identity the connector
	// impersonates, so the user's permissions are checked with it too.
	groups := append(slices.Clone(claim.Groups), "system:authenticated")

	for _, attr := range attrs {
		attr.Verb = "impersonate"
		allowed, err := a.review(ctx, token, claim.Name, groups, attr)
		if err != nil {
			return err
		}
		if !allowed {
			return impersonationDeniedError{user: claim.Name, attr: attr}
		}
	}
	return nil
}

// serviceAccountName splits a system:serviceaccount:<namespace>:<name>
// username, as the API server does to decide impersonation is of a service
// account.
func serviceAccountName(user string) (namespace, name string, ok bool) {
	rest, ok := strings.CutPrefix(user, "system:serviceaccount:")
	if !ok {
		return "", "", false
	}
	namespace, name, ok = strings.Cut(rest, ":")
	if !ok || namespace == "" || name == "" || strings.Contains(name, ":") {
		return "", "", false
	}
	return namespace, name, true
}

func (a *impersonationAuthorizer) review(ctx context.Context, token, user string, groups []string, attr resourceAttributes) (bool, error) {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "authorization.k8s.io/v1",
		"kind":       "SubjectAccessReview",
		"spec": map[string]any{
			"user":               user,
			"groups":             groups,
			"resourceAttributes": attr,
		},
	})
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	res, err := a.client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return false, fmt.Errorf("kubernetes API subject access review failed: %v", res.Status)
	}

	var review struct {
		Status struct {
			Allowed bool `json:"allowed"`
		} `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&review); err != nil {
		return false, fmt.Errorf("reading subject access review: %w", err)
	}
	return review.Status.Allowed, nil
}

// writeStatus writes a kubernetes Status, so that kubectl shows the message.
func writeStatus(resp http.ResponseWriter, code int, message string) {
	resp.Header().Set("Content-Type", "application/json")
	resp.WriteHeader(code)
	_ = json.NewEncoder(resp).Encode(map[string]any{
		"kind":       "Status",
		"apiVersion": "v1",
		"status":     "Failure",
		"message":    message,
		"reason":     strings.ReplaceAll(http.StatusText(code), " ", ""),
		"code":       code,
	})
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
