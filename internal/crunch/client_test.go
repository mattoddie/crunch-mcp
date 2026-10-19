package crunch_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch"
	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

func staticClient(t *testing.T, m *crunchtest.Mock) *crunch.Client {
	t.Helper()
	c, _ := newClient(t, m, func(c *config.Crunch) { c.AccessToken = crunchtest.AccessToken })
	return c
}

func TestRetriesWhenRateLimited(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	calls := 0
	m.On("GET", "/expenses/vat-types", func(crunchtest.Request, []string) (int, any) {
		calls++
		if calls < 3 {
			return 429, map[string]string{"message": "slow down"}
		}
		return 200, map[string]any{"vatTypes": []any{}}
	})
	if err := staticClient(t, m).Get(context.Background(), "/expenses/vat-types", nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("calls: %d", calls)
	}
}

func TestErrorMessages(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("POST", "/invoices", crunchtest.Handler(func(crunchtest.Request, []string) (int, any) {
		return 400, map[string]any{"message": "Validation failed", "errors": []any{map[string]any{"field": "customer", "defaultMessage": "must not be null"}}}
	}))
	m.On("DELETE", "/expenses/1", func(crunchtest.Request, []string) (int, any) { return 403, map[string]string{"error": "Forbidden"} })
	c := staticClient(t, m)
	err := c.Post(context.Background(), "/invoices", nil, map[string]any{}, nil)
	var apiErr *crunch.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || !strings.Contains(err.Error(), "customer: must not be null") {
		t.Errorf("400: %v", err)
	}
	err = c.Delete(context.Background(), "/expenses/1", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "delete:all") {
		t.Errorf("403 hint: %v", err)
	}
	err = c.Get(context.Background(), "/nope", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("404: %v", err)
	}
}

func TestQueryEncoding(t *testing.T) {
	id := int64(42)
	yes := true
	q := crunch.Query{"a": "", "b": nil, "c": &id, "d": &yes, "e": []any{1.0, "x"}, "f": 1500000.0, "g": (*int64)(nil)}
	if got := q.Encode(); got != "c=42&d=true&e=1&e=x&f=1500000" {
		t.Errorf("got %q", got)
	}
}

func TestBareValueResponse(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	m.On("DELETE", "/invoices/5", func(crunchtest.Request, []string) (int, any) { return 200, []byte("5") })
	m.On("GET", "/x", func(crunchtest.Request, []string) (int, any) { return 200, []byte("not json") })
	var out any
	c := staticClient(t, m)
	if err := c.Delete(context.Background(), "/invoices/5", nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), "/x", nil, &out); err != nil || out != "not json" {
		t.Errorf("got %v %v", out, err)
	}
}

func TestPostsAreNotRetriedOn503(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	posts, gets := 0, 0
	m.On("POST", "/expenses", func(crunchtest.Request, []string) (int, any) {
		posts++
		return 503, map[string]string{"message": "busy"}
	})
	m.On("GET", "/expenses/1", func(crunchtest.Request, []string) (int, any) {
		gets++
		if gets < 2 {
			return 503, map[string]string{"message": "busy"}
		}
		return 200, map[string]any{}
	})
	c := staticClient(t, m)
	if err := c.Post(context.Background(), "/expenses", nil, map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("POST: %v", err)
	}
	if posts != 1 {
		t.Errorf("POST sent %d times, want 1 (it may already have created the expense)", posts)
	}
	if err := c.Get(context.Background(), "/expenses/1", nil, nil); err != nil || gets != 2 {
		t.Errorf("GET retried: %v, %d calls", err, gets)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	followed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed = true
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	c, err := crunch.New(config.Crunch{APIURL: srv.URL + "/v1", AuthURL: srv.URL, AccessToken: "x", TokenFile: t.TempDir() + "/t.json", Timeout: 5e9})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Get(context.Background(), "/user-details", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") || followed {
		t.Errorf("redirect: %v (followed %v)", err, followed)
	}
}

func TestOversizedResponsesFail(t *testing.T) {
	m := crunchtest.New()
	defer m.Server.Close()
	big := make([]byte, 64<<20+1)
	m.On("GET", "/big", func(crunchtest.Request, []string) (int, any) { return 200, big })
	c := staticClient(t, m)
	if err := c.Get(context.Background(), "/big", nil, new(any)); err == nil || !strings.Contains(err.Error(), "larger than 64 MiB") {
		t.Errorf("Get: %v", err)
	}
	if _, err := c.GetFile(context.Background(), "/big", nil); err == nil || !strings.Contains(err.Error(), "larger than 64 MiB") {
		t.Errorf("GetFile: %v", err)
	}
}
