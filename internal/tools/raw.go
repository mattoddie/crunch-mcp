package tools

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

type rawRequestArgs struct {
	Method string         `json:"method,omitempty" jsonschema:"HTTP method: GET (default), POST, PUT or DELETE"`
	Path   string         `json:"path" jsonschema:"Path relative to https://public-api.crunch.co.uk/v1, starting with the service, for example /expenses/vat-types"`
	Query  map[string]any `json:"query,omitempty" jsonschema:"Query string parameters"`
	Body   any            `json:"body,omitempty" jsonschema:"JSON request body for POST and PUT"`
}

var (
	// POSTs that only read data.
	readOnlyPostRe = regexp.MustCompile(`^/banking/(bank-accounts|credit-cards)/opening-balances/?$`)
	emailPathRe    = regexp.MustCompile(`^/invoices/[^/]+/email/?$`)
)

func registerRaw(c *Context) {
	cl := c.Client
	desc := "Call any Crunch public API endpoint directly, for anything the other tools don't cover. `path` is relative to the API root and starts with the service: /banking, /clients, /company-details, /expenses, /immutable-data, /invoices or /user-details. "
	switch {
	case !c.AllowWrites:
		desc += "Only GET is allowed because write access is disabled (CRUNCH_ALLOW_WRITES=false)."
	case c.AllowDeletes:
		desc += "All HTTP methods are allowed."
	default:
		desc += "DELETE is not allowed because delete access is disabled (CRUNCH_ALLOW_DELETES=false)."
	}
	if !c.AllowEmail {
		desc += " Emailing invoices is blocked (CRUNCH_ALLOW_EMAIL=false)."
	}
	add(c, Def{
		Toolset: "raw", Name: "crunch_api_request", Title: "Raw Crunch API request",
		Description: desc,
		ReadOnly:    boolPtr(!c.AllowWrites), Destructive: boolPtr(c.AllowWrites),
		Enums: map[string][]string{"method": {"GET", "POST", "PUT", "DELETE"}},
	}, func(ctx context.Context, a rawRequestArgs) (any, error) {
		method := strings.ToUpper(a.Method)
		if method == "" {
			method = "GET"
		}
		p, err := url.PathUnescape(a.Path)
		if err != nil {
			return nil, errors.New("path is not validly percent-encoded")
		}
		p = strings.TrimPrefix(p, "/v1")
		if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.ContainsAny(p, "?#\\") || strings.Contains(p, "//") || strings.Contains(p, "/./") {
			return nil, errors.New("path must start with /, must not contain '..', './' or '//' segments, and must not include a query string (use query)")
		}
		switch {
		case method == "GET":
		case method == "POST" && readOnlyPostRe.MatchString(p):
		case !c.AllowWrites:
			return nil, errors.New("write access is disabled. Set CRUNCH_ALLOW_WRITES=true to allow non-GET requests")
		case method == "DELETE" && !c.AllowDeletes:
			return nil, errors.New("delete access is disabled. Set CRUNCH_ALLOW_DELETES=true to allow DELETE requests")
		case method != "POST" && method != "PUT" && method != "DELETE":
			return nil, errors.New("method must be GET, POST, PUT or DELETE")
		}
		if emailPathRe.MatchString(p) && !c.AllowEmail {
			return nil, errors.New("emailing invoices is disabled. Set CRUNCH_ALLOW_EMAIL=true to allow it")
		}
		var out any
		return out, cl.Do(ctx, method, p, crunch.Query(a.Query), a.Body, &out)
	})
}
