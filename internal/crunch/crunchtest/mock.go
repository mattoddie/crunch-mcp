// Package crunchtest is a fake Crunch API and OAuth server for tests.
package crunchtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/mattoddie/crunch-mcp/internal/config"
)

// Request is a recorded API request.
type Request struct {
	Method string
	// Path is relative to /v1, e.g. /expenses/123.
	Path   string
	Query  url.Values
	Body   any // decoded JSON, or nil
	Header http.Header
}

// Handler answers a matched API request with a status and a JSON body (or []byte for raw content).
type Handler func(r Request, match []string) (int, any)

type route struct {
	method  string
	pattern *regexp.Regexp
	fn      Handler
}

// Mock serves the API under /v1 and OAuth under /oauth. Every API request
// needs a valid bearer token; unmatched GETs return 404, unmatched writes
// echo their body back with status 200.
type Mock struct {
	Server *httptest.Server

	mu       sync.Mutex
	requests []Request
	routes   []route
	access   map[string]bool
	refresh  map[string]bool
	codes    map[string]string // code -> PKCE challenge
	counter  int
	// TokenRequests counts calls to /oauth/token.
	TokenRequests int
	// TokenForms records the form of each /oauth/token call.
	TokenForms []url.Values
	// TokenBasicAuth records the Basic auth user of each /oauth/token call ("" if none).
	TokenBasicAuth []string
	// ExpiresIn is the lifetime the mock gives new access tokens (default 3600).
	ExpiresIn int
}

// AccessToken and RefreshToken are valid from the start.
const (
	AccessToken  = "access-0"
	RefreshToken = "refresh-0"
	ClientID     = "test-client"
)

// New starts a mock server. Close it with m.Server.Close().
func New() *Mock {
	m := &Mock{
		access:    map[string]bool{AccessToken: true},
		refresh:   map[string]bool{RefreshToken: true},
		codes:     map[string]string{},
		ExpiresIn: 3600,
	}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	return m
}

// Config returns Crunch settings pointing at the mock.
func (m *Mock) Config(tokenFile string) config.Crunch {
	c := config.Crunch{
		APIURL:      m.Server.URL + "/v1",
		AuthURL:     m.Server.URL + "/oauth",
		ClientID:    ClientID,
		RedirectURI: "http://127.0.0.1:8976/callback",
		Scopes:      config.DefaultScopes(true, true),
		TokenFile:   tokenFile,
	}
	c.Timeout = 5e9
	return c
}

// On adds a route; pattern is a regular expression matched against the whole path (relative to /v1).
func (m *Mock) On(method, pattern string, fn Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Later routes win, so tests can override earlier ones.
	m.routes = append([]route{{method, regexp.MustCompile("^" + pattern + "$"), fn}}, m.routes...)
}

// JSON is a handler that always returns v with status 200.
func JSON(v any) Handler {
	return func(Request, []string) (int, any) { return 200, v }
}

// Reset forgets routes and recorded requests.
func (m *Mock) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes, m.requests = nil, nil
}

// Requests returns the recorded API requests.
func (m *Mock) Requests() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Request(nil), m.requests...)
}

// Last returns the last request with this method (any method if ""), or nil.
func (m *Mock) Last(method string) *Request {
	reqs := m.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if method == "" || reqs[i].Method == method {
			return &reqs[i]
		}
	}
	return nil
}

// IssueCode simulates the user approving a login whose authorize URL carried this PKCE challenge.
func (m *Mock) IssueCode(challenge string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter++
	code := fmt.Sprintf("code-%d", m.counter)
	m.codes[code] = challenge
	return code
}

// RevokeAccess makes all current access tokens invalid (as if they expired).
func (m *Mock) RevokeAccess() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.access = map[string]bool{}
}

func (m *Mock) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/oauth/token":
		m.token(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/"):
		m.api(w, r)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *Mock) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TokenRequests++
	m.TokenForms = append(m.TokenForms, r.PostForm)
	user, _, _ := r.BasicAuth()
	m.TokenBasicAuth = append(m.TokenBasicAuth, user)
	if user == "" && r.PostForm.Get("client_id") != ClientID {
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "refresh_token":
		rt := r.PostForm.Get("refresh_token")
		if !m.refresh[rt] {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "Invalid refresh token"})
			return
		}
		delete(m.refresh, rt) // rotation: each refresh token works once
	case "authorization_code":
		code := r.PostForm.Get("code")
		challenge, ok := m.codes[code]
		delete(m.codes, code)
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "Invalid code or verifier"})
			return
		}
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	m.counter++
	at, rt := fmt.Sprintf("access-%d", m.counter), fmt.Sprintf("refresh-%d", m.counter)
	m.access[at], m.refresh[rt] = true, true
	writeJSON(w, 200, map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": m.ExpiresIn, "refresh_token": rt, "scope": "read:all offline_access"})
}

func (m *Mock) api(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	m.mu.Lock()
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	authorised := m.access[token]
	m.mu.Unlock()
	if !authorised {
		writeJSON(w, 401, map[string]string{"error": "invalid_token"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	req := Request{Method: r.Method, Path: path, Query: r.URL.Query(), Header: r.Header.Clone()}
	if len(bytes.TrimSpace(body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		_ = dec.Decode(&req.Body)
	}
	m.mu.Lock()
	m.requests = append(m.requests, req)
	routes := append([]route(nil), m.routes...)
	m.mu.Unlock()

	for _, rt := range routes {
		if rt.method != r.Method {
			continue
		}
		if match := rt.pattern.FindStringSubmatch(path); match != nil {
			status, out := rt.fn(req, match)
			if b, ok := out.([]byte); ok {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.WriteHeader(status)
				_, _ = w.Write(b)
				return
			}
			if out == nil {
				w.WriteHeader(status)
				return
			}
			writeJSON(w, status, out)
			return
		}
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 404, map[string]string{"message": "Not found: " + path})
	case http.MethodDelete:
		w.WriteHeader(200)
	default:
		if obj, ok := req.Body.(map[string]any); ok {
			echo := map[string]any{"id": json.Number("9001")}
			for k, v := range obj {
				echo[k] = v
			}
			writeJSON(w, 200, echo)
			return
		}
		w.WriteHeader(200)
	}
}
