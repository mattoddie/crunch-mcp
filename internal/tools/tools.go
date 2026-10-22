// Package tools defines crunch-mcp's MCP tools and prompts.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/config"
	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

// Obj is a JSON object from the API.
type Obj = crunch.Obj

// Gate is the setting a tool needs before it is registered.
type Gate int

const (
	Read   Gate = iota // always registered
	Write              // CRUNCH_ALLOW_WRITES
	Delete             // CRUNCH_ALLOW_WRITES and CRUNCH_ALLOW_DELETES
	Email              // CRUNCH_ALLOW_WRITES and CRUNCH_ALLOW_EMAIL
)

func (g Gate) String() string {
	return [...]string{"read", "write", "delete", "email"}[g]
}

// Context is shared by the tool definitions.
type Context struct {
	Server *mcp.Server
	Client *crunch.Client
	config.Tools
	// Registered records each registered tool's toolset and gate, for the docs generator.
	Registered map[string]Info
	// guarded holds the tools that change things: they take dry_run and are audited.
	guarded map[string]guard
	audit   *auditLog
}

// Info describes a registered tool.
type Info struct {
	Toolset string
	Gate    Gate
}

// Def describes a tool.
type Def struct {
	Toolset     string
	Name        string
	Title       string
	Description string
	Gate        Gate
	// ReadOnly overrides the readOnlyHint annotation (defaults to Gate == Read).
	ReadOnly *bool
	// Destructive overrides the destructiveHint annotation (defaults to Gate == Delete).
	Destructive *bool
	Idempotent  bool
	// OpenWorld marks tools that reach outside Crunch, such as emailing a customer.
	OpenWorld bool
	// Enums restricts string arguments to fixed values, keyed by argument name.
	Enums map[string][]string
	// Writes marks a Read-gated tool that can still change things (the raw request tool
	// with writes enabled), so it gets dry_run and the audit log like Write tools.
	Writes bool
	// Reads, for a tool that changes things, reports whether a call's arguments only
	// read; such calls are left out of the audit log.
	Reads func(args map[string]any) bool
}

// Money is an amount given as a number (120) or a decimal string ("120.00"). It is sent to Crunch as a JSON number.
type Money string

// UnmarshalJSON accepts numbers and decimal strings.
func (m *Money) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	var n json.Number = json.Number(s)
	if _, err := n.Float64(); err != nil {
		return fmt.Errorf("%q is not an amount; use a number like 120.00", s)
	}
	*m = Money(s)
	return nil
}

// MarshalJSON sends the amount as a JSON number.
func (m Money) MarshalJSON() ([]byte, error) {
	if m == "" {
		return []byte("null"), nil
	}
	return []byte(m), nil
}

// Float returns the amount as a float64.
func (m Money) Float() float64 {
	f, _ := json.Number(m).Float64()
	return f
}

// Date is a YYYY-MM-DD date.
type Date = string

var typeSchemas = map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[Money](): {Types: []string{"number", "string"}, Pattern: `^-?\d+(\.\d+)?$`},
}

const datePattern = `^\d{4}-\d{2}-\d{2}$`

func boolPtr(b bool) *bool { return &b }

// schemaFor infers the input schema from In: descriptions come from `jsonschema` tags, optional
// fields have `omitempty`, fields named *date* get a date pattern, and Enums become enum lists.
func schemaFor[In any](d Def) *jsonschema.Schema {
	s, err := jsonschema.For[In](&jsonschema.ForOptions{TypeSchemas: typeSchemas})
	if err != nil {
		panic(fmt.Sprintf("tool %s: %v", d.Name, err))
	}
	var walk func(*jsonschema.Schema)
	walk = func(s *jsonschema.Schema) {
		if s == nil {
			return
		}
		// Optional pointer and slice fields infer as ["null", T]; plain T reads better, and omitting the argument is how to leave it out.
		if len(s.Types) == 2 && contains(s.Types, "null") {
			for _, t := range s.Types {
				if t != "null" {
					s.Type, s.Types = t, nil
				}
			}
		}
		for name, p := range s.Properties {
			if (name == "date" || strings.HasSuffix(name, "_date") || strings.HasPrefix(name, "date_")) && (p.Type == "string" || contains(p.Types, "string")) {
				p.Pattern = datePattern
			}
			if values, ok := d.Enums[name]; ok {
				p.Enum = nil
				for _, v := range values {
					p.Enum = append(p.Enum, v)
				}
			}
			walk(p)
			walk(p.Items)
		}
	}
	walk(s)
	return s
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Result lets a handler return a fully formed tool result (e.g. with file content) instead of JSON.
type Result struct{ *mcp.CallToolResult }

// textResult renders a handler's value as indented JSON.
func textResult(v any) *mcp.CallToolResult {
	if r, ok := v.(Result); ok {
		return r.CallToolResult
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		b = []byte(fmt.Sprintf("%v", v))
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

// add registers a tool if its toolset and gate are enabled.
func add[In any](c *Context, d Def, h func(ctx context.Context, in In) (any, error)) {
	if !c.Toolsets[d.Toolset] {
		return
	}
	switch d.Gate {
	case Write:
		if !c.AllowWrites {
			return
		}
	case Delete:
		if !c.AllowWrites || !c.AllowDeletes {
			return
		}
	case Email:
		if !c.AllowWrites || !c.AllowEmail {
			return
		}
	}
	readOnly := d.Gate == Read
	if d.ReadOnly != nil {
		readOnly = *d.ReadOnly
	}
	ann := &mcp.ToolAnnotations{Title: d.Title, ReadOnlyHint: readOnly, IdempotentHint: d.Idempotent, OpenWorldHint: boolPtr(d.OpenWorld)}
	if !readOnly {
		ann.DestructiveHint = boolPtr(d.Gate == Delete)
		if d.Destructive != nil {
			ann.DestructiveHint = d.Destructive
		}
	}
	schema := schemaFor[In](d)
	guarded := d.Gate != Read || d.Writes
	if guarded {
		addDryRunArg(schema)
		if c.guarded == nil {
			c.guarded = map[string]guard{}
		}
		c.guarded[d.Name] = guard{reads: d.Reads}
	}
	tool := &mcp.Tool{Name: d.Name, Title: d.Title, Description: d.Description, Annotations: ann, InputSchema: schema}
	mcp.AddTool(c.Server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := h(ctx, in)
		if guarded {
			out, err = dryRunResult(ctx, out, err)
		}
		if err != nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}}, IsError: true}, nil, nil
		}
		return textResult(out), nil, nil
	})
	if c.Registered != nil {
		c.Registered[d.Name] = Info{Toolset: d.Toolset, Gate: d.Gate}
	}
}

// NoArgs is the input of tools without arguments.
type NoArgs struct{}
