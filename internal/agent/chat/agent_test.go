package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/plugin"
)

func TestLoadProjectContext(t *testing.T) {
	tests := []struct {
		name         string
		files        map[string]string
		expectedName string
		expectedBody string
		expectedOk   bool
	}{
		{
			name:         "only AGENTS.md",
			files:        map[string]string{"AGENTS.md": "agents"},
			expectedName: "AGENTS.md",
			expectedBody: "agents",
			expectedOk:   true,
		},
		{
			name:         "only CLAUDE.md",
			files:        map[string]string{"CLAUDE.md": "claude"},
			expectedName: "CLAUDE.md",
			expectedBody: "claude",
			expectedOk:   true,
		},
		{
			name:         "both files, AGENTS.md wins",
			files:        map[string]string{"AGENTS.md": "agents", "CLAUDE.md": "claude"},
			expectedName: "AGENTS.md",
			expectedBody: "agents",
			expectedOk:   true,
		},
		{
			name:       "neither file",
			files:      map[string]string{},
			expectedOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}

			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatalf("open root: %v", err)
			}
			defer root.Close()

			name, content, ok := loadProjectContext(root)
			if ok != tt.expectedOk || name != tt.expectedName || string(content) != tt.expectedBody {
				t.Errorf("loadProjectContext() = (%q, %q, %v); want (%q, %q, %v)",
					name, content, ok, tt.expectedName, tt.expectedBody, tt.expectedOk)
			}
		})
	}

	t.Run("nil root", func(t *testing.T) {
		if _, _, ok := loadProjectContext(nil); ok {
			t.Errorf("loadProjectContext(nil) ok = true; want false")
		}
	})
}

func TestMatchCommand(t *testing.T) {
	tests := []struct {
		name      string
		nameInput string
		cmd       plugin.ChatCommand
		expected  bool
	}{
		{
			name:      "match exact name (case-insensitive)",
			nameInput: "help",
			cmd:       plugin.ChatCommand{Name: "Help", Aliases: []string{}},
			expected:  true,
		},
		{
			name:      "match exact name lowercase",
			nameInput: "help",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{}},
			expected:  true,
		},
		{
			name:      "match exact name uppercase",
			nameInput: "HELP",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{}},
			expected:  true,
		},
		{
			name:      "no match with different name",
			nameInput: "exit",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{}},
			expected:  false,
		},
		{
			name:      "match first alias",
			nameInput: "h",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{"h", "halp"}},
			expected:  true,
		},
		{
			name:      "match second alias",
			nameInput: "halp",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{"h", "halp"}},
			expected:  true,
		},
		{
			name:      "match alias case-insensitive",
			nameInput: "HALP",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{"h", "halp"}},
			expected:  true,
		},
		{
			name:      "no match with alias",
			nameInput: "helpme",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{"h"}},
			expected:  false,
		},
		{
			name:      "no aliases",
			nameInput: "help",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{}},
			expected:  true,
		},
		{
			name:      "empty name string, match empty command",
			nameInput: "",
			cmd:       plugin.ChatCommand{Name: "", Aliases: []string{}},
			expected:  true,
		},
		{
			name:      "empty name string, no match",
			nameInput: "",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{}},
			expected:  false,
		},
		{
			name:      "empty name with empty alias",
			nameInput: "",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{""}},
			expected:  true,
		},
		{
			name:      "match with multiple aliases",
			nameInput: "h",
			cmd:       plugin.ChatCommand{Name: "help", Aliases: []string{"a", "b", "h", "c"}},
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchCommand(tt.nameInput, tt.cmd)
			if result != tt.expected {
				t.Errorf("matchCommand(%q, {%q, %v}) = %v; want %v",
					tt.nameInput, tt.cmd.Name, tt.cmd.Aliases, result, tt.expected)
			}
		})
	}
}

func TestPrintDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{850*time.Millisecond + 400*time.Microsecond, "850ms"},
		{999*time.Millisecond + 900*time.Microsecond, "1s"},
		{1234*time.Millisecond + 600*time.Microsecond, "1.235s"},
		{9999 * time.Millisecond, "9.999s"},
		{10*time.Second + 400*time.Millisecond, "10s"},
		{75*time.Second + 600*time.Millisecond, "1m16s"},
	}
	for _, tt := range tests {
		if got := printDuration(tt.d); got != tt.want {
			t.Errorf("printDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

type testRepeatPlugin struct {
	execCalls *int32
	readCalls *int32
}

func (p *testRepeatPlugin) Init(_ *plugin.State, hub plugin.Hub) error {
	hub.RegisterTool(plugin.LLMTool{
		Definition: func() openai.Tool {
			return openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name: "exec",
				},
			}
		},
		Execute: func(_ context.Context, _ string) (string, error) {
			n := atomic.AddInt32(p.execCalls, 1)
			return fmt.Sprintf("uuid-%d", n), nil
		},
		Repeatable: true,
	})
	hub.RegisterTool(plugin.LLMTool{
		Definition: func() openai.Tool {
			return openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name: "file_read",
				},
			}
		},
		Execute: func(_ context.Context, _ string) (string, error) {
			n := atomic.AddInt32(p.readCalls, 1)
			return fmt.Sprintf("content-%d", n), nil
		},
		Repeatable: false,
	})
	return nil
}

func TestAskRepeatableAndNonRepeatableTools(t *testing.T) {
	var execCalls int32
	var readCalls int32

	plugin.Register("test_repeat_188", &testRepeatPlugin{
		execCalls: &execCalls,
		readCalls: &readCalls,
	})
	if err := plugin.InitAll(&plugin.State{}); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	defer plugin.Stop()

	var step int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		cur := atomic.AddInt32(&step, 1)
		var resp openai.ChatCompletionResponse
		if cur == 1 {
			resp = openai.ChatCompletionResponse{
				Choices: []openai.ChatCompletionChoice{
					{
						FinishReason: openai.FinishReasonToolCalls,
						Message: openai.ChatCompletionMessage{
							Role: openai.ChatMessageRoleAssistant,
							ToolCalls: []openai.ToolCall{
								{
									ID:   "call-exec-1",
									Type: openai.ToolTypeFunction,
									Function: openai.FunctionCall{
										Name:      "exec",
										Arguments: `{"command":"uuidgen"}`,
									},
								},
								{
									ID:   "call-exec-2",
									Type: openai.ToolTypeFunction,
									Function: openai.FunctionCall{
										Name:      "exec",
										Arguments: `{"command":"uuidgen"}`,
									},
								},
								{
									ID:   "call-read-1",
									Type: openai.ToolTypeFunction,
									Function: openai.FunctionCall{
										Name:      "file_read",
										Arguments: `{"filename":"same.txt"}`,
									},
								},
								{
									ID:   "call-read-2",
									Type: openai.ToolTypeFunction,
									Function: openai.FunctionCall{
										Name:      "file_read",
										Arguments: `{"filename":"same.txt"}`,
									},
								},
							},
						},
					},
				},
			}
		} else {
			resp = openai.ChatCompletionResponse{
				Choices: []openai.ChatCompletionChoice{
					{
						FinishReason: openai.FinishReasonStop,
						Message: openai.ChatCompletionMessage{
							Role:    openai.ChatMessageRoleAssistant,
							Content: "done",
						},
					},
				},
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	cfgYAML := fmt.Sprintf("version: \"1.0\"\nllm:\n  base_url: %q\n  api_key: \"test\"\n  model: \"gpt-4o\"\n", srv.URL+"/v1")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	agent, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ans, err := agent.Ask("generate two uuids and read same.txt")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans != "done" {
		t.Errorf("Ask() = %q, want %q", ans, "done")
	}
	if got := atomic.LoadInt32(&execCalls); got != 2 {
		t.Errorf("repeatable tool exec executed %d times, want 2", got)
	}
	if got := atomic.LoadInt32(&readCalls); got != 1 {
		t.Errorf("non-repeatable tool file_read executed %d times, want 1", got)
	}

	toolResults := make(map[string]string)
	for _, m := range agent.messages {
		if m.Role == openai.ChatMessageRoleTool {
			toolResults[m.ToolCallID] = m.Content
		}
	}
	if toolResults["call-exec-1"] == "" || toolResults["call-exec-2"] == "" {
		t.Errorf("expected both exec tool calls to succeed, got %v", toolResults)
	}
	if toolResults["call-exec-1"] == toolResults["call-exec-2"] {
		t.Errorf("expected distinct outputs from repeatable exec calls, got %q and %q", toolResults["call-exec-1"], toolResults["call-exec-2"])
	}
	if toolResults["call-read-1"] != "content-1" {
		t.Errorf("call-read-1 result = %q, want %q", toolResults["call-read-1"], "content-1")
	}
	wantDupErr := "error: this tool was called earlier with the same parameters"
	if toolResults["call-read-2"] != wantDupErr {
		t.Errorf("call-read-2 result = %q, want %q", toolResults["call-read-2"], wantDupErr)
	}
}
