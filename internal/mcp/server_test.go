package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/service"
	"github.com/ematvey/kvt/internal/testutil"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerRegistersKVTTools(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	names := RegisteredToolNames(server)
	for _, want := range []string{
		"kvt_summary",
		"kvt_howto",
		"kvt_search",
		"kvt_grep",
		"kvt_list",
		"kvt_read",
		"kvt_types",
		"kvt_log",
		"kvt_history",
		"kvt_write",
		"kvt_edit",
		"kvt_delete",
		"kvt_validate",
	} {
		if !names[want] {
			t.Fatalf("missing tool %s in %#v", want, names)
		}
	}
	if names["kvt_push"] {
		t.Fatalf("push must not be exposed as an MCP tool")
	}
}

func TestHowtoMentionsServiceOwnedFilesAndConflictRetry(t *testing.T) {
	text := DefaultHowto()
	for _, want := range []string{"index.md", "timestamp", "base_hash", "kvt_search"} {
		if !strings.Contains(text, want) {
			t.Fatalf("howto missing %q", want)
		}
	}
}

func TestSummaryToolOverMCP(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)

	result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "kvt_summary",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("missing result content")
	}
	text, ok := result.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content = %#v", result.Content[0])
	}
	if !strings.Contains(text.Text, "document_count") {
		t.Fatalf("summary text = %q", text.Text)
	}
}

func TestServerInstructionsOverMCP(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)

	instructions := session.InitializeResult().Instructions
	for _, want := range []string{"kvt_search", "kvt_howto", "base_hash"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions missing %q: %q", want, instructions)
		}
	}
}

func TestMCPToolContractsUseStableJSONShapes(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)

	written := callToolMap(t, session, "kvt_write", map[string]any{
		"path":    "notes/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nPrimary body\n",
	})
	if written["path"] != "notes/a.md" || written["hash"] == "" || written["Path"] != nil {
		t.Fatalf("write output = %#v", written)
	}
	read := callToolMap(t, session, "kvt_read", map[string]any{"path": "notes/a.md"})
	if read["path"] != "notes/a.md" || read["content"] == "" || read["document"] != nil || read["Document"] != nil {
		t.Fatalf("read output = %#v", read)
	}
	list := callToolMap(t, session, "kvt_list", map[string]any{"type": "Note", "limit": 10})
	if list["documents"] == nil || list["Documents"] != nil {
		t.Fatalf("list output = %#v", list)
	}
	search := callToolMap(t, session, "kvt_search", map[string]any{"query": "Primary", "limit": 10})
	if search["hits"] == nil || search["Hits"] != nil {
		t.Fatalf("search output = %#v", search)
	}

	conflict := callToolResult(t, session, "kvt_write", map[string]any{
		"path":      "notes/a.md",
		"base_hash": "stale",
		"content":   "---\ntype: Note\ntitle: A\n---\nUpdated\n",
	})
	if !conflict.IsError {
		t.Fatalf("expected conflict to be a tool-visible error: %#v", conflict)
	}
}

func TestMCPRequestAccessRestrictsReadAndWrite(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)

	emptyAccess := callToolResult(t, session, "kvt_write", map[string]any{
		"path":    "public/empty.md",
		"content": "---\ntype: Note\ntitle: Empty\n---\nA\n",
		"access":  map[string]any{},
	})
	if !emptyAccess.IsError {
		t.Fatalf("empty access result = %#v", emptyAccess)
	}
	denied := callToolResult(t, session, "kvt_write", map[string]any{
		"path":    "private/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nA\n",
		"access":  map[string]any{"write_globs": []string{"public/**"}},
	})
	if !denied.IsError {
		t.Fatalf("denied write result = %#v", denied)
	}
	callToolMap(t, session, "kvt_write", map[string]any{
		"path":    "public/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nneedle\n",
		"access":  map[string]any{"write_globs": []string{"public/**"}},
	})
	readDenied := callToolResult(t, session, "kvt_read", map[string]any{
		"path":   "public/a.md",
		"access": map[string]any{"read_globs": []string{"private/**"}},
	})
	if !readDenied.IsError {
		t.Fatalf("read denied result = %#v", readDenied)
	}
	readAllowed := callToolMap(t, session, "kvt_read", map[string]any{
		"path":   "public/a.md",
		"access": map[string]any{"read_globs": []string{"public/**"}},
	})
	if readAllowed["path"] != "public/a.md" {
		t.Fatalf("read allowed = %#v", readAllowed)
	}
	editDenied := callToolResult(t, session, "kvt_edit", map[string]any{
		"path":       "public/a.md",
		"old_string": "needle",
		"new_string": "changed",
		"access":     map[string]any{"write_globs": []string{"private/**"}},
	})
	if !editDenied.IsError {
		t.Fatalf("edit denied result = %#v", editDenied)
	}
	deleteDenied := callToolResult(t, session, "kvt_delete", map[string]any{
		"path":   "public/a.md",
		"access": map[string]any{"write_globs": []string{"private/**"}},
	})
	if !deleteDenied.IsError {
		t.Fatalf("delete denied result = %#v", deleteDenied)
	}
}

func TestMCPRequestAccessFiltersDiscoveryAndRejectsLog(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)
	for _, path := range []string{"public/a.md", "private/a.md"} {
		callToolMap(t, session, "kvt_write", map[string]any{
			"path":    path,
			"content": "---\ntype: Note\ntitle: A\n---\nneedle\n",
		})
	}
	accessArg := map[string]any{"read_globs": []string{"public/**"}}
	list := callToolMap(t, session, "kvt_list", map[string]any{"access": accessArg})
	docs := list["documents"].([]any)
	if len(docs) != 1 || docs[0].(map[string]any)["path"] != "public/a.md" {
		t.Fatalf("docs = %#v", docs)
	}
	grep := callToolMap(t, session, "kvt_grep", map[string]any{
		"query":  "needle",
		"access": accessArg,
	})
	matches := grep["matches"].([]any)
	if len(matches) != 1 || matches[0].(map[string]any)["path"] != "public/a.md" {
		t.Fatalf("matches = %#v", matches)
	}
	search := callToolMap(t, session, "kvt_search", map[string]any{
		"query":  "needle",
		"access": accessArg,
	})
	hits := search["hits"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["path"] != "public/a.md" {
		t.Fatalf("hits = %#v", hits)
	}
	historyDenied := callToolResult(t, session, "kvt_history", map[string]any{
		"path":   "private/a.md",
		"access": accessArg,
	})
	if !historyDenied.IsError {
		t.Fatalf("history denied = %#v", historyDenied)
	}
	logDenied := callToolResult(t, session, "kvt_log", map[string]any{
		"access": accessArg,
	})
	if !logDenied.IsError {
		t.Fatalf("log denied = %#v", logDenied)
	}
	badGlob := callToolResult(t, session, "kvt_read", map[string]any{
		"path":   "public/a.md",
		"access": map[string]any{"read_globs": []string{"../bad/**"}},
	})
	if !badGlob.IsError {
		t.Fatalf("bad glob result = %#v", badGlob)
	}
}

func TestMCPListAndGrepExposePaginationCursor(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)
	for _, item := range []string{"a", "b"} {
		callToolMap(t, session, "kvt_write", map[string]any{
			"path":    "notes/" + item + ".md",
			"content": "---\ntype: Note\ntitle: " + item + "\n---\nshared body\n",
		})
	}

	list := callToolMap(t, session, "kvt_list", map[string]any{"limit": 1})
	if list["next_cursor"] == "" || list["truncated"] != true {
		t.Fatalf("list output = %#v", list)
	}
	grep := callToolMap(t, session, "kvt_grep", map[string]any{"query": "shared", "limit": 1})
	if grep["next_cursor"] == "" || grep["truncated"] != true {
		t.Fatalf("grep output = %#v", grep)
	}
}

func TestMCPListAndGrepApplyResponseBudget(t *testing.T) {
	svc := newMCPTestService(t)
	cfg := openConfig()
	cfg.Limits.MaxResponseChars = 650
	server, err := NewServer(svc, cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)
	longBody := "needle " + strings.Repeat("alpha ", 700)
	for _, item := range []string{"a", "b", "c", "d", "e", "f"} {
		callToolMap(t, session, "kvt_write", map[string]any{
			"path":    "notes/" + item + ".md",
			"content": "---\ntype: Note\ntitle: " + item + "\ndescription: " + strings.Repeat(item, 120) + "\n---\n" + longBody + "\n",
		})
	}

	list := callToolMap(t, session, "kvt_list", map[string]any{"limit": 20})
	assertBudgetedMCPOutput(t, list, cfg.Limits.MaxResponseChars)
	grep := callToolMap(t, session, "kvt_grep", map[string]any{"query": "alpha", "limit": 20})
	assertBudgetedMCPOutput(t, grep, cfg.Limits.MaxResponseChars)
}

func TestToolDescriptionsAreRegistered(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)
	tools, err := session.ListTools(t.Context(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	descriptions := map[string]string{}
	for _, tool := range tools.Tools {
		descriptions[tool.Name] = tool.Description
	}
	for _, name := range []string{"kvt_search", "kvt_write", "kvt_validate"} {
		if strings.TrimSpace(descriptions[name]) == "" {
			t.Fatalf("missing description for %s in %#v", name, descriptions)
		}
	}
}

func TestHowtoResourceAndPromptOverMCP(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)

	resources, err := session.ListResources(t.Context(), &mcpsdk.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	foundResource := false
	for _, resource := range resources.Resources {
		if resource.URI == howtoURI {
			foundResource = true
		}
	}
	if !foundResource {
		t.Fatalf("missing howto resource: %#v", resources.Resources)
	}
	resource, err := session.ReadResource(t.Context(), &mcpsdk.ReadResourceParams{URI: howtoURI})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(resource.Contents) != 1 || !strings.Contains(resource.Contents[0].Text, "base_hash") {
		t.Fatalf("resource = %#v", resource.Contents)
	}

	prompts, err := session.ListPrompts(t.Context(), &mcpsdk.ListPromptsParams{})
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	foundPrompt := false
	for _, prompt := range prompts.Prompts {
		if prompt.Name == "kvt_howto" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("missing howto prompt: %#v", prompts.Prompts)
	}
	prompt, err := session.GetPrompt(t.Context(), &mcpsdk.GetPromptParams{Name: "kvt_howto"})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(prompt.Messages) != 1 {
		t.Fatalf("prompt messages = %#v", prompt.Messages)
	}
	text, ok := prompt.Messages[0].Content.(*mcpsdk.TextContent)
	if !ok || !strings.Contains(text.Text, "kvt_validate") {
		t.Fatalf("prompt content = %#v", prompt.Messages[0].Content)
	}
}

func TestHowtoIncludesVaultHouseRules(t *testing.T) {
	svc, root := newMCPTestServiceWithRoot(t)
	if err := os.WriteFile(filepath.Join(root, "_howto.md"), []byte("House rule: link incidents to systems.\n"), 0o644); err != nil {
		t.Fatalf("write howto: %v", err)
	}
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)

	tool := callToolMap(t, session, "kvt_howto", map[string]any{})
	if !strings.Contains(tool["text"].(string), "House rule: link incidents to systems.") {
		t.Fatalf("tool howto = %#v", tool)
	}
	resource, err := session.ReadResource(t.Context(), &mcpsdk.ReadResourceParams{URI: howtoURI})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if !strings.Contains(resource.Contents[0].Text, "House rule: link incidents to systems.") {
		t.Fatalf("resource howto = %#v", resource.Contents)
	}
	prompt, err := session.GetPrompt(t.Context(), &mcpsdk.GetPromptParams{Name: "kvt_howto"})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	text, ok := prompt.Messages[0].Content.(*mcpsdk.TextContent)
	if !ok || !strings.Contains(text.Text, "House rule: link incidents to systems.") {
		t.Fatalf("prompt content = %#v", prompt.Messages[0].Content)
	}
}

func TestReadToolSupportsLineRangeAndWarnings(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)
	callToolMap(t, session, "kvt_write", map[string]any{
		"path":    "notes/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nline one\nline two\nline three\n",
	})
	full := callToolMap(t, session, "kvt_read", map[string]any{"path": "notes/a.md"})
	lines := strings.Split(full["content"].(string), "\n")
	lineTwo := 0
	for i, line := range lines {
		if line == "line two" {
			lineTwo = i + 1
			break
		}
	}
	if lineTwo == 0 {
		t.Fatalf("line two missing from %#v", full)
	}

	got := callToolMap(t, session, "kvt_read", map[string]any{"path": "notes/a.md", "start_line": lineTwo, "end_line": lineTwo})
	if strings.TrimSpace(got["content"].(string)) != "line two" {
		t.Fatalf("range read = %#v", got)
	}
	warnings, ok := got["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("warnings = %#v", got["warnings"])
	}
}

func newMCPTestService(t *testing.T) *service.Service {
	t.Helper()
	svc, _ := newMCPTestServiceWithRoot(t)
	return svc
}

func newMCPTestServiceWithRoot(t *testing.T) (*service.Service, string) {
	t.Helper()
	testutil.RequireGit(t)
	root := t.TempDir()
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	svc, err := service.New(root, config.Default(), service.Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, root
}

func openConfig() config.Config {
	return config.Default()
}

func connectMCPClient(t *testing.T, server *Server) *mcpsdk.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	errc := make(chan error, 1)
	go func() {
		errc <- server.Run(ctx, serverTransport)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-errc; err != nil && !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("server Run: %v", err)
		}
	})

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "kvt-test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	return session
}

func callToolMap(t *testing.T, session *mcpsdk.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	result := callToolResult(t, session, name, args)
	if result.IsError {
		t.Fatalf("%s returned tool error: %#v", name, result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal structured content %s: %v", string(data), err)
	}
	return out
}

func assertBudgetedMCPOutput(t *testing.T, payload map[string]any, maxChars int) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if len([]rune(string(data))) > maxChars {
		t.Fatalf("payload length = %d, want <= %d: %s", len([]rune(string(data))), maxChars, string(data))
	}
	if payload["budget_truncated"] != true {
		t.Fatalf("expected budget_truncated in %#v", payload)
	}
	if payload["next_cursor"] == "" {
		t.Fatalf("expected next_cursor in %#v", payload)
	}
}

func callToolResult(t *testing.T, session *mcpsdk.ClientSession, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return result
}
