package connector

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"gotest.tools/v3/assert"
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/claims"
	"github.com/infrahq/infra/internal/kubernetes"
	"github.com/infrahq/infra/internal/server"
	"github.com/infrahq/infra/uid"
)

func TestAuthenticator_Authenticate(t *testing.T) {
	type testCase struct {
		name        string
		setup       func(t *testing.T, req *http.Request)
		fakeClient  fakeClient
		expectedErr string
		expected    func(t *testing.T, claims claims.Custom)
	}

	pub, priv := generateJWK(t)

	run := func(t *testing.T, tc testCase) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/apis", nil)
		if tc.setup != nil {
			tc.setup(t, req)
		}

		opts := Options{
			Server: ServerOptions{SkipTLSVerify: true, AccessKey: "the-access-key"},
		}
		assert.NilError(t, opts.Server.URL.Set("https://127.0.0.1:12345"))
		authn := newAuthenticator(opts)
		authn.client = tc.fakeClient

		actual, err := authn.Authenticate(req)
		if tc.expectedErr != "" {
			assert.ErrorContains(t, err, tc.expectedErr)
			return
		}
		assert.NilError(t, err)

		if tc.expected != nil {
			tc.expected(t, actual)
		}
	}

	testCases := []testCase{
		{
			name:        "no auth header",
			expectedErr: "no bearer token found",
		},
		{
			name: "no token",
			setup: func(t *testing.T, req *http.Request) {
				req.Header.Set("Authorization", "username:password")
			},
			expectedErr: "invalid JWT signature",
		},
		{
			name: "invalid JWK",
			setup: func(t *testing.T, req *http.Request) {
				req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZW1haWwiOiJ0ZXN0QHRlc3QuY29tIiwiaWF0IjoxNTE2MjM5MDIyfQ.j7o5o8GBkybaYXdFJIi8O6mPF50E-gJWZ3reLfMQD68")
			},
			fakeClient:  fakeClient{err: fmt.Errorf("server not available")},
			expectedErr: "get JWK from server: server not available",
		},
		{
			name: "invalid JWT",
			setup: func(t *testing.T, req *http.Request) {
				req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZW1haWwiOiJ0ZXN0QHRlc3QuY29tIiwiaWF0IjoxNTE2MjM5MDIyfQ.j7o5o8GBkybaYXdFJIi8O6mPF50E-gJWZ3reLfMQD68")
			},
			fakeClient:  fakeClient{key: *pub},
			expectedErr: "error in cryptographic primitive",
		},
		{
			name: "expired JWT",
			setup: func(t *testing.T, req *http.Request) {
				j := generateJWT(t, priv, "test@example.com", time.Now().Add(-1*time.Hour))
				req.Header.Set("Authorization", "Bearer "+j)
			},
			fakeClient:  fakeClient{key: *pub},
			expectedErr: "token is expired",
		},
		{
			name: "no username in JWT",
			setup: func(t *testing.T, req *http.Request) {
				j := generateJWT(t, priv, "", time.Now().Add(time.Hour))
				req.Header.Set("Authorization", "Bearer "+j)
			},
			fakeClient:  fakeClient{key: *pub},
			expectedErr: "no username in JWT claim",
		},
		{
			name: "valid JWT",
			setup: func(t *testing.T, req *http.Request) {
				j := generateJWT(t, priv, "test@example.com", time.Now().Add(time.Hour))
				req.Header.Set("Authorization", "Bearer "+j)
			},
			fakeClient: fakeClient{key: *pub},
			expected: func(t *testing.T, actual claims.Custom) {
				expected := claims.Custom{
					Name:   "test@example.com",
					Groups: []string{"developers"},
				}
				assert.DeepEqual(t, actual, expected)
			},
		},
		{
			name: "error status code from server",
			setup: func(t *testing.T, req *http.Request) {
				j := generateJWT(t, priv, "test@example.com", time.Now().Add(time.Hour))
				req.Header.Set("Authorization", "Bearer "+j)
			},
			fakeClient:  fakeClient{key: *pub, statusCode: http.StatusBadRequest},
			expectedErr: "Bad Request",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			run(t, tc)
		})
	}
}

func generateJWK(t *testing.T) (pub *jose.JSONWebKey, priv *jose.JSONWebKey) {
	t.Helper()
	pubkey, key, err := ed25519.GenerateKey(rand.Reader)
	assert.NilError(t, err)

	priv = &jose.JSONWebKey{Key: key, KeyID: "", Algorithm: string(jose.ED25519), Use: "sig"}
	thumb, err := priv.Thumbprint(crypto.SHA256)
	assert.NilError(t, err)

	kid := base64.URLEncoding.EncodeToString(thumb)
	priv.KeyID = kid
	pub = &jose.JSONWebKey{Key: pubkey, KeyID: kid, Algorithm: string(jose.ED25519), Use: "sig"}
	return pub, priv
}

func generateJWT(t *testing.T, priv *jose.JSONWebKey, email string, expiry time.Time) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: priv}, (&jose.SignerOptions{}).WithType("JWT"))
	assert.NilError(t, err)

	cl := jwt.Claims{
		Issuer:   "InfraHQ",
		Expiry:   jwt.NewNumericDate(expiry),
		IssuedAt: jwt.NewNumericDate(time.Now()),
	}

	custom := claims.Custom{
		Name:   email,
		Groups: []string{"developers"},
	}

	raw, err := jwt.Signed(signer).Claims(cl).Claims(custom).Serialize()
	assert.NilError(t, err)
	return raw
}

type fakeClient struct {
	key        jose.JSONWebKey
	err        error
	statusCode int
}

func (f fakeClient) Do(req *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}

	r := server.WellKnownJWKResponse{Keys: []jose.JSONWebKey{f.key}}

	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(r)
	if err != nil {
		return nil, err
	}

	if authHeader := req.Header.Get("Authorization"); authHeader != "Bearer the-access-key" {
		return nil, fmt.Errorf("missing authorization header or wrong access key: %v", authHeader)
	}

	code := http.StatusOK
	if f.statusCode != 0 {
		code = f.statusCode
	}
	resp := &http.Response{
		StatusCode: code,
		Status:     http.StatusText(code),
		Body:       io.NopCloser(&buf),
	}
	return resp, nil
}

func TestProxyMiddleware_BearerTokenIsReadPerRequest(t *testing.T) {
	pub, priv := generateJWK(t)

	var forwarded []string
	upstream := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		forwarded = append(forwarded, req.Header.Get("Authorization"))
		resp.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, err := url.Parse(upstream.URL)
	assert.NilError(t, err)

	opts := Options{Server: ServerOptions{SkipTLSVerify: true, AccessKey: "the-access-key"}}
	assert.NilError(t, opts.Server.URL.Set("https://127.0.0.1:12345"))
	authn := newAuthenticator(opts)
	authn.client = fakeClient{key: *pub}

	// the token file is rotated between the two requests
	token := "first-token"
	handler := proxyMiddleware(
		httputil.NewSingleHostReverseProxy(upstreamURL),
		authn,
		func() (string, error) { return token, nil },
		nil,
	)

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/apis", nil)
		req.Header.Set("Authorization", "Bearer "+generateJWT(t, priv, "user@example.com", time.Now().Add(time.Hour)))
		resp := httptest.NewRecorder()
		handler(resp, req)
		return resp
	}

	assert.Equal(t, send().Code, http.StatusOK)
	token = "rotated-token"
	assert.Equal(t, send().Code, http.StatusOK)

	assert.DeepEqual(t, forwarded, []string{"Bearer first-token", "Bearer rotated-token"})
}

func TestProxyMiddleware_BearerTokenUnavailable(t *testing.T) {
	pub, priv := generateJWK(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		t.Error("request should not have reached the kubernetes API")
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, err := url.Parse(upstream.URL)
	assert.NilError(t, err)

	opts := Options{Server: ServerOptions{SkipTLSVerify: true, AccessKey: "the-access-key"}}
	assert.NilError(t, opts.Server.URL.Set("https://127.0.0.1:12345"))
	authn := newAuthenticator(opts)
	authn.client = fakeClient{key: *pub}

	handler := proxyMiddleware(
		httputil.NewSingleHostReverseProxy(upstreamURL),
		authn,
		func() (string, error) { return "", errors.New("no such file") },
		nil,
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/apis", nil)
	req.Header.Set("Authorization", "Bearer "+generateJWT(t, priv, "user@example.com", time.Now().Add(time.Hour)))
	resp := httptest.NewRecorder()
	handler(resp, req)

	assert.Equal(t, resp.Code, http.StatusInternalServerError)
}

func TestProxyMiddleware_Impersonation(t *testing.T) {
	pub, priv := generateJWK(t)

	type sar struct {
		Spec struct {
			User               string             `json:"user"`
			Groups             []string           `json:"groups"`
			ResourceAttributes resourceAttributes `json:"resourceAttributes"`
		} `json:"spec"`
	}

	type testCase struct {
		name           string
		headers        http.Header
		allowed        []string // resource/name pairs user@example.com may impersonate
		expectedCode   int
		expectedHeader http.Header // impersonation headers reaching the kubernetes API
		expectedBody   string
	}

	testCases := []testCase{
		{
			name:         "no impersonation requested",
			expectedCode: http.StatusOK,
			expectedHeader: http.Header{
				"Impersonate-User":  {"user@example.com"},
				"Impersonate-Group": {"developers"},
			},
		},
		{
			name: "escalation to system:masters is refused",
			headers: http.Header{
				"Impersonate-User":  {"user@example.com"},
				"Impersonate-Group": {"system:masters"},
			},
			allowed:      []string{"users/user@example.com"},
			expectedCode: http.StatusForbidden,
			expectedBody: `cannot impersonate groups \"system:masters\"`,
		},
		{
			name:         "group without user is rejected",
			headers:      http.Header{"Impersonate-Group": {"system:masters"}},
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "extra without user is rejected",
			headers:      http.Header{"Impersonate-Extra-Scopes": {"view"}},
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "impersonating a user without permission",
			headers:      http.Header{"Impersonate-User": {"bob"}},
			expectedCode: http.StatusForbidden,
			expectedBody: `user \"user@example.com\" cannot impersonate users \"bob\"`,
		},
		{
			name:         "impersonating a permitted user",
			headers:      http.Header{"Impersonate-User": {"bob"}},
			allowed:      []string{"users/bob"},
			expectedCode: http.StatusOK,
			expectedHeader: http.Header{
				"Impersonate-User": {"bob"},
			},
		},
		{
			name: "impersonating a permitted user and groups",
			headers: http.Header{
				"Impersonate-User":         {"bob"},
				"Impersonate-Group":        {"ops", "devs"},
				"Impersonate-Uid":          {"1234"},
				"Impersonate-Extra-Scopes": {"view"},
			},
			allowed:      []string{"users/bob", "groups/ops", "groups/devs", "uids/1234", "userextras/scopes/view"},
			expectedCode: http.StatusOK,
			expectedHeader: http.Header{
				"Impersonate-User":         {"bob"},
				"Impersonate-Group":        {"ops", "devs"},
				"Impersonate-Uid":          {"1234"},
				"Impersonate-Extra-Scopes": {"view"},
			},
		},
		{
			name: "one group not permitted",
			headers: http.Header{
				"Impersonate-User":  {"bob"},
				"Impersonate-Group": {"ops", "admins"},
			},
			allowed:      []string{"users/bob", "groups/ops"},
			expectedCode: http.StatusForbidden,
			expectedBody: `cannot impersonate groups \"admins\"`,
		},
		{
			name:         "impersonating a permitted service account",
			headers:      http.Header{"Impersonate-User": {"system:serviceaccount:apps:builder"}},
			allowed:      []string{"serviceaccounts/apps/builder"},
			expectedCode: http.StatusOK,
			expectedHeader: http.Header{
				"Impersonate-User": {"system:serviceaccount:apps:builder"},
			},
		},
		{
			name:         "service account permission is namespaced",
			headers:      http.Header{"Impersonate-User": {"system:serviceaccount:apps:builder"}},
			allowed:      []string{"users/system:serviceaccount:apps:builder"},
			expectedCode: http.StatusForbidden,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var forwarded http.Header
			upstream := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/apis/authorization.k8s.io/v1/subjectaccessreviews" {
					forwarded = http.Header{}
					for k, v := range req.Header {
						if strings.HasPrefix(k, "Impersonate-") {
							forwarded[k] = v
						}
					}
					resp.WriteHeader(http.StatusOK)
					return
				}

				assert.Equal(t, req.Header.Get("Authorization"), "Bearer the-token")
				var review sar
				assert.NilError(t, json.NewDecoder(req.Body).Decode(&review))
				assert.Equal(t, review.Spec.User, "user@example.com")
				assert.DeepEqual(t, review.Spec.Groups, []string{"developers", "system:authenticated"})

				attr := review.Spec.ResourceAttributes
				assert.Equal(t, attr.Verb, "impersonate")
				key := strings.Join(slices.DeleteFunc([]string{attr.Resource, attr.Subresource, attr.Namespace, attr.Name}, func(s string) bool { return s == "" }), "/")
				_, _ = fmt.Fprintf(resp, `{"status":{"allowed":%v}}`, slices.Contains(tc.allowed, key))
			}))
			t.Cleanup(upstream.Close)

			upstreamURL, err := url.Parse(upstream.URL)
			assert.NilError(t, err)

			opts := Options{Server: ServerOptions{SkipTLSVerify: true, AccessKey: "the-access-key"}}
			assert.NilError(t, opts.Server.URL.Set("https://127.0.0.1:12345"))
			authn := newAuthenticator(opts)
			authn.client = fakeClient{key: *pub}

			handler := proxyMiddleware(
				httputil.NewSingleHostReverseProxy(upstreamURL),
				authn,
				func() (string, error) { return "the-token", nil },
				&impersonationAuthorizer{client: upstream.Client(), url: upstream.URL + "/apis/authorization.k8s.io/v1/subjectaccessreviews"},
			)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/pods", nil)
			for k, v := range tc.headers {
				req.Header[k] = v
			}
			req.Header.Set("Authorization", "Bearer "+generateJWT(t, priv, "user@example.com", time.Now().Add(time.Hour)))
			resp := httptest.NewRecorder()
			handler(resp, req)

			assert.Equal(t, resp.Code, tc.expectedCode, resp.Body.String())
			assert.Assert(t, strings.Contains(resp.Body.String(), tc.expectedBody), resp.Body.String())
			if tc.expectedCode != http.StatusOK {
				assert.Assert(t, forwarded == nil, "request should not have reached the kubernetes API")
				return
			}
			assert.DeepEqual(t, forwarded, tc.expectedHeader)
		})
	}
}

func TestCredentialCheck(t *testing.T) {
	const authenticated = `{"status":{"userInfo":{"username":"system:serviceaccount:infrahq:connector","groups":["system:serviceaccounts","system:authenticated"]}}}`

	type testCase struct {
		name        string
		statusCode  int
		body        string
		bearerToken bearerTokenSource
		expectedErr string
	}

	testCases := []testCase{
		{
			name:       "credentials accepted",
			statusCode: http.StatusCreated,
			body:       authenticated,
		},
		{
			name:        "credentials expired",
			statusCode:  http.StatusUnauthorized,
			expectedErr: "rejected the connector credentials",
		},
		{
			name:        "credentials forbidden",
			statusCode:  http.StatusForbidden,
			expectedErr: "rejected the connector credentials",
		},
		{
			name:        "endpoint unavailable",
			statusCode:  http.StatusNotFound,
			expectedErr: "credential check failed",
		},
		{
			name:        "api server error",
			statusCode:  http.StatusInternalServerError,
			expectedErr: "credential check failed",
		},
		{
			name:        "request sent without credentials",
			statusCode:  http.StatusCreated,
			body:        `{"status":{"userInfo":{"username":"system:anonymous","groups":["system:unauthenticated"]}}}`,
			expectedErr: `did not authenticate the connector credentials: identity is "system:anonymous"`,
		},
		{
			name:        "unreadable response",
			statusCode:  http.StatusCreated,
			body:        `{`,
			expectedErr: "reading kubernetes API identity",
		},
		{
			name:        "token unreadable",
			statusCode:  http.StatusCreated,
			body:        authenticated,
			bearerToken: func() (string, error) { return "", errors.New("no such file") },
			expectedErr: "no such file",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var requests int
			upstream := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
				requests++
				assert.Equal(t, req.Method, http.MethodPost)
				assert.Equal(t, req.URL.Path, "/apis/authentication.k8s.io/v1/selfsubjectreviews")
				assert.Equal(t, req.Header.Get("Authorization"), "Bearer the-token")
				assert.Equal(t, req.Header.Get("Content-Type"), "application/json")

				body, err := io.ReadAll(req.Body)
				assert.NilError(t, err)
				assert.Equal(t, string(body), `{"apiVersion":"authentication.k8s.io/v1","kind":"SelfSubjectReview"}`)

				resp.WriteHeader(tc.statusCode)
				_, _ = resp.Write([]byte(tc.body))
			}))
			t.Cleanup(upstream.Close)

			bearerToken := tc.bearerToken
			if bearerToken == nil {
				bearerToken = func() (string, error) { return "the-token", nil }
			}

			check := &credentialCheck{
				client:      upstream.Client(),
				url:         upstream.URL + "/apis/authentication.k8s.io/v1/selfsubjectreviews",
				bearerToken: bearerToken,
				interval:    time.Hour,
			}

			err := check.Check(context.Background())
			if tc.expectedErr != "" {
				assert.ErrorContains(t, err, tc.expectedErr)
			} else {
				assert.NilError(t, err)
			}

			// the result is cached, so a second call must not reach the API
			before := requests
			assert.Equal(t, fmt.Sprint(check.Check(context.Background())), fmt.Sprint(err))
			assert.Equal(t, requests, before)
		})
	}
}

func TestCertCache_Certificate(t *testing.T) {
	if testing.Short() {
		t.Skip("too slow for short run")
	}
	testCACertPEM, err := os.ReadFile("./_testdata/test-ca-cert.pem")
	assert.NilError(t, err)

	testCAKeyPEM, err := os.ReadFile("./_testdata/test-ca-key.pem")
	assert.NilError(t, err)

	t.Run("no cached certificate adds empty certificate", func(t *testing.T) {
		certCache := NewCertCache(testCACertPEM, testCAKeyPEM)

		cert, err := certCache.Certificate()

		assert.NilError(t, err)
		assert.Equal(t, len(certCache.hosts), 1)
		assert.Equal(t, certCache.hosts[0], "")
		assert.Assert(t, cert != nil)
	})

	t.Run("cached certificate is returned when the host is set", func(t *testing.T) {
		certCache := NewCertCache(testCACertPEM, testCAKeyPEM)
		_, err := certCache.AddHost("test-host")
		assert.NilError(t, err)

		cert, err := certCache.Certificate()

		assert.NilError(t, err)
		assert.Equal(t, len(certCache.hosts), 1)
		assert.Equal(t, certCache.hosts[0], "test-host")

		parsedCert, err := x509.ParseCertificate(cert.Certificate[0])
		assert.NilError(t, err)
		assert.Equal(t, len(parsedCert.DNSNames), 1)
		assert.Equal(t, parsedCert.DNSNames[0], "test-host")
	})
}

func TestSyncGrantsToDestination_KubeBindings(t *testing.T) {
	type testCase struct {
		name                     string
		fakeAPI                  *fakeAPIClient
		fakeKube                 *fakeKubeClient
		expectedListGrantIndexes []int64
		successCount             int
	}

	run := func(t *testing.T, tc testCase) {
		ctx := context.Background()
		waiter := &fakeWaiter{endAtIndex: 1}
		if tc.fakeKube == nil {
			tc.fakeKube = &fakeKubeClient{}
		}
		con := &connector{
			k8s:         tc.fakeKube,
			client:      tc.fakeAPI,
			destination: &api.Destination{Name: "the-dest"},
		}

		fn := func(ctx context.Context, grants []api.Grant) error {
			return updateRoles(ctx, con.client, con.k8s, grants)
		}
		err := syncGrantsToDestination(ctx, con, waiter, fn)
		assert.ErrorIs(t, err, errDone)

		assert.Equal(t, len(waiter.resets), tc.successCount)
		assert.DeepEqual(t, tc.fakeAPI.listGrantsIndexes, tc.expectedListGrantIndexes)
	}

	testCases := []testCase{
		{
			name: "successful update",
			fakeAPI: &fakeAPIClient{
				listGrantsResult: &api.ListResponse[api.Grant]{
					Items: []api.Grant{
						{User: uid.ID(123), Resource: "the-test", Privilege: "view"},
						{User: uid.ID(124), Resource: "the-test.ns1", Privilege: "logs"},
					},
					LastUpdateIndex: api.LastUpdateIndex{Index: 42},
				},
			},
			expectedListGrantIndexes: []int64{1, 42},
			successCount:             2,
		},
		{
			name: "api blocking request timeout",
			fakeAPI: &fakeAPIClient{
				listGrantsError: api.Error{Code: http.StatusNotModified},
			},
			expectedListGrantIndexes: []int64{1, 1},
			successCount:             2,
		},
		{
			name: "error from api",
			fakeAPI: &fakeAPIClient{
				listGrantsError: api.Error{Code: http.StatusInternalServerError},
			},
			expectedListGrantIndexes: []int64{1, 1},
		},
		{
			name: "failed to update kube",
			fakeAPI: &fakeAPIClient{
				listGrantsResult: &api.ListResponse[api.Grant]{
					Items: []api.Grant{
						{User: uid.ID(123), Resource: "the-test", Privilege: "view"},
					},
					LastUpdateIndex: api.LastUpdateIndex{Index: 42},
				},
			},
			expectedListGrantIndexes: []int64{1, 1},
			fakeKube: &fakeKubeClient{
				updateBindingsError: fmt.Errorf("failed to update"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			run(t, tc)
		})
	}
}

type fakeWaiter struct {
	index      int
	resets     []int
	endAtIndex int
}

func (f *fakeWaiter) Reset() {
	f.resets = append(f.resets, f.index)
}

func (f *fakeWaiter) Wait(ctx context.Context) error {
	if f.index >= f.endAtIndex {
		return errDone
	}
	if ctx.Err() != nil {
		return errDone
	}
	f.index++
	return nil
}

var errDone = fmt.Errorf("done")

type fakeAPIClient struct {
	api.Client

	listGrantsResult  *api.ListResponse[api.Grant]
	listGrantsError   error
	listGrantsIndexes []int64

	users      map[uid.ID]api.User
	groupUsers map[uid.ID][]uid.ID // group ID -> user IDs
}

func (f *fakeAPIClient) ListGrants(ctx context.Context, req api.ListGrantsRequest) (*api.ListResponse[api.Grant], error) {
	f.listGrantsIndexes = append(f.listGrantsIndexes, req.LastUpdateIndex)
	return f.listGrantsResult, f.listGrantsError
}

func (f *fakeAPIClient) GetGroup(ctx context.Context, id uid.ID) (*api.Group, error) {
	return &api.Group{Name: "the-group"}, nil
}

func (f *fakeAPIClient) GetUser(ctx context.Context, id uid.ID) (*api.User, error) {
	if user, ok := f.users[id]; ok {
		return &user, nil
	}
	return &api.User{Name: "theuser@example.com"}, nil
}

// GetUsersInGroup returns the users in a group.
func (f *fakeAPIClient) GetUsersInGroup(ctx context.Context, id uid.ID) ([]uid.ID, error) {
	if users, ok := f.groupUsers[id]; ok {
		return users, nil
	}
	return []uid.ID{}, nil
}

type fakeKubeClient struct {
	kubernetes.Kubernetes
	updateBindingsError           error
	updateClusterRoleBindingsArgs []map[string][]rbacv1.Subject
	updateRoleBindingsArgs        []map[kubernetes.ClusterRoleNamespace][]rbacv1.Subject
}

func (f *fakeKubeClient) UpdateClusterRoleBindings(subjects map[string][]rbacv1.Subject) error {
	f.updateClusterRoleBindingsArgs = append(f.updateClusterRoleBindingsArgs, subjects)
	return f.updateBindingsError
}

func (f *fakeKubeClient) UpdateRoleBindings(subjects map[kubernetes.ClusterRoleNamespace][]rbacv1.Subject) error {
	f.updateRoleBindingsArgs = append(f.updateRoleBindingsArgs, subjects)
	return f.updateBindingsError
}

func TestSyncGrantsToDestination_GracePeriod(t *testing.T) {
	// appliedGrants captures the grants list passed to toDestination.
	type run struct {
		con             *connector
		appliedGrants   [][]api.Grant
		toDestinationFn func(ctx context.Context, grants []api.Grant) error
	}

	setupRun := func(opts Options) *run {
		r := &run{}
		r.con = &connector{
			client:      &fakeAPIClient{listGrantsError: fmt.Errorf("server unreachable")},
			destination: &api.Destination{Name: "dest"},
			options:     opts,
		}
		r.toDestinationFn = func(ctx context.Context, grants []api.Grant) error {
			r.appliedGrants = append(r.appliedGrants, grants)
			return nil
		}
		return r
	}

	t.Run("grace period not yet expired keeps grants unchanged", func(t *testing.T) {
		r := setupRun(Options{GrantSyncGracePeriod: time.Hour})
		// Simulate failures that started 30 min ago (within grace period)
		r.con.lastSuccessfulSync = time.Now().Add(-30 * time.Minute)

		waiter := &fakeWaiter{endAtIndex: 2}
		err := syncGrantsToDestination(context.Background(), r.con, waiter, r.toDestinationFn)
		assert.ErrorIs(t, err, errDone)

		// toDestination should not have been called (no fail-closed yet)
		assert.Equal(t, len(r.appliedGrants), 0)
	})

	t.Run("grace period expired removes all access", func(t *testing.T) {
		r := setupRun(Options{GrantSyncGracePeriod: time.Hour})
		// Simulate failures that started 2 hours ago (past grace period)
		r.con.lastSuccessfulSync = time.Now().Add(-2 * time.Hour)

		waiter := &fakeWaiter{endAtIndex: 5}
		err := syncGrantsToDestination(context.Background(), r.con, waiter, r.toDestinationFn)
		// Should return an error (not errDone) when grace period exceeded
		assert.Assert(t, !errors.Is(err, errDone), "expected grace period error, got errDone")
		assert.ErrorContains(t, err, "grace period")

		// toDestination must have been called once with empty grants
		assert.Equal(t, len(r.appliedGrants), 1)
		assert.Equal(t, len(r.appliedGrants[0]), 0, "expected empty grants on fail-closed")
	})

	t.Run("grace period disabled never removes access", func(t *testing.T) {
		r := setupRun(Options{GrantSyncGracePeriod: 0}) // disabled
		r.con.lastSuccessfulSync = time.Now().Add(-72 * time.Hour)

		waiter := &fakeWaiter{endAtIndex: 3}
		err := syncGrantsToDestination(context.Background(), r.con, waiter, r.toDestinationFn)
		assert.ErrorIs(t, err, errDone)

		// With grace period disabled, toDestination is never called on failure
		assert.Equal(t, len(r.appliedGrants), 0)
	})

	t.Run("recovery after failure resets grace period", func(t *testing.T) {
		// First simulate a successful sync to set lastSuccessfulSync
		opts := Options{GrantSyncGracePeriod: time.Hour}
		r := setupRun(opts)

		successClient := &fakeAPIClient{
			listGrantsResult: &api.ListResponse[api.Grant]{
				Items:           []api.Grant{{User: uid.ID(1), Resource: "dest", Privilege: "view"}},
				LastUpdateIndex: api.LastUpdateIndex{Index: 10},
			},
		}
		r.con.client = successClient

		waiter := &fakeWaiter{endAtIndex: 1}
		err := syncGrantsToDestination(context.Background(), r.con, waiter, r.toDestinationFn)
		assert.ErrorIs(t, err, errDone)

		// After recovery the grace period should be reset (lastSuccessfulSync updated)
		assert.Assert(t, !r.con.lastSuccessfulSync.IsZero())
		assert.Assert(t, time.Since(r.con.lastSuccessfulSync) < 5*time.Second,
			"lastSuccessfulSync should be recent after a successful sync")
	})
}
