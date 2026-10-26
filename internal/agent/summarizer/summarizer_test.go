package summarizer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sashabaranov/go-openai"

	"github.com/wmentor/go-magnetar/internal/config"
)

func loadTestConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\n"+yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	return cfg
}

func TestSummarizerMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "defaults to soft when unset",
			yaml: "",
			want: ModeSoft,
		},
		{
			name: "explicit soft mode",
			yaml: "compact:\n  mode: soft\n",
			want: ModeSoft,
		},
		{
			name: "explicit compact mode",
			yaml: "compact:\n  mode: compact\n",
			want: ModeCompact,
		},
		{
			name: "case-insensitive compact mode",
			yaml: "compact:\n  mode: COMPACT\n",
			want: ModeCompact,
		},
		{
			name: "unknown mode falls back to soft",
			yaml: "compact:\n  mode: unknown\n",
			want: ModeSoft,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := loadTestConfig(t, tt.yaml)
			s := New(cfg)
			if got := s.Mode(); got != tt.want {
				t.Errorf("Mode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSummarizerCompactNoOpWhenNothingToSummarize(t *testing.T) {
	t.Parallel()

	cfg := loadTestConfig(t, "compact:\n  mode: soft\n  save_tail: 6\n")
	s := New(cfg)

	empty, err := s.Compact(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("Compact(nil) = (%v, %v), want empty nil error", empty, err)
	}

	onlySystem := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system prompt"},
	}
	got, err := s.Compact(onlySystem)
	if err != nil || len(got) != 1 {
		t.Fatalf("Compact(onlySystem) = (%v, %v), want 1 message", got, err)
	}
}

func TestSummarizerCompactStrategies(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"condensed summary"}}]}`)
	}))
	t.Cleanup(srv.Close)

	history := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "sys"},
		{Role: openai.ChatMessageRoleUser, Content: "msg1"},
		{Role: openai.ChatMessageRoleAssistant, Content: "msg2"},
		{Role: openai.ChatMessageRoleUser, Content: "msg3"},
		{Role: openai.ChatMessageRoleAssistant, Content: "msg4"},
	}

	t.Run("soft mode preserves save_tail messages", func(t *testing.T) {
		t.Parallel()

		yaml := fmt.Sprintf("profile: default\nprofiles:\n  default:\n    llm:\n      base_url: %q\n      api_key: test\n      model: gpt-test\ncompact:\n  mode: soft\n  save_tail: 2\n", srv.URL)
		cfg := loadTestConfig(t, yaml)
		s := New(cfg)

		got, err := s.Compact(history)
		if err != nil {
			t.Fatalf("Compact() error = %v", err)
		}
		if len(got) != 4 {
			t.Fatalf("len(got) = %d, want 4 ([sys, summary, msg3, msg4])", len(got))
		}
		if got[1].Content != "condensed summary" || got[2].Content != "msg3" || got[3].Content != "msg4" {
			t.Errorf("unexpected messages in soft mode: %+v", got)
		}
	})

	t.Run("compact mode summarizes entire non-system history into single message", func(t *testing.T) {
		t.Parallel()

		yaml := fmt.Sprintf("profile: default\nprofiles:\n  default:\n    llm:\n      base_url: %q\n      api_key: test\n      model: gpt-test\ncompact:\n  mode: compact\n  save_tail: 2\n", srv.URL)
		cfg := loadTestConfig(t, yaml)
		s := New(cfg)

		got, err := s.Compact(history)
		if err != nil {
			t.Fatalf("Compact() error = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2 ([sys, summary])", len(got))
		}
		if got[0].Content != "sys" || got[1].Content != "condensed summary" {
			t.Errorf("unexpected messages in compact mode: %+v", got)
		}
	})
}
