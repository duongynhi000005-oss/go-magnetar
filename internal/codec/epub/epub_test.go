package epub_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/errors"

	"github.com/wmentor/go-magnetar/internal/codec/epub"
)

func createEPUBZip(t *testing.T, entries map[string]string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.epub")
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

func TestReadFile_MultiChapterEPUB(t *testing.T) {
	t.Parallel()

	container := `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="ch1" href="Text/chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="Text/chapter2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`

	ch1 := `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body><h1>Chapter One</h1><p>First chapter text.</p></body>
</html>`

	ch2 := `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body><h2>Chapter Two</h2><p>Second chapter text.</p></body>
</html>`

	path := createEPUBZip(t, map[string]string{
		"mimetype":                "application/epub+zip",
		"META-INF/container.xml":  container,
		"OEBPS/content.opf":       opf,
		"OEBPS/Text/chapter1.xhtml": ch1,
		"OEBPS/Text/chapter2.xhtml": ch2,
	})

	c := &epub.Codec{}
	got, err := c.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) unexpected error: %v", path, err)
	}

	if !strings.Contains(got, "Chapter One") || !strings.Contains(got, "First chapter text.") {
		t.Fatalf("ReadFile(%q) missing chapter 1 content: %q", path, got)
	}
	if !strings.Contains(got, "Chapter Two") || !strings.Contains(got, "Second chapter text.") {
		t.Fatalf("ReadFile(%q) missing chapter 2 content: %q", path, got)
	}
}

func TestReadFile_FallbackOPFWithoutContainer(t *testing.T) {
	t.Parallel()

	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <manifest>
    <item id="intro" href="intro.html" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="intro"/>
  </spine>
</package>`

	path := createEPUBZip(t, map[string]string{
		"book.opf":   opf,
		"intro.html": "<html><body><p>Fallback OPF content.</p></body></html>",
	})

	c := &epub.Codec{}
	got, err := c.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) unexpected error: %v", path, err)
	}

	if !strings.Contains(got, "Fallback OPF content.") {
		t.Fatalf("ReadFile(%q) = %q, want substring %q", path, got, "Fallback OPF content.")
	}
}

func TestReadFile_InvalidArchive(t *testing.T) {
	t.Parallel()

	path := createEPUBZip(t, map[string]string{
		"readme.txt": "not an epub",
	})

	c := &epub.Codec{}
	_, err := c.ReadFile(path)
	if !errors.Is(err, epub.ErrInvalidFileFormat) {
		t.Fatalf("ReadFile(%q) error = %v, want %v", path, err, epub.ErrInvalidFileFormat)
	}
}

func TestReadFile_NonExistentFile(t *testing.T) {
	t.Parallel()

	c := &epub.Codec{}
	_, err := c.ReadFile(filepath.Join(t.TempDir(), "missing.epub"))
	if err == nil {
		t.Fatal("ReadFile expected error for missing file, got nil")
	}
}
