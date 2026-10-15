// Package crunch is a client for the Crunch Accounting public API.
package crunch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mattoddie/crunch-mcp/internal/config"
)

// Error is an API error with its HTTP status.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// Query holds query parameters; nil values are dropped and slices repeat the key.
type Query map[string]any

// Encode turns the query into a string, without the leading "?".
func (q Query) Encode() string {
	v := url.Values{}
	for k, val := range q {
		switch x := val.(type) {
		case nil:
		case []string:
			for _, s := range x {
				v.Add(k, s)
			}
		case []int64:
			for _, n := range x {
				v.Add(k, strconv.FormatInt(n, 10))
			}
		case *string:
			if x != nil {
				v.Add(k, *x)
			}
		case *int64:
			if x != nil {
				v.Add(k, strconv.FormatInt(*x, 10))
			}
		case *int:
			if x != nil {
				v.Add(k, strconv.Itoa(*x))
			}
		case *bool:
			if x != nil {
				v.Add(k, strconv.FormatBool(*x))
			}
		case string:
			if x != "" {
				v.Add(k, x)
			}
		case float64:
			v.Add(k, strconv.FormatFloat(x, 'f', -1, 64))
		case []any:
			for _, item := range x {
				if f, ok := item.(float64); ok {
					v.Add(k, strconv.FormatFloat(f, 'f', -1, 64))
				} else {
					v.Add(k, fmt.Sprint(item))
				}
			}
		default:
			v.Add(k, fmt.Sprint(x))
		}
	}
	return v.Encode()
}

// File is a downloaded PDF or attachment.
type File struct {
	Data        []byte
	ContentType string
	Filename    string // from Content-Disposition, when the API sends one
}

const maxRetries = 3

// Client calls the Crunch public REST API (https://public-api.crunch.co.uk/v1).
// Paths are relative to the API root and include the service, e.g. /expenses/vat-types.
// It sends the OAuth bearer token, refreshing it once on a 401, and retries when rate limited.
type Client struct {
	cfg  config.Crunch
	http *http.Client
	Auth *Auth
}

// New creates a client and loads any saved tokens.
func New(cfg config.Crunch) (*Client, error) {
	hc := &http.Client{Timeout: cfg.Timeout}
	auth, err := NewAuth(cfg, hc)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, http: hc, Auth: auth}, nil
}

// Get fetches JSON into out (which may be nil).
func (c *Client) Get(ctx context.Context, path string, q Query, out any) error {
	return c.Do(ctx, http.MethodGet, path, q, nil, out)
}

// Post sends body as JSON.
func (c *Client) Post(ctx context.Context, path string, q Query, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, q, body, out)
}

// Put sends body as JSON.
func (c *Client) Put(ctx context.Context, path string, q Query, body, out any) error {
	return c.Do(ctx, http.MethodPut, path, q, body, out)
}

// Delete sends a DELETE.
func (c *Client) Delete(ctx context.Context, path string, q Query, out any) error {
	return c.Do(ctx, http.MethodDelete, path, q, nil, out)
}

// Do makes a JSON request and decodes the response into out. Empty bodies leave out untouched;
// bodies that aren't JSON (some endpoints return a bare ID) are stored as a string when out is *any.
func (c *Client) Do(ctx context.Context, method, path string, q Query, body, out any) error {
	resp, err := c.send(ctx, method, path, q, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: reading the response: %w", method, path, err)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // keeps money and IDs exact when objects are sent back
	if err := dec.Decode(out); err != nil {
		if p, ok := out.(*any); ok {
			*p = strings.TrimSpace(string(data))
			return nil
		}
		return fmt.Errorf("%s %s: unexpected response: %w", method, path, err)
	}
	return nil
}

// GetFile downloads raw bytes (PDFs and attachments).
func (c *Client) GetFile(ctx context.Context, path string, q Query) (*File, error) {
	resp, err := c.send(ctx, http.MethodGet, path, q, nil, "*/*")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("GET %s: reading the file: %w", path, err)
	}
	f := &File{Data: data, ContentType: "application/octet-stream"}
	if ct, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil {
		f.ContentType = ct
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		f.Filename = params["filename"]
	}
	return f, nil
}

func (c *Client) send(ctx context.Context, method, path string, q Query, body any, accept string) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := c.cfg.APIURL + path
	if qs := q.Encode(); qs != "" {
		u += "?" + qs
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	refreshed := false
	for attempt := 0; ; attempt++ {
		token, err := c.Auth.AccessToken(ctx)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", accept)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s %s: could not reach the Crunch API: %w", method, path, err)
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}
		if resp.StatusCode == http.StatusUnauthorized && !refreshed {
			resp.Body.Close()
			refreshed = true
			if _, err := c.Auth.Refresh(ctx, token); err != nil {
				return nil, err
			}
			continue
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && attempt < maxRetries {
			resp.Body.Close()
			delay := time.Duration(250<<attempt) * time.Millisecond
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
				delay = min(time.Duration(s)*time.Second, 10*time.Second)
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, &Error{
			Status:  resp.StatusCode,
			Message: fmt.Sprintf("%s %s failed (HTTP %d): %s.%s", method, path, resp.StatusCode, errorMessage(resp.StatusCode, data), hint(resp.StatusCode, method)),
		}
	}
}

// errorMessage pulls a readable message out of a Crunch (Spring) error body.
func errorMessage(status int, data []byte) string {
	var j map[string]any
	if json.Unmarshal(data, &j) != nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return truncate(s, 500)
		}
		return http.StatusText(status)
	}
	var parts []string
	add := func(s string) {
		for _, p := range parts {
			if p == s {
				return
			}
		}
		parts = append(parts, s)
	}
	for _, k := range []string{"message", "error_description", "detail", "title", "error"} {
		if s, ok := j[k].(string); ok && s != "" {
			add(s)
		}
	}
	for _, k := range []string{"errors", "fieldErrors", "violations"} {
		list, _ := j[k].([]any)
		for i, e := range list {
			if i == 10 {
				break
			}
			switch x := e.(type) {
			case string:
				add(x)
			case map[string]any:
				field := firstString(x, "field", "propertyPath", "objectName")
				msg := firstString(x, "defaultMessage", "message", "reason")
				if field != "" {
					add(field + ": " + msg)
				} else if msg != "" {
					add(msg)
				}
			}
		}
	}
	if len(parts) == 0 {
		return truncate(string(data), 500)
	}
	return strings.Join(parts, "; ")
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func hint(status int, method string) string {
	switch {
	case status == http.StatusUnauthorized:
		return " The access token was rejected. Log in again with `crunch-mcp login` or the crunch_auth_start tool."
	case status == http.StatusForbidden:
		scope := map[string]string{"GET": "read:all", "POST": "create:all", "PUT": "update:all", "DELETE": "delete:all"}[method]
		return fmt.Sprintf(" Crunch refused this request. The login may not include the %s scope (log in again after enabling the matching CRUNCH_ALLOW_* setting), or the endpoint isn't part of the public API.", scope)
	case status == http.StatusInternalServerError && method != http.MethodGet:
		return " Crunch often returns 500 when a required field is missing or a value (VAT type, expense type, payment method) isn't valid; check them with the reference tools."
	}
	return ""
}
