package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wmentor/go-magnetar/internal/config"
)

func TestHTTPMCPServer_JSONAndSSE(t *testing.T) {
	var seenSessionID string
	var seenAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
			seenSessionID = sid
		}

		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "session-abc-123")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"test-http","version":"1.0.0"}}}`, req.ID)

		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)

		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"tools\":[{\"name\":\"echo_text\",\"description\":\"Echo text back\",\"inputSchema\":{\"type\":\"object\",\"properties\":{\"message\":{\"type\":\"string\"}},\"required\":[\"message\"]}}]}}\n\n", req.ID)

		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			raw, _ := json.Marshal(req.Params)
			_ = json.Unmarshal(raw, &params)

			if params.Name == "fail_tool" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"isError":true,"content":[{"type":"text","text":"tool failed intentionally"}]}}`, req.ID)
				return
			}

			msg, _ := params.Arguments["message"].(string)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"Echo: %s"}]}}`, req.ID, msg)

		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"method not found"}}`, req.ID)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	yamlContent := fmt.Sprintf(`version: "1.0"
profile: default
mcp:
  enable: true
  servers:
    demo_http:
      transport: http
      url: %q
      headers:
        Authorization: "Bearer secret-token"
      timeout: 10
`, srv.URL)
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	mgr, err := NewManager(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	if seenAuth != "Bearer secret-token" {
		t.Fatalf("expected Authorization header 'Bearer secret-token', got %q", seenAuth)
	}
	if seenSessionID != "session-abc-123" {
		t.Fatalf("expected Mcp-Session-Id 'session-abc-123' to be echoed on subsequent requests, got %q", seenSessionID)
	}

	tools := mgr.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 discovered tool, got %d", len(tools))
	}

	bound := tools[0]
	def := bound.Definition()
	if def.Function == nil || def.Function.Name != "mcp_demo_http_echo_text" {
		t.Fatalf("unexpected function definition: %+v", def.Function)
	}
	if !strings.Contains(def.Function.Description, "[MCP:demo_http] Echo text back") {
		t.Fatalf("unexpected description: %q", def.Function.Description)
	}

	res, err := bound.Execute(context.Background(), `{"message":"hello magnetar"}`)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if res != "Echo: hello magnetar" {
		t.Fatalf("expected 'Echo: hello magnetar', got %q", res)
	}
}

func TestStdioMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_STDIO_HELPER") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			// Emit an unsolicited notification before the response to verify notification skipping.
			fmt.Println(`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info"}}`)
			fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"stdio-helper","version":"1.0.0"}}}`+"\n", req.ID)
		case "notifications/initialized":
			// No response for notifications.
		case "tools/list":
			fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"add","description":"Add two numbers","inputSchema":{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}}}}]}}`+"\n", req.ID)
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			raw, _ := json.Marshal(req.Params)
			_ = json.Unmarshal(raw, &params)
			a, _ := params.Arguments["a"].(float64)
			b, _ := params.Arguments["b"].(float64)
			prefix := os.Getenv("MCP_TEST_PREFIX")
			fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"%s%g"}]}}`+"\n", req.ID, prefix, a+b)
		}
	}
	os.Exit(0)
}

func TestStdioMCPServer(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	sc := ServerConfig{
		Name:      "calc",
		Transport: "stdio",
		Command:   exe,
		Args:      []string{"-test.run=^TestStdioMCPHelperProcess$"},
		Env: map[string]string{
			"GO_WANT_MCP_STDIO_HELPER": "1",
			"MCP_TEST_PREFIX":          "sum=",
		},
	}

	client := NewStdioClient(sc)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "add" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	out, err := client.CallTool(ctx, "add", map[string]any{"a": 19, "b": 23})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if out != "sum=42" {
		t.Fatalf("expected 'sum=42', got %q", out)
	}
}
