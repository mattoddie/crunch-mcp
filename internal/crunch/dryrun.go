package crunch

// Dry runs and the record of what a tool call sent.
//
// A tool call puts a *Call in its context. The client then records every
// request that changes something (for the audit log) and, in a dry run, stops
// the first one before it is sent. GETs and the POSTs that only read data
// still go through, so a preview can be built from the live record.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// readOnlyPostRe matches the POST endpoints that only read data.
var readOnlyPostRe = regexp.MustCompile(`^/banking/(bank-accounts|credit-cards)/opening-balances/?$`)

// IsReadOnlyPost reports whether a POST to path (relative to the API root) only reads data.
func IsReadOnlyPost(path string) bool {
	return readOnlyPostRe.MatchString(strings.ToLower(path))
}

// changes reports whether a request can change something in Crunch.
func changes(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		return false
	case http.MethodPost:
		return !IsReadOnlyPost(path)
	}
	return true
}

// Call records the requests made for one tool call.
type Call struct {
	mu          sync.Mutex
	dryRun      bool
	sent        []SentRequest
	fetched     []fetched
	intercepted *Intercepted
}

// SentRequest is a request that changes something, as sent to Crunch.
type SentRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Status is the HTTP status, or 0 when no response arrived.
	Status int `json:"status,omitempty"`
}

// Intercepted is the first request of a dry run that would have changed something. It was not sent.
type Intercepted struct {
	Method string
	Path   string
	Query  Query
	Body   any
	// Current is the record the request would change, when the call fetched it first.
	Current any
}

type fetched struct {
	path string
	data any
}

type callKey struct{}

// WithCall returns a context whose requests are recorded in call.
func WithCall(ctx context.Context, call *Call) context.Context {
	return context.WithValue(ctx, callKey{}, call)
}

// CallFrom returns the Call in ctx, or nil.
func CallFrom(ctx context.Context) *Call {
	call, _ := ctx.Value(callKey{}).(*Call)
	return call
}

// SetDryRun makes the call a dry run: no request that changes something is sent.
func (c *Call) SetDryRun() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dryRun = true
}

// DryRun reports whether the call is a dry run.
func (c *Call) DryRun() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dryRun
}

// Sent returns the requests that changed something (or tried to).
func (c *Call) Sent() []SentRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SentRequest(nil), c.sent...)
}

// Intercepted returns the request a dry run stopped, or nil.
func (c *Call) Intercepted() *Intercepted {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.intercepted
}

func (c *Call) recordSent(method, path string, status int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, SentRequest{method, path, status})
}

// remember keeps a GET response during a dry run, so an update can be shown as a diff.
func (c *Call) remember(path string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dryRun {
		return
	}
	if v, err := normalise(data); err == nil {
		c.fetched = append(c.fetched, fetched{path, v})
	}
}

// intercept records a request a dry run stops and returns the error the tool sees.
func (c *Call) intercept(method, path string, q Query, body any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.intercepted == nil {
		var b any
		if body != nil {
			if data, err := json.Marshal(body); err == nil {
				b, _ = normalise(data)
			}
		}
		c.intercepted = &Intercepted{Method: method, Path: path, Query: q, Body: b, Current: c.currentLocked(method, path)}
	}
	return &DryRunError{Method: method, Path: path}
}

// currentLocked finds the record a request changes among the responses fetched
// earlier in the call: the response from the same path, or an object whose id is
// the path's last segment.
func (c *Call) currentLocked(method, path string) any {
	if method == http.MethodPost {
		return nil
	}
	for i := len(c.fetched) - 1; i >= 0; i-- {
		if c.fetched[i].path == path {
			if _, ok := c.fetched[i].data.(map[string]any); ok {
				return c.fetched[i].data
			}
		}
	}
	last := path[strings.LastIndex(path, "/")+1:]
	if _, err := strconv.ParseInt(last, 10, 64); err != nil {
		return nil
	}
	for i := len(c.fetched) - 1; i >= 0; i-- {
		if o, ok := c.fetched[i].data.(map[string]any); ok && fmt.Sprint(o["id"]) == last {
			return o
		}
	}
	return nil
}

// normalise decodes JSON keeping numbers exact, so values from the API and
// bodies built here compare equal when they hold the same thing.
func normalise(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// DryRunError is returned for a request a dry run didn't send.
type DryRunError struct{ Method, Path string }

func (e *DryRunError) Error() string {
	return fmt.Sprintf("dry run: %s %s was not sent", e.Method, e.Path)
}

// Preview describes an intercepted request for the model: what would be sent, and for
// an update of a record the call fetched first, the fields that would change.
func (i *Intercepted) Preview() Obj {
	send := Obj{"method": i.Method, "path": i.Path}
	if qs := i.Query.Encode(); qs != "" {
		send["query"] = qs
	}
	out := Obj{"dry_run": true, "would_send": send}
	switch {
	case i.Current != nil && i.Method != http.MethodDelete && i.Body != nil:
		out["changes"] = Diff(i.Current, i.Body)
	case i.Body != nil:
		send["body"] = ShortenFiles(i.Body)
	}
	if i.Current != nil && i.Method == http.MethodDelete {
		out["current"] = ShortenFiles(i.Current)
	}
	return out
}

// Diff lists the fields an update would change, as {changed: {path: {from, to}},
// added: {path: value}, removed: {path: value}}. Fields are named by path, such as
// expenseLineItems[0].amount.grossAmount. Removed fields are reported because
// Crunch updates replace the whole record.
func Diff(current, body any) Obj {
	d := &differ{changed: Obj{}, added: Obj{}, removed: Obj{}}
	d.walk("", current, body)
	out := Obj{}
	for name, o := range map[string]Obj{"changed": d.changed, "added": d.added, "removed": d.removed} {
		if len(o) > 0 {
			out[name] = o
		}
	}
	return out
}

type differ struct{ changed, added, removed Obj }

func (d *differ) walk(path string, from, to any) {
	fo, fok := from.(map[string]any)
	tob, tok := to.(map[string]any)
	if fok && tok {
		for _, k := range sortedKeys(tob) {
			p := joinPath(path, k)
			if _, ok := fo[k]; !ok {
				if tob[k] != nil {
					d.added[p] = ShortenFiles(tob[k])
				}
				continue
			}
			d.walk(p, fo[k], tob[k])
		}
		for _, k := range sortedKeys(fo) {
			if _, ok := tob[k]; !ok && fo[k] != nil {
				d.removed[joinPath(path, k)] = ShortenFiles(fo[k])
			}
		}
		return
	}
	fa, faok := from.([]any)
	ta, taok := to.([]any)
	if faok && taok && len(fa) == len(ta) {
		for i := range ta {
			d.walk(path+"["+strconv.Itoa(i)+"]", fa[i], ta[i])
		}
		return
	}
	if !sameJSON(from, to) {
		d.changed[path] = Obj{"from": ShortenFiles(from), "to": ShortenFiles(to)}
	}
}

// sameJSON compares two decoded values; numbers compare by value, so 120 and 120.00 are equal.
func sameJSON(a, b any) bool {
	an, aok := a.(json.Number)
	bn, bok := b.(json.Number)
	if aok && bok {
		af, err1 := an.Float64()
		bf, err2 := bn.Float64()
		return err1 == nil && err2 == nil && af == bf
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

func sortedKeys(o map[string]any) []string {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

var dataURIRe = regexp.MustCompile(`^data:([^;,]*)[^,]*,`)

// ShortenFiles replaces long file contents (data URIs and base64 strings over
// 256 characters) with a short description, so previews and the audit log stay
// readable and don't copy receipts around.
func ShortenFiles(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = ShortenFiles(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = ShortenFiles(val)
		}
		return out
	case string:
		if len(x) <= 256 {
			return x
		}
		if m := dataURIRe.FindStringSubmatch(x); m != nil {
			return fmt.Sprintf("[%s file, %d characters of base64]", firstNonEmpty(m[1], "unknown"), len(x)-len(m[0]))
		}
		if base64Re.MatchString(x) {
			return fmt.Sprintf("[%d characters of base64]", len(x))
		}
	}
	return v
}

var base64Re = regexp.MustCompile(`^[A-Za-z0-9+/=\s]+$`)
