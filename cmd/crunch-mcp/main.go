// Command crunch-mcp is a Model Context Protocol server for Crunch Accounting.
//
//	crunch-mcp              serve MCP (stdio, or Streamable HTTP with MCP_TRANSPORT=http)
//	crunch-mcp login        log in to Crunch in a browser and save the tokens
//	crunch-mcp status       show the login status
//	crunch-mcp healthcheck  check the HTTP server is up (for Docker HEALTHCHECK)
//	crunch-mcp version      print the version
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch"
	"github.com/mattoddie/crunch-mcp/internal/httpserver"
	"github.com/mattoddie/crunch-mcp/internal/tools"
)

func main() {
	// Logs go to stderr: stdout is the MCP channel in stdio mode.
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "login":
		err = login()
	case "status":
		err = status()
	case "healthcheck":
		err = healthcheck()
	case "version", "--version", "-v":
		fmt.Println(tools.Version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	if err != nil {
		log.Printf("crunch-mcp: %v", err)
		os.Exit(1)
	}
}

const usage = `Usage: crunch-mcp [command]

Commands:
  serve        Serve MCP (default). stdio, or Streamable HTTP with MCP_TRANSPORT=http
  login        Log in to Crunch in a browser and save the tokens to CRUNCH_TOKEN_FILE
  status       Show whether the server is logged in
  healthcheck  Exit 0 if the HTTP server answers /health (for Docker HEALTHCHECK)
  version      Print the version

Configuration is by environment variable; see https://crunch-mcp.mattoddie.dev/docs/configuration/
`

func load() (*config.Config, *crunch.Client, error) {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return nil, nil, err
	}
	client, err := crunch.New(cfg.Crunch)
	if err != nil {
		return nil, nil, err
	}
	return cfg, client, nil
}

func serve() error {
	cfg, client, err := load()
	if err != nil {
		return err
	}
	t := cfg.Tools
	writes := "disabled"
	if t.AllowWrites {
		writes = "ENABLED"
		if t.AllowDeletes {
			writes += " (with deletes)"
		}
		if t.AllowEmail {
			writes += " (with email)"
		}
	}
	login := "not logged in (use `crunch-mcp login` or the crunch_auth_start tool)"
	if client.Auth.HasCredentials() {
		login = "token file " + cfg.Crunch.TokenFile
		if cfg.Crunch.AccessToken != "" {
			login = "CRUNCH_ACCESS_TOKEN"
		}
	}
	log.Printf("crunch-mcp %s: %s, auth %s, writes %s, toolsets %d, transport %s", tools.Version, cfg.Crunch.APIURL, login, writes, len(t.Toolsets), cfg.Transport)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := tools.NewServer(client, cfg.Tools, nil)
	if cfg.Transport == "stdio" {
		err := server.Run(ctx, &mcp.StdioTransport{})
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	srv := &http.Server{Addr: httpserver.Addr(cfg.HTTP), Handler: httpserver.Handler(cfg.HTTP, server), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	return httpserver.Serve(srv, cfg.HTTP)
}

// login runs the OAuth flow from a terminal. The browser is sent back to
// CRUNCH_REDIRECT_URI; when that is a loopback address, a one-off listener
// catches the code. The user can also paste the URL from the browser's
// address bar, which works when the browser can't reach this machine.
func login() error {
	cfg, client, err := load()
	if err != nil {
		return err
	}
	start, err := client.Auth.StartLogin()
	if err != nil {
		return err
	}

	results := make(chan string, 2)
	redirect, _ := url.Parse(cfg.Crunch.RedirectURI)
	var srv *http.Server
	if host := redirect.Hostname(); host == "127.0.0.1" || host == "localhost" || host == "::1" {
		listen := os.Getenv("CRUNCH_LOGIN_LISTEN")
		if listen == "" {
			listen = net.JoinHostPort(host, redirect.Port())
		}
		mux := http.NewServeMux()
		mux.HandleFunc(redirect.Path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.URL.Query().Get("code") == "" && r.URL.Query().Get("error") == "" {
				http.Error(w, "No authorisation code in this request.", http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>crunch-mcp</title><body style="font-family:system-ui;padding:3rem;max-width:36rem"><h1>Thanks!</h1><p>crunch-mcp received the authorisation. You can close this tab and go back to the terminal.</p>`)
			results <- r.URL.String()
		})
		srv = &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		ln, err := net.Listen("tcp", listen)
		if err != nil {
			log.Printf("Could not listen on %s for the redirect (%v); paste the URL instead.", listen, err)
		} else {
			go func() { _ = srv.Serve(ln) }()
			defer srv.Close()
		}
	}

	fmt.Fprintf(os.Stderr, "\nOpen this URL in a browser, log in to Crunch and click Allow:\n\n  %s\n\n", start.URL)
	fmt.Fprintf(os.Stderr, "Scopes: %s\nWaiting for the redirect to %s ...\n", start.Scopes, cfg.Crunch.RedirectURI)
	fmt.Fprintf(os.Stderr, "If the browser shows an error page instead, copy the full URL from its address bar and paste it here:\n> ")
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				results <- line
				return
			}
		}
	}()

	var redirected string
	select {
	case redirected = <-results:
	case <-time.After(15 * time.Minute):
		return errors.New("timed out waiting for the login")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := client.Auth.CompleteLogin(ctx, redirected)
	if err != nil {
		return err
	}
	var user map[string]any
	if client.Get(ctx, "/user-details", nil, &user) == nil {
		fmt.Fprintf(os.Stderr, "\nLogged in to Crunch as %v %v (%v).\n", user["firstName"], user["lastName"], user["email"])
	} else {
		fmt.Fprintf(os.Stderr, "\nLogged in to Crunch.\n")
	}
	fmt.Fprintf(os.Stderr, "Tokens saved to %v (scope: %v).\n", res["token_file"], res["scope"])
	if w, ok := res["warning"]; ok {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", w)
	}
	if hr, _ := res["has_refresh_token"].(bool); !hr {
		fmt.Fprintln(os.Stderr, "Warning: no refresh token was issued (is offline_access in CRUNCH_SCOPES?). You'll need to log in again within the hour.")
	}
	return nil
}

func status() error {
	_, client, err := load()
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(client.Auth.Status(), "", "  ")
	fmt.Println(string(b))
	return nil
}

func healthcheck() error {
	if os.Getenv("MCP_TRANSPORT") == "stdio" {
		return nil
	}
	port := os.Getenv("MCP_HTTP_PORT")
	if port == "" {
		port = "8080"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %s", resp.Status)
	}
	return nil
}
