package docx_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wmentor/go-magnetar/internal/codec/docx"
)

func TestWriteFile_RoundTrip(t *testing.T) {
	t.Parallel()

	outPath := filepath.Join(t.TempDir(), "output.docx")
	markdown := "# Project Title\n\nSome text with **bold**, *italic*, ***both***, and `inline code`.\n\n- First item\n  - Nested item\n\n1. Ordered one\n2. Ordered two\n\n```go\nfmt.Println(\"hello\")\n```\n"

	writer := &docx.WriteCodec{}
	if err := writer.WriteFile(outPath, markdown); err != nil {
		t.Fatalf("WriteFile(%q) unexpected error: %v", outPath, err)
	}

	reader := &docx.Codec{}
	got, err := reader.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) unexpected error: %v", outPath, err)
	}

	for _, want := range []string{
		"Project Title",
		"Some text with bold, italic, both, and inline code.",
		"* First item",
		"  * Nested item",
		"fmt.Println(\"hello\")",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ReadFile(%q) output %q does not contain %q", outPath, got, want)
		}
	}
}
