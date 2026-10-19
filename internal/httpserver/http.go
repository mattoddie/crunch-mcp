// Package httpserver serves MCP over Streamable HTTP, with bearer auth, a
// Host/Origin allow-list and a health endpoint.
package httpserver

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/config"
)

// MaxBodyBytes is the largest request body accepted on /mcp: attachments arrive as base64
// in tool arguments, and a 10 MB file is about 13.4 MB encoded.
const MaxBodyBytes = 16 << 20

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func rpcError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32000, "message": message}, "id": nil})
}

func tokenMatches(header, token string) bool {
	presented, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		presented, ok = strings.CutPrefix(header, "bearer ")
	}
	return ok && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presented)), []byte(token)) == 1
}

// splitHostPort splits "host", "host:port", "[v6]" or "[v6]:port" into a lower-case
// host (without brackets) and a port, which is empty when there isn't one.
func splitHostPort(s string) (string, string) {
	s = strings.ToLower(s)
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, p
	}
	return strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"), ""
}

// hostAllowed reports whether a Host header is on the allow-list. An entry without a port
// matches any port, because the port the server sees varies behind Docker and proxies.
func hostAllowed(allowed []string, hostHeader string) bool {
	h, p := splitHostPort(hostHeader)
	for _, a := range allowed {
		ah, ap := splitHostPort(a)
		if ah == h && (ap == "" || ap == p) {
			return true
		}
	}
	return false
}

// originAllowed reports whether an Origin header is on the allow-list. An entry without a
// port matches only the scheme's default port (443 or 80); a page on another port must be
// listed as host:port. "null", empty and malformed origins are refused.
func originAllowed(allowed []string, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" || u.User != nil {
		return false
	}
	defaultPort := map[string]string{"http": "80", "https": "443"}[u.Scheme]
	h, p := strings.ToLower(u.Hostname()), u.Port()
	if p == "" {
		p = defaultPort
	}
	for _, a := range allowed {
		ah, ap := splitHostPort(a)
		if ah == h && (ap == p || (ap == "" && p == defaultPort)) {
			return true
		}
	}
	return false
}

// Handler serves /mcp (stateless Streamable HTTP, so the container can be restarted or scaled freely) and /health.
func Handler(cfg config.HTTP, server *mcp.Server) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		DisableLocalhostProtection: len(cfg.AllowedHosts) > 0, // MCP_ALLOWED_HOSTS replaces the built-in localhost check
		// The SDK applies its own 4 MiB limit unless told otherwise, which would cut off 10 MB attachments.
		MaxRequestBodyBytes: MaxBodyBytes,
	})
	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if len(cfg.AllowedHosts) > 0 {
			if !hostAllowed(cfg.AllowedHosts, r.Host) {
				rpcError(w, http.StatusForbidden, "Host "+strings.ToLower(r.Host)+" is not allowed")
				return
			}
			// Browsers send Origin with cross-site requests: a page on another site must not
			// reach the tools. A header that is present but empty counts as present.
			if origins, ok := r.Header["Origin"]; ok {
				origin := ""
				if len(origins) > 0 {
					origin = origins[0]
				}
				if len(origins) != 1 || !originAllowed(cfg.AllowedHosts, origin) {
					rpcError(w, http.StatusForbidden, "Origin "+origin+" is not allowed")
					return
				}
			}
		}
		if cfg.AuthToken != "" && !tokenMatches(r.Header.Get("Authorization"), cfg.AuthToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			rpcError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		if r.Method != http.MethodPost {
			// Stateless mode: no server-initiated SSE stream and no sessions to delete.
			w.Header().Set("Allow", "POST")
			rpcError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				rpcError(w, http.StatusRequestEntityTooLarge, "Request body too large")
				return
			}
			rpcError(w, http.StatusBadRequest, "Could not read the request body")
			return
		}
		if !json.Valid(body) {
			rpcError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		mcpHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found. The MCP endpoint is /mcp"})
	})
	return mux
}

// Addr is the listen address.
func Addr(cfg config.HTTP) string {
	return net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
}

// NewServer returns the HTTP server for the handler. There is deliberately no
// WriteTimeout: a tool call can legitimately take minutes.
func NewServer(cfg config.HTTP, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              Addr(cfg),
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// Serve listens until the server is shut down.
func Serve(srv *http.Server, cfg config.HTTP) error {
	if cfg.AuthToken == "" {
		log.Printf("crunch-mcp: WARNING: serving without bearer auth (MCP_ALLOW_UNAUTHENTICATED=true); only Host names in MCP_ALLOWED_HOSTS are accepted, so whatever is in front of this server must authenticate every request")
	}
	auth := " (WITHOUT bearer auth: MCP_ALLOW_UNAUTHENTICATED=true)"
	if cfg.AuthToken != "" {
		auth = " (bearer auth enabled)"
	}
	log.Printf("crunch-mcp listening on http://%s/mcp%s", srv.Addr, auth)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
