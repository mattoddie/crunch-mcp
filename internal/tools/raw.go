package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	pathpkg "path"
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
	readOnlyPostRe = regexp.MustCompile(`^/banking/(bank-accounts|credit-cards)/opening-balances$`)
	// Anything that might send an email: invoice emails and payment reminders.
	emailPathRe = regexp.MustCompile(`email|remind`)
	// Records whose payments and attachments an update replaces.
	recordPathRe = regexp.MustCompile(`^/(invoices|expenses|expenses/recurring-expenses)/\d+$`)
)

const badPath = "path must start with /, must be in canonical form (no '.', '..' or empty segments), and must not contain a query string, '%', ';', '\\', spaces or control characters (use query for parameters)"

// cleanRawPath checks a raw path and returns it without the optional /v1 prefix, plus the
// canonical lower-case form the access checks match against. Only canonical paths are
// accepted, so a path can't be dressed up to slip past the checks while Crunch reads it as
// something else.
func cleanRawPath(p string) (string, string, error) {
	if rest, ok := strings.CutPrefix(p, "/v1/"); ok {
		p = "/" + rest
	}
	if !strings.HasPrefix(p, "/") {
		return "", "", errors.New(badPath)
	}
	for _, r := range p {
		if r <= ' ' || r >= 0x7f || strings.ContainsRune(`%;?#\`, r) {
			return "", "", errors.New(badPath)
		}
	}
	canon := p
	if len(canon) > 1 {
		canon = strings.TrimSuffix(canon, "/") // one trailing slash is allowed
	}
	if pathpkg.Clean(canon) != canon {
		return "", "", errors.New(badPath)
	}
	return p, strings.ToLower(canon), nil
}

func registerRaw(c *Context) {
	cl := c.Client
	desc := "Call any Crunch public API endpoint directly, for anything the other tools don't cover. `path` is relative to the API root and starts with the service: /banking, /clients, /company-details, /expenses, /immutable-data, /invoices or /user-details. "
	switch {
	case !c.AllowWrites:
		desc += "Only GET is allowed because write access is disabled (CRUNCH_ALLOW_WRITES=false)."
	case c.AllowDeletes:
		desc += "All HTTP methods are allowed."
	default:
		desc += "DELETE is not allowed because delete access is disabled (CRUNCH_ALLOW_DELETES=false), and a PUT must keep the record's existing payments and attachments."
	}
	if !c.AllowEmail {
		desc += " Emailing invoices and turning on payment reminders are blocked (CRUNCH_ALLOW_EMAIL=false)."
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
		p, canon, err := cleanRawPath(a.Path)
		if err != nil {
			return nil, err
		}
		switch {
		case method == "GET":
		case method == "POST" && readOnlyPostRe.MatchString(canon):
		case !c.AllowWrites:
			return nil, errors.New("write access is disabled. Set CRUNCH_ALLOW_WRITES=true to allow non-GET requests")
		case method == "DELETE" && !c.AllowDeletes:
			return nil, errors.New("delete access is disabled. Set CRUNCH_ALLOW_DELETES=true to allow DELETE requests")
		case method != "POST" && method != "PUT" && method != "DELETE":
			return nil, errors.New("method must be GET, POST, PUT or DELETE")
		}
		if !c.AllowEmail && emailPathRe.MatchString(canon) {
			return nil, errors.New("emailing invoices is disabled. Set CRUNCH_ALLOW_EMAIL=true to allow it")
		}
		if method == http.MethodPost || method == http.MethodPut {
			if err := c.checkRawBody(ctx, method, p, canon, a.Body); err != nil {
				return nil, err
			}
		}
		var out any
		return out, cl.Do(ctx, method, p, crunch.Query(a.Query), a.Body, &out)
	})
}

// checkRawBody applies the email and delete switches to a raw POST or PUT body.
func (c *Context) checkRawBody(ctx context.Context, method, p, canon string, body any) error {
	if method == http.MethodPost && readOnlyPostRe.MatchString(canon) {
		return nil
	}
	var current Obj
	fetched := false
	fetch := func() error {
		if fetched {
			return nil
		}
		fetched = true
		if err := c.Client.Get(ctx, strings.TrimSuffix(p, "/"), nil, &current); err != nil {
			return fmt.Errorf("could not fetch the current record to check this update against the access settings: %w", err)
		}
		return nil
	}
	if !c.AllowEmail && remindersOn(body) {
		// Leaving reminders on where they already are is fine; turning them on is not.
		if method != http.MethodPut || !recordPathRe.MatchString(canon) {
			return errors.New(remindersDisabled)
		}
		if err := fetch(); err != nil {
			return err
		}
		if !remindersOn(current) {
			return errors.New(remindersDisabled)
		}
	}
	if c.AllowDeletes || method != http.MethodPut {
		return nil
	}
	obj, isObj := body.(map[string]any)
	touchesLists := false
	for k := range obj {
		for _, name := range keptLists {
			if strings.EqualFold(k, name) {
				touchesLists = true
			}
		}
	}
	if !recordPathRe.MatchString(canon) && !touchesLists {
		return nil
	}
	if !isObj {
		return errors.New("the body of this PUT must be a JSON object")
	}
	if err := fetch(); err != nil {
		return err
	}
	return c.checkKeepsEntries(current, obj)
}
