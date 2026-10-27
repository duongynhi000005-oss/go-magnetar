package mcpplugin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/plugin"
)

type fakeHub struct {
	tools      []plugin.LLMTool
	goroutines []func(ctx context.Context)
}

func (h *fakeHub) RegisterTool(tool plugin.LLMTool) {
	h.tools = append(h.tools, tool)
}

func (h *fakeHub) RegisterChatCommand(_ plugin.ChatCommand)       {}
func (h *fakeHub) RegisterPreprocessor(_ plugin.PreprocessorFunc) {}
func (h *fakeHub) Go(f func(ctx context.Context)) {
	h.goroutines = append(h.goroutines, f)
}
func (h *fakeHub) Stop() {}

func TestPluginInitRegistersMCPTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","tools":[{"name":"ping","description":"Ping MCP server"}]}}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	yamlContent := fmt.Sprintf(`version: "1.0"
profile: default
mcp:
  enable: true
  servers:
    local:
      url: %q
`, srv.URL)
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	p := &Plugin{}
	hub := &fakeHub{}
	if err := p.Init(&plugin.State{Config: cfg}, hub); err != nil {
		t.Fatalf("Plugin.Init failed: %v", err)
	}

	if !p.IsEnabled() {
		t.Fatal("expected plugin to be enabled")
	}
	if len(hub.tools) != 1 {
		t.Fatalf("expected 1 registered tool, got %d", len(hub.tools))
	}
	if hub.tools[0].Definition().Function.Name != "mcp_local_ping" {
		t.Fatalf("unexpected tool name: %s", hub.tools[0].Definition().Function.Name)
	}
}
