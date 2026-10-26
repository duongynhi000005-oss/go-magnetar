package pages_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/errors"

	"github.com/wmentor/go-magnetar/internal/codec/pages"
)

func createPagesZip(t *testing.T, entries map[string]string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.pages")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}

	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}

	return path
}

func TestReadFile_PreviewTxt(t *testing.T) {
	t.Parallel()

	expected := "Apple Pages Title\n\nFirst paragraph from QuickLook preview.\n"
	path := createPagesZip(t, map[string]string{
		"QuickLook/Preview.txt": expected,
	})

	c := &pages.Codec{}
	got, err := c.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) unexpected error: %v", path, err)
	}

	if got != expected {
		t.Fatalf("ReadFile(%q) = %q, want %q", path, got, expected)
	}
}

func TestReadFile_IndexXML(t *testing.T) {
	t.Parallel()

	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<sl:document xmlns:sl="http://developer.apple.com/namespaces/sl" xmlns:sf="http://developer.apple.com/namespaces/sf">
  <sf:text-storage>
    <sf:text-body>
      <sf:p>Heading Text</sf:p>
      <sf:p>Hello <sf:span>World</sf:span><sf:br/>Second line<sf:tab/>Tabbed</sf:p>
    </sf:text-body>
  </sf:text-storage>
</sl:document>`

	path := createPagesZip(t, map[string]string{
		"index.xml": xmlContent,
	})

	c := &pages.Codec{}
	got, err := c.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) unexpected error: %v", path, err)
	}

	want := "Heading Text\n\nHello World\nSecond line\tTabbed\n\n"
	if got != want {
		t.Fatalf("ReadFile(%q) = %q, want %q", path, got, want)
	}
}

func TestReadFile_InvalidArchive(t *testing.T) {
	t.Parallel()

	path := createPagesZip(t, map[string]string{
		"other.txt": "not a pages entry",
	})

	c := &pages.Codec{}
	_, err := c.ReadFile(path)
	if !errors.Is(err, pages.ErrInvalidFileFormat) {
		t.Fatalf("ReadFile(%q) error = %v, want %v", path, err, pages.ErrInvalidFileFormat)
	}
}

func TestReadFile_MalformedIndexXML(t *testing.T) {
	t.Parallel()

	path := createPagesZip(t, map[string]string{
		"index.xml": "<sf:p>unclosed",
	})

	c := &pages.Codec{}
	_, err := c.ReadFile(path)
	if !errors.Is(err, pages.ErrInvalidFileFormat) {
		t.Fatalf("ReadFile(%q) error = %v, want %v", path, err, pages.ErrInvalidFileFormat)
	}
}

func TestReadFile_NonExistentFile(t *testing.T) {
	t.Parallel()

	c := &pages.Codec{}
	_, err := c.ReadFile(filepath.Join(t.TempDir(), "missing.pages"))
	if err == nil {
		t.Fatal("ReadFile expected error for missing file, got nil")
	}
}
