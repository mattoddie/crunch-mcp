// Package httpserver serves MCP over Streamable HTTP, with bearer auth and a health endpoint.
package httpserver

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/config"
)

const maxBodyBytes = 16 << 20 // attachments arrive as base64 in tool arguments

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

// hostAllowed reports whether host (with or without a port) is on the allow-list.
func hostAllowed(allowed []string, host string) bool {
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	for _, a := range allowed {
		if a == host || a == hostname {
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
		MaxRequestBodyBytes: maxBodyBytes,
	})
	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if len(cfg.AllowedHosts) > 0 {
			host := strings.ToLower(r.Host)
			if !hostAllowed(cfg.AllowedHosts, host) {
				rpcError(w, http.StatusForbidden, "Host "+host+" is not allowed")
				return
			}
			// Browsers send Origin with cross-site requests: a page on another site must not reach the tools.
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host == "" || !hostAllowed(cfg.AllowedHosts, strings.ToLower(u.Host)) {
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
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
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

// Serve listens until the server is shut down.
func Serve(srv *http.Server, cfg config.HTTP) error {
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
