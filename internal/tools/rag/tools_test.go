package rag_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/tools/rag"
)

func TestContentUUID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "empty string",
			content: "",
		},
		{
			name:    "simple text",
			content: "hello world",
		},
		{
			name:    "text with newlines",
			content: "line1\nline2\nline3",
		},
		{
			name:    "text with spaces",
			content: "hello   world   test",
		},
		{
			name:    "unicode text",
			content: "Привет мир",
		},
		{
			name:    "same content produces same UUID",
			content: "repeat this text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			uuid := rag.ContentUUID(tt.content)
			if uuid == "" {
				t.Error("ContentUUID() returned empty string")
			}

			if uuid != rag.ContentUUID(tt.content) {
				t.Error("ContentUUID() is not deterministic")
			}
		})
	}
}

func TestStaticDefinitionSearch(t *testing.T) {
	t.Parallel()

	tool := rag.StaticDefinitionSearch()

	if tool.Type != "function" {
		t.Errorf("Tool.Type = %s, want %s", tool.Type, "function")
	}

	if tool.Function == nil {
		t.Fatal("Tool.Function is nil")
	}

	if tool.Function.Name != "rag_search" {
		t.Errorf("Tool.Function.Name = %s, want %s", tool.Function.Name, "rag_search")
	}

	if tool.Function.Description != "Search the knowledge base for relevant information" {
		t.Errorf("Tool.Function.Description = %s, want %s",
			tool.Function.Description, "Search the knowledge base for relevant information")
	}

	params := tool.Function.Parameters
	if params == nil {
		t.Fatal("Tool.Function.Parameters is nil")
	}

	propsMap, ok := params.(map[string]any)
	if !ok {
		t.Fatal("Tool.Function.Parameters is not map[string]any")
	}

	props, ok := propsMap["properties"].(map[string]any)
	if !ok {
		t.Fatal("Tool.Function.Parameters.properties is not map[string]any")
	}

	if _, ok := props["query"]; !ok {
		t.Error("Tool.Function.Parameters.properties.query is missing")
	}

	queryProp, ok := props["query"].(map[string]any)
	if !ok {
		t.Fatal("Tool.Function.Parameters.properties.query is not map[string]any")
	}

	if queryProp["type"] != "string" {
		t.Errorf("Tool.Function.Parameters.properties.query.type = %v, want %v", queryProp["type"], "string")
	}

	if queryProp["description"] != "Search query" {
		t.Errorf("Tool.Function.Parameters.properties.query.description = %v, want %v",
			queryProp["description"], "Search query")
	}

	req, ok := propsMap["required"].([]string)
	if !ok {
		t.Fatalf("Tool.Function.Parameters.required is not []string, got %T", propsMap["required"])
	}

	if len(req) != 1 {
		t.Errorf("Tool.Function.Parameters.required has %d items, want %d", len(req), 1)
	}

	if req[0] != "query" {
		t.Errorf("Tool.Function.Parameters.required[0] = %v, want %v", req[0], "query")
	}
}

func TestNewEmbeddingClient(t *testing.T) {
	t.Parallel()

	t.Run("nil config", func(t *testing.T) {
		t.Parallel()

		client := rag.NewEmbeddingClient(nil)
		if client == nil {
			t.Fatal("NewEmbeddingClient(nil) returned nil")
		}
	})

	t.Run("configured client", func(t *testing.T) {
		t.Parallel()

		cfgPath := filepath.Join(t.TempDir(), "config.yml")
		yaml := "version: \"1.0\"\nrag:\n  llm:\n    api_key: \"test-key\"\n    base_url: \"http://localhost:11434/v1\"\n"
		if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}

		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatal(err)
		}

		client := rag.NewEmbeddingClient(cfg)
		if client == nil {
			t.Fatal("NewEmbeddingClient(cfg) returned nil")
		}
	})
}
