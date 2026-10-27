package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	_ "github.com/wmentor/go-magnetar/internal/codec/docx"
	_ "github.com/wmentor/go-magnetar/internal/codec/pdf"
	hstore "github.com/wmentor/go-magnetar/internal/history"
)

func TestCompletePathAndCommandInput(t *testing.T) {
	tmpDir := t.TempDir()

	files := map[string]string{
		"document.md":   "# Doc",
		"doc_notes.txt": "notes",
		"data.jsonl":    `{"source":"a.md"}`,
		"image.png":     "binary",
		"report.pdf":    "pdf",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	subDir := filepath.Join(tmpDir, "docs_dir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("index completes single supported file", func(t *testing.T) {
		partial := filepath.ToSlash(filepath.Join(tmpDir, "rep"))
		gotLine, matches := completeCommandInput("/index "+partial, nil)
		wantFile := filepath.ToSlash(filepath.Join(tmpDir, "report.pdf"))
		if len(matches) != 1 || matches[0] != wantFile {
			t.Fatalf("matches = %v, want [%s]", matches, wantFile)
		}
		if gotLine != "/index "+wantFile {
			t.Fatalf("gotLine = %q, want %q", gotLine, "/index "+wantFile)
		}
	})

	t.Run("index filters out unsupported extensions and returns multiple matches", func(t *testing.T) {
		partial := filepath.ToSlash(filepath.Join(tmpDir, "doc"))
		gotLine, matches := completeCommandInput("/index "+partial, nil)
		if len(matches) != 3 {
			t.Fatalf("expected 3 matches (doc_notes.txt, docs_dir/, document.md), got %v", matches)
		}
		for _, m := range matches {
			if strings.HasSuffix(m, "image.png") {
				t.Fatalf("unsupported image.png should be filtered out: %v", matches)
			}
		}
		if !strings.HasPrefix(gotLine, "/index "+partial) {
			t.Fatalf("unexpected gotLine: %q", gotLine)
		}
	})

	t.Run("idxtab completes only jsonl files and directories", func(t *testing.T) {
		partial := filepath.ToSlash(filepath.Join(tmpDir, "da"))
		gotLine, matches := completeCommandInput("/idxtab "+partial, nil)
		wantFile := filepath.ToSlash(filepath.Join(tmpDir, "data.jsonl"))
		if len(matches) != 1 || matches[0] != wantFile {
			t.Fatalf("matches = %v, want [%s]", matches, wantFile)
		}
		if gotLine != "/idxtab "+wantFile {
			t.Fatalf("gotLine = %q, want %q", gotLine, "/idxtab "+wantFile)
		}
	})

	t.Run("fetch completes URLs from history", func(t *testing.T) {
		hist := hstore.New("", 20)
		hist.Add("/fetch https://example.com/articles/go-rag")
		hist.Add("/index https://golang.org/doc/effective_go")

		gotLine, matches := completeCommandInput("/fetch https://ex", hist)
		wantURL := "https://example.com/articles/go-rag"
		if len(matches) != 1 || matches[0] != wantURL {
			t.Fatalf("matches = %v, want [%s]", matches, wantURL)
		}
		if gotLine != "/fetch "+wantURL {
			t.Fatalf("gotLine = %q, want %q", gotLine, "/fetch "+wantURL)
		}
	})

	t.Run("inputModel Tab key completes single match and shows dropdown on multiple", func(t *testing.T) {
		m := newInputModel()
		singlePrefix := filepath.ToSlash(filepath.Join(tmpDir, "da"))
		m.input.SetValue("/idxtab " + singlePrefix)

		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		um := updated.(*inputModel)
		want := "/idxtab " + filepath.ToSlash(filepath.Join(tmpDir, "data.jsonl"))
		if um.input.Value() != want {
			t.Fatalf("input value after Tab = %q, want %q", um.input.Value(), want)
		}
		if len(um.suggestions) != 0 {
			t.Fatalf("expected suggestions cleared on single match, got %v", um.suggestions)
		}

		multiPrefix := filepath.ToSlash(filepath.Join(tmpDir, "doc"))
		um.input.SetValue("/index " + multiPrefix)
		updated, _ = um.Update(tea.KeyMsg{Type: tea.KeyTab})
		um = updated.(*inputModel)
		if len(um.suggestions) != 3 {
			t.Fatalf("expected 3 suggestions, got %v", um.suggestions)
		}
		view := um.View()
		if !strings.Contains(view, "document.md") || !strings.Contains(view, "doc_notes.txt") {
			t.Fatalf("View() should render suggestions, got:\n%s", view)
		}

		// Subsequent non-Tab key clears suggestions
		updated, _ = um.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
		um = updated.(*inputModel)
		if len(um.suggestions) != 0 {
			t.Fatalf("expected suggestions cleared after typing, got %v", um.suggestions)
		}
	})
}
