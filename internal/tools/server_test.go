package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/config"
)

func TestGatesControlWhichToolsExist(t *testing.T) {
	all := config.AllTools()
	registered := map[string]Info{}
	NewServer(nil, all, registered)

	readOnly := all
	readOnly.AllowWrites, readOnly.AllowDeletes, readOnly.AllowEmail = false, false, false
	writes := all
	writes.AllowDeletes, writes.AllowEmail = false, false

	env := newEnv(t, readOnly)
	reads := toolNames(t, env.session)
	writeTools := toolNames(t, connect(t, env.client, writes))
	allTools := toolNames(t, connect(t, env.client, all))

	if len(allTools) != len(registered) {
		t.Errorf("registered %d, listed %d", len(registered), len(allTools))
	}
	for name, info := range registered {
		_, inRead := reads[name]
		_, inWrite := writeTools[name]
		switch info.Gate {
		case Read:
			if !inRead {
				t.Errorf("%s: read tool missing in read-only mode", name)
			}
			if tool := allTools[name]; !tool.Annotations.ReadOnlyHint && name != "crunch_api_request" && !strings.HasPrefix(name, "crunch_auth_") {
				t.Errorf("%s: read tool without readOnlyHint", name)
			}
		case Write:
			if inRead || !inWrite {
				t.Errorf("%s: write tool gating (read=%v write=%v)", name, inRead, inWrite)
			}
		case Delete, Email:
			if inRead || inWrite {
				t.Errorf("%s: %s tool registered without its switch", name, info.Gate)
			}
		}
		if info.Gate == Delete && (allTools[name].Annotations.DestructiveHint == nil || !*allTools[name].Annotations.DestructiveHint) {
			t.Errorf("%s: delete tool not marked destructive", name)
		}
	}
	if tool := allTools["crunch_email_invoice"]; tool == nil || tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
		t.Error("crunch_email_invoice should be open-world")
	}
	if tool := allTools["crunch_issue_invoice"]; tool == nil || !*tool.Annotations.DestructiveHint {
		t.Error("issuing an invoice is final and should be marked destructive")
	}
	if len(reads) < 30 || len(allTools) < 65 {
		t.Errorf("unexpectedly few tools: %d read, %d total", len(reads), len(allTools))
	}
}

func TestToolsetsFilter(t *testing.T) {
	o := config.AllTools()
	o.Toolsets = map[string]bool{"invoices": true}
	env := newEnv(t, o)
	for name := range toolNames(t, env.session) {
		if !strings.Contains(name, "invoice") {
			t.Errorf("%s registered with only the invoices toolset", name)
		}
	}
}

func TestSchemasDescribeEveryArgument(t *testing.T) {
	env := newEnv(t)
	for name, tool := range toolNames(t, env.session) {
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s: missing title or description", name)
		}
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for arg, p := range props {
			if d, _ := p.(map[string]any)["description"].(string); d == "" {
				t.Errorf("%s.%s has no description", name, arg)
			}
		}
	}
}

func TestPrompts(t *testing.T) {
	env := newEnv(t)
	var names []string
	for p, err := range env.session.Prompts(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, p.Name)
	}
	if len(names) != len(prompts) {
		t.Errorf("prompts: %v", names)
	}
	res, err := env.session.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "create_invoice", Arguments: map[string]string{"client": "Acme", "work": "3 days at 500"}})
	if err != nil {
		t.Fatal(err)
	}
	txt := res.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(txt, "Acme") || !strings.Contains(txt, "crunch_create_invoice") {
		t.Errorf("prompt text: %s", txt)
	}
}

func TestToolErrorsAreToolResults(t *testing.T) {
	env := newEnv(t)
	msg := env.callErr("crunch_get_expense", map[string]any{"expense_id": 404})
	if !strings.Contains(msg, "HTTP 404") {
		t.Errorf("error: %s", msg)
	}
	// Schema validation rejects bad input before the handler runs.
	res, err := env.session.CallTool(context.Background(), &mcp.CallToolParams{Name: "crunch_create_expense", Arguments: map[string]any{"posting_date": "14/09/2026"}})
	if err == nil && !res.IsError {
		t.Error("invalid arguments were accepted")
	}
}

// With CRUNCH_FILES_DIR set, save_to writes files, so the download tools aren't read-only.
func TestDownloadToolsAreReadOnlyOnlyWithoutFilesDir(t *testing.T) {
	ro := config.AllTools()
	ro.AllowWrites, ro.AllowDeletes, ro.AllowEmail = false, false, false
	env := newEnv(t, ro)
	withFiles := ro
	withFiles.FilesDir = filesDir(t)
	without, with := toolNames(t, env.session), toolNames(t, connect(t, env.client, withFiles))
	for _, name := range []string{"crunch_download_invoice_pdf", "crunch_download_invoice_attachment", "crunch_download_expense_attachment"} {
		if !without[name].Annotations.ReadOnlyHint {
			t.Errorf("%s: should be read-only without CRUNCH_FILES_DIR", name)
		}
		a := with[name].Annotations
		if a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s with CRUNCH_FILES_DIR: readOnly=%v destructive=%v, want false/false", name, a.ReadOnlyHint, a.DestructiveHint)
		}
	}
}
