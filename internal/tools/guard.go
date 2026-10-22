package tools

// Safety layers for tools that change things: a dry_run argument that previews
// the first change without sending it, and an audit log entry for every call
// (CRUNCH_AUDIT_LOG).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

const dryRunDescription = "Preview: return the request that would be sent (for an update, the fields that would change) without sending it"

const dryRunNote = "Nothing was changed: this was a dry run. would_send is the first request that would change something, and it was not sent. Later steps of the tool, and Crunch's response, were not simulated. Call the tool again without dry_run to make the change."

// guard describes a tool that changes things.
type guard struct {
	// reads, when set, reports whether a call's arguments only read (no audit entry).
	reads func(args map[string]any) bool
}

// addDryRunArg adds the dry_run argument to a tool's input schema.
func addDryRunArg(s *jsonschema.Schema) {
	if s.Properties == nil {
		s.Properties = map[string]*jsonschema.Schema{}
	}
	s.Properties["dry_run"] = &jsonschema.Schema{Type: "boolean", Description: dryRunDescription}
}

// guardMiddleware wraps every call of a guarded tool, before its input is validated:
// it puts a crunch.Call in the context (which records the requests sent, and stops
// them in a dry run) and writes the audit log entry once the call is done.
func (c *Context) guardMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		r, ok := req.(*mcp.CallToolRequest)
		if method != "tools/call" || !ok || r.Params == nil {
			return next(ctx, method, req)
		}
		g, ok := c.guarded[r.Params.Name]
		if !ok {
			return next(ctx, method, req)
		}
		args := parseArgs(r.Params.Arguments)
		call := &crunch.Call{}
		if args["dry_run"] == true {
			call.SetDryRun()
		}
		res, err := next(crunch.WithCall(ctx, call), method, req)
		if c.audit != nil && (g.reads == nil || !g.reads(args)) {
			c.audit.record(r.Params.Name, args, call, res, err)
		}
		return res, err
	}
}

func parseArgs(raw json.RawMessage) map[string]any {
	var args map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if len(raw) == 0 || dec.Decode(&args) != nil || args == nil {
		return map[string]any{}
	}
	return args
}

// dryRunResult turns a guarded handler's result into the dry-run preview.
func dryRunResult(ctx context.Context, out any, err error) (any, error) {
	call := crunch.CallFrom(ctx)
	if call == nil || !call.DryRun() {
		return out, err
	}
	if i := call.Intercepted(); i != nil {
		preview := i.Preview()
		note := dryRunNote
		if ch, ok := preview["changes"].(Obj); ok && len(ch) == 0 {
			note = "The request would not change any field. " + note
		}
		preview["note"] = note
		return preview, nil
	}
	if err != nil {
		return nil, err
	}
	return Obj{"dry_run": true, "would_send": nil, "note": "The tool would not send any request that changes something. result is what it returned.", "result": out}, nil
}

// auditLog writes one JSON line per call of a tool that changes things.
type auditLog struct {
	target string
	stderr io.Writer
	mu     sync.Mutex
	warned sync.Once
}

func newAuditLog(target string) *auditLog {
	if target == "" {
		return nil
	}
	return &auditLog{target: target, stderr: os.Stderr}
}

func (l *auditLog) record(tool string, args map[string]any, call *crunch.Call, res mcp.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			l.warn(r)
		}
	}()
	requests := call.Sent()
	if requests == nil {
		requests = []crunch.SentRequest{}
	}
	entry := Obj{
		"time":      time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		"tool":      tool,
		"dry_run":   call.DryRun(),
		"arguments": redact(args),
		"requests":  requests,
		"outcome":   "ok",
	}
	if p := call.Intercepted(); p != nil {
		entry["previewed"] = Obj{"method": p.Method, "path": p.Path}
	}
	if tr, ok := res.(*mcp.CallToolResult); ok && tr != nil && tr.IsError {
		msg := ""
		if len(tr.Content) > 0 {
			if t, ok := tr.Content[0].(*mcp.TextContent); ok {
				msg = strings.TrimPrefix(t.Text, "Error: ")
			}
		}
		entry["outcome"], entry["error"] = "error", msg
	} else if err != nil {
		entry["outcome"], entry["error"] = "error", err.Error()
	}
	line, merr := json.Marshal(entry)
	if merr != nil {
		l.warn(merr)
		return
	}
	l.write(string(line) + "\n")
}

func (l *auditLog) write(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	if l.target == config.AuditLogStderr {
		_, err = io.WriteString(l.stderr, line)
	} else {
		var f *os.File
		// Opened for each entry, so a rotated or deleted log is recreated.
		if f, err = config.OpenAuditLog(l.target); err == nil {
			_, err = f.WriteString(line)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
	}
	if err != nil {
		l.warn(err)
	}
}

// warn reports the first audit log failure; tool calls carry on regardless.
func (l *auditLog) warn(err any) {
	l.warned.Do(func() {
		log.Printf("crunch-mcp: WARNING: could not write the audit log (%s): %v. Tool calls carry on; this warning is not repeated.", l.target, err)
	})
}

// secretKeyRe matches argument names whose values must not be logged.
var secretKeyRe = regexp.MustCompile(`(?i)token|secret|password|verifier|authorization|^code$`)

// redact returns the arguments with secrets replaced and file contents shortened.
func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if secretKeyRe.MatchString(k) && val != nil && val != "" {
				out[k] = "[redacted]"
				continue
			}
			out[k] = redact(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redact(val)
		}
		return out
	case string:
		// File contents and other long values are summarised, so the log stays small
		// and doesn't hold copies of receipts.
		s, _ := crunch.ShortenFiles(x).(string)
		if len(s) > maxLoggedString {
			return strings.ToValidUTF8(s[:64], "") + fmt.Sprintf("… (%d characters)", len(s))
		}
		return s
	}
	return v
}

// maxLoggedString is the longest argument value the audit log keeps whole.
const maxLoggedString = 256
