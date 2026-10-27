package indexer

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/tools/rag"
)

type savedChunk struct {
	content string
	prepend string
	part    int
}

type mockRAGStore struct {
	mu     sync.Mutex
	chunks []savedChunk
}

func (m *mockRAGStore) Name() string {
	return "mock"
}

func (m *mockRAGStore) RagSearch(_ string) string {
	return ""
}

func (m *mockRAGStore) RagSave(content string, prepend string, part int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks = append(m.chunks, savedChunk{
		content: content,
		prepend: prepend,
		part:    part,
	})
	return true
}

func newTestIndexer(t *testing.T) (*Indexer, *mockRAGStore) {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\nrag:\n  chunk:\n    size: 512\n    overlap: 64\n"), 0o644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load test config: %v", err)
	}

	store := &mockRAGStore{}
	ragTools, err := rag.New(cfg, store)
	if err != nil {
		t.Fatalf("failed to init rag tools: %v", err)
	}

	return &Indexer{
		cfg: cfg,
		rag: ragTools,
	}, store
}

func TestIndexDirectoryRequiresRecursiveFlag(t *testing.T) {
	t.Parallel()

	idx, _ := newTestIndexer(t)
	dir := t.TempDir()

	if err := idx.IndexDirectory(dir, "msg", false); err == nil {
		t.Fatal("expected error when recursive is false, got nil")
	}
}

func TestIndexDirectoryRecursiveAndExclusions(t *testing.T) {
	t.Parallel()

	idx, store := newTestIndexer(t)
	root := t.TempDir()

	writeFile := func(relPath, content string) {
		t.Helper()
		full := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}

	// Supported files across nested directories
	writeFile("notes.txt", "Top level plain text document.")
	writeFile(filepath.Join("sub", "data.csv"), "name,value\nalpha,1\nbeta,2\n")
	writeFile(filepath.Join("sub", "deep", "page.html"), "<html><body><h1>Deep Title</h1><p>Deep paragraph.</p></body></html>")

	// Unsupported file types (should be skipped with warning)
	writeFile("image.png", "not-an-indexed-type")
	writeFile(filepath.Join("sub", "script.py"), "print('skip me')")

	// Default excluded directories (.git, node_modules, __pycache__)
	writeFile(filepath.Join(".git", "secret.txt"), "should be skipped")
	writeFile(filepath.Join("node_modules", "pkg", "readme.txt"), "should be skipped")
	writeFile(filepath.Join("__pycache__", "cached.txt"), "should be skipped")

	// Custom excluded directory
	writeFile(filepath.Join("vendor_docs", "ignored.txt"), "should be skipped via custom exclude")

	// Corrupted PDF file (should log warning and continue indexing remaining files)
	writeFile(filepath.Join("sub", "corrupted.pdf"), "not a valid pdf binary")

	if err := idx.IndexDirectory(root, "API documentation", true, "vendor_docs"); err != nil {
		t.Fatalf("IndexDirectory unexpected error: %v", err)
	}

	if len(store.chunks) != 3 {
		t.Fatalf("expected 3 chunks saved (from notes.txt, data.csv, page.html), got %d", len(store.chunks))
	}

	for _, c := range store.chunks {
		if c.prepend != "API documentation" {
			t.Errorf("expected chunk prepend %q, got %q", "API documentation", c.prepend)
		}
	}
}

func TestIndexDirectoryInvalidTarget(t *testing.T) {
	t.Parallel()

	idx, _ := newTestIndexer(t)
	root := t.TempDir()

	// Non-existent path
	if err := idx.IndexDirectory(filepath.Join(root, "missing"), "", true); err == nil {
		t.Fatal("expected error for missing directory, got nil")
	}

	// Regular file passed to IndexDirectory
	filePath := filepath.Join(root, "single.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := idx.IndexDirectory(filePath, "", true); err == nil {
		t.Fatal("expected error when passing regular file to IndexDirectory, got nil")
	}
}
