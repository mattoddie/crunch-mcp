package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	pathpkg "path"
	"regexp"
	"strconv"
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
	// Anything that might send an email: invoice emails and payment reminders.
	emailPathRe = regexp.MustCompile(`email|remind`)
	// Records whose payments and attachments an update replaces.
	recordPathRe = regexp.MustCompile(`^/(invoices|expenses|expenses/recurring-expenses)/\d+$`)
)

const badPath = "path must start with /, must be in canonical form (no '.', '..' or empty segments), and each segment may only contain letters, digits, '-' and '_', with a segment that starts with a digit being a plain number such as 123 (use query for parameters)"

var (
	// A path segment: a name, or a record id.
	segmentRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	digitsRe  = regexp.MustCompile(`^\d+$`)
)

// rawWrites are the requests that change something which the raw tool may send: every
// write endpoint of Crunch's documented public API (canonical, lower-case paths; {id} is
// a number). Anything else is refused, so an endpoint the access checks don't know can't
// be used to get round them; Crunch refuses undocumented endpoints anyway.
var rawWrites = map[string]*regexp.Regexp{
	http.MethodPost: regexp.MustCompile(`^/(invoices|invoices/\d+/(issue|email)|expenses|expenses/recurring-expenses|expenses/suppliers|clients|banking/bank-accounts|banking/credit-cards|banking/credit-cards/postopeningbalance/\d+)$`),
	http.MethodPut: regexp.MustCompile(`^/(invoices/\d+|expenses/\d+|expenses/recurring-expenses/\d+|expenses/suppliers/\d+|clients/\d+|` +
		`banking/(bank-accounts|credit-cards)/((default|archive)/)?\d+|banking/credit-cards/updateopeningbalance/\d+)$`),
	http.MethodDelete: regexp.MustCompile(`^/(invoices|expenses|expenses/recurring-expenses|expenses/suppliers|clients|banking/bank-accounts|banking/credit-cards)/\d+$`),
}

// rawCreateRe matches the POSTs that create a record, whose body must not name an existing one.
var rawCreateRe = regexp.MustCompile(`^/(invoices|expenses|expenses/recurring-expenses|expenses/suppliers|clients|banking/bank-accounts|banking/credit-cards)$`)

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
	// Spring reads ids leniently ("+1", "0x1", "1.json" can all mean record 1), so only
	// plain segments are accepted and anything that starts like a number must be one:
	// the checks below then see the same record Crunch does.
	if canon != "/" {
		for _, seg := range strings.Split(canon[1:], "/") {
			if !segmentRe.MatchString(seg) || (seg[0] >= '0' && seg[0] <= '9' && !digitsRe.MatchString(seg)) {
				return "", "", errors.New(badPath)
			}
		}
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
	if c.AllowWrites {
		desc += " Writes only go to the write endpoints of Crunch's documented public API (such as POST /invoices or PUT /expenses/{id}), and a POST that creates a record must not include an id."
	}
	if !c.AllowEmail {
		desc += " Emailing invoices and turning on payment reminders are blocked (CRUNCH_ALLOW_EMAIL=false), including query parameters and body flags that might send email."
	}
	add(c, Def{
		Toolset: "raw", Name: "crunch_api_request", Title: "Raw Crunch API request",
		Description: desc,
		ReadOnly:    boolPtr(!c.AllowWrites), Destructive: boolPtr(c.AllowWrites),
		Enums:  map[string][]string{"method": {"GET", "POST", "PUT", "DELETE"}},
		Writes: c.AllowWrites, Reads: rawReads,
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
		case method == "POST" && crunch.IsReadOnlyPost(canon):
		case !c.AllowWrites:
			return nil, errors.New("write access is disabled. Set CRUNCH_ALLOW_WRITES=true to allow non-GET requests")
		case method == "DELETE" && !c.AllowDeletes:
			return nil, errors.New("delete access is disabled. Set CRUNCH_ALLOW_DELETES=true to allow DELETE requests")
		case method != "POST" && method != "PUT" && method != "DELETE":
			return nil, errors.New("method must be GET, POST, PUT or DELETE")
		case !rawWrites[method].MatchString(canon):
			return nil, fmt.Errorf("%s %s is not one of the write endpoints of Crunch's public API that this tool knows, so it is refused. Use GET to read, or the dedicated tools to change records", method, p)
		}
		if !c.AllowEmail && emailPathRe.MatchString(canon) {
			return nil, errors.New("emailing invoices is disabled. Set CRUNCH_ALLOW_EMAIL=true to allow it")
		}
		if !c.AllowEmail && method != http.MethodGet {
			if err := checkRawEmail(a.Query, "query"); err != nil {
				return nil, err
			}
		}
		if method == http.MethodPut {
			if err := checkRawBodyID(canon, a.Body); err != nil {
				return nil, err
			}
		}
		if method == http.MethodPost && rawCreateRe.MatchString(canon) {
			if err := checkNoID(a.Body, "body"); err != nil {
				return nil, err
			}
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
	if method == http.MethodPost && crunch.IsReadOnlyPost(canon) {
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
	if !c.AllowEmail {
		if k := sendFlagOn(body); k != "" {
			return fmt.Errorf("the body sets %q, which might send an email; emailing is disabled. Set CRUNCH_ALLOW_EMAIL=true to allow it", k)
		}
	}
	if c.AllowDeletes {
		return nil
	}
	onRecord := recordPathRe.MatchString(canon)
	if method == http.MethodPost && !onRecord {
		return nil // creating a record can't drop existing entries
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
	if !onRecord && !touchesLists {
		return nil
	}
	if !isObj {
		return fmt.Errorf("the body of this %s must be a JSON object", method)
	}
	if err := fetch(); err != nil {
		return err
	}
	return c.checkKeepsEntries(current, obj)
}

var trailingIDRe = regexp.MustCompile(`/(\d+)$`)

// checkRawBodyID refuses a raw PUT whose body names a different record from its path: the
// access checks fetch and compare the record at the path, so Crunch must update that one.
func checkRawBodyID(canon string, body any) error {
	m := trailingIDRe.FindStringSubmatch(canon)
	obj, ok := body.(map[string]any)
	if m == nil || !ok {
		return nil
	}
	for k, v := range obj {
		if !strings.EqualFold(k, "id") || v == nil {
			continue
		}
		s := fmt.Sprint(v)
		if f, ok := v.(float64); ok {
			s = strconv.FormatFloat(f, 'f', -1, 64)
		}
		if s != m[1] {
			return fmt.Errorf("the body's %s (%v) doesn't match the record in the path (%s); a PUT may only update the record it names", k, v, m[1])
		}
	}
	return nil
}

// checkRawEmail refuses query parameters or body fields of a raw write that could make
// Crunch send an email: turning on payment reminders, a parameter named like email or
// reminders, or a flag such as sendEmail set to true.
func checkRawEmail(v any, where string) error {
	if remindersOn(v) {
		return errors.New(remindersDisabled)
	}
	if q, ok := v.(map[string]any); ok && where == "query" {
		for k := range q {
			if emailPathRe.MatchString(strings.ToLower(k)) {
				return fmt.Errorf("the query parameter %q might send an email, which is disabled. Set CRUNCH_ALLOW_EMAIL=true to allow it", k)
			}
		}
	}
	if k := sendFlagOn(v); k != "" {
		return fmt.Errorf("the %s sets %q, which might send an email; emailing is disabled. Set CRUNCH_ALLOW_EMAIL=true to allow it", where, k)
	}
	return nil
}

// sendFlagOn returns the name of a field (at any depth) that looks like an instruction to
// send something, such as sendEmail or sendToCustomer, set to true; or "" if there is none.
// sendReminders is left to remindersOn, which allows keeping reminders that are already on.
func sendFlagOn(v any) string {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if n := normKey(k); strings.HasPrefix(n, "send") && n != "sendreminders" { // reminders: see remindersOn
				if b, ok := val.(bool); (ok && b) || strings.EqualFold(fmt.Sprint(val), "true") {
					return k
				}
			}
			if k := sendFlagOn(val); k != "" {
				return k
			}
		}
	case []any:
		for _, val := range x {
			if k := sendFlagOn(val); k != "" {
				return k
			}
		}
	}
	return ""
}

// rawReads reports whether a raw request only reads (a GET, or a POST that only reads),
// so it is left out of the audit log.
func rawReads(args map[string]any) bool {
	method, _ := args["method"].(string)
	switch strings.ToUpper(method) {
	case "", http.MethodGet:
		return true
	case http.MethodPost:
		p, _ := args["path"].(string)
		_, canon, err := cleanRawPath(p)
		return err == nil && crunch.IsReadOnlyPost(canon)
	}
	return false
}
