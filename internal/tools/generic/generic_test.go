package generic

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/plugin"
	"github.com/wmentor/go-magnetar/internal/response"
)

func newTestTools(t *testing.T, yaml string, readOnly bool) *GenericTools {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("version: \"1.0\"\n"+yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	responseStore := response.New(plugin.DefaultResponseStoreSize)

	return New(cfg, nil, &plugin.State{Config: cfg, ReadOnly: readOnly, ResponseStore: responseStore})
}

func TestSSHExecEnableFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		yaml     string
		readOnly bool
		want     string
	}{
		{
			name: "disabled by default",
			yaml: "",
			want: "error: ssh disabled",
		},
		{
			name: "explicitly disabled",
			yaml: "ssh:\n  enable: false\n",
			want: "error: ssh disabled",
		},
		{
			// With ssh enabled the call must get past the enable check.
			// An empty address stops it right after, before any network access.
			name: "enabled",
			yaml: "ssh:\n  enable: true\n",
			want: "error: no address provided",
		},
		{
			name:     "enabled but read-only mode",
			yaml:     "ssh:\n  enable: true\n",
			readOnly: true,
			want:     "error: the ssh tool is forbidden in read-only mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newTestTools(t, tt.yaml, tt.readOnly)

			if got := g.SSHExec("ls", "", "", "", ""); got != tt.want {
				t.Errorf("SSHExec() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSystemDate(t *testing.T) {
	t.Parallel()

	g := newTestTools(t, "", false)

	// date(1) prints whole seconds, so truncate the lower bound.
	before := time.Now().Truncate(time.Second)
	got := g.SystemDate()
	after := time.Now()

	parsed, err := time.Parse(time.UnixDate, got)
	if err != nil {
		t.Fatalf("SystemDate() = %q, want time.UnixDate format: %v", got, err)
	}

	if parsed.Before(before) || parsed.After(after) {
		t.Errorf("SystemDate() = %q, want a time between %v and %v", got, before, after)
	}
}

func TestFileEditSingleOccurrence(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	rootFS, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFS.Close()

	responseStore := response.New(plugin.DefaultResponseStoreSize)
	g := New(cfg, rootFS, &plugin.State{Config: cfg, ReadOnly: false, ResponseStore: responseStore})

	// Create a test file with single occurrence
	testFile := "test.txt"
	testContent := "Hello World\nThis is a test file"
	if err := rootFS.WriteFile(testFile, []byte(testContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test successful edit with single occurrence
	result, ok := g.FileEdit(testFile, "World", "Universe")
	if !ok {
		t.Errorf("FileEdit() returned false for single occurrence, want true: %s", result)
	}
	if !strings.Contains(result, "File edited successfully") {
		t.Errorf("FileEdit() result = %q, want to contain 'File edited successfully'", result)
	}

	// Verify the file was modified
	content, err := rootFS.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	expected := "Hello Universe\nThis is a test file"
	if string(content) != expected {
		t.Errorf("File content after edit = %q, want %q", string(content), expected)
	}
}

func TestFileEditMultipleOccurrences(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	rootFS, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFS.Close()

	responseStore := response.New(plugin.DefaultResponseStoreSize)
	g := New(cfg, rootFS, &plugin.State{Config: cfg, ReadOnly: false, ResponseStore: responseStore})

	// Create a test file with multiple occurrences
	testFile := "test.txt"
	testContent := "Hello World\nThis is a test file\nHello World"
	if err := rootFS.WriteFile(testFile, []byte(testContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test edit with multiple occurrences - should fail
	result, ok := g.FileEdit(testFile, "World", "Universe")
	if ok {
		t.Errorf("FileEdit() returned true for multiple occurrences, want false")
	}
	if !strings.Contains(result, "appears multiple times") && !strings.Contains(result, "appears") {
		t.Errorf("FileEdit() result = %q, want to contain 'appears multiple times'", result)
	}

	// Verify the file was NOT modified
	content, err := rootFS.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "Hello World\nThis is a test file\nHello World" {
		t.Errorf("File content after edit should not change")
	}
}

func TestFileEditNotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	rootFS, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFS.Close()

	responseStore := response.New(plugin.DefaultResponseStoreSize)
	g := New(cfg, rootFS, &plugin.State{Config: cfg, ReadOnly: false, ResponseStore: responseStore})

	// Create a test file
	testFile := "test.txt"
	if err := rootFS.WriteFile(testFile, []byte("Hello World"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test edit with non-existent text - returns ok=true with warning
	result, ok := g.FileEdit(testFile, "NotExists", "Modified")
	// FileEdit returns ok=true even when text is not found (it's a warning, not an error)
	if !ok {
		t.Errorf("FileEdit() returned false for non-existent text, want true (warning)")
	}
	// Should get a warning, not an error
	if strings.Contains(result, "error") {
		t.Errorf("FileEdit() result = %q, should not contain 'error'", result)
	}
}

func TestFileEditMultipleAllStrategy(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	rootFS, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFS.Close()

	responseStore := response.New(plugin.DefaultResponseStoreSize)
	g := New(cfg, rootFS, &plugin.State{Config: cfg, ReadOnly: false, ResponseStore: responseStore})

	// Create a test file
	testFile := "test.txt"
	testContent := "Hello World\nHello World\nHello World"
	if err := rootFS.WriteFile(testFile, []byte(testContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test multiple edits with "all" strategy
	edits := []FileEditEdit{
		{OldText: "World", NewText: "Universe"},
		{OldText: "Hello", NewText: "Hi"},
	}

	result, ok := g.FileEditMultiple(testFile, edits, "all")
	if !ok {
		t.Errorf("FileEditMultiple() returned false, want true")
	}
	if !strings.Contains(result, "File edited successfully") {
		t.Errorf("FileEditMultiple() result = %q, want to contain 'File edited successfully'", result)
	}

	// Verify the file was modified
	content, err := rootFS.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	expected := "Hi Universe\nHi Universe\nHi Universe"
	if string(content) != expected {
		t.Errorf("File content after edit = %q, want %q", string(content), expected)
	}
}

func TestFileEditMultipleFirstStrategy(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	rootFS, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFS.Close()

	responseStore := response.New(plugin.DefaultResponseStoreSize)
	g := New(cfg, rootFS, &plugin.State{Config: cfg, ReadOnly: false, ResponseStore: responseStore})

	// Create a test file
	testFile := "test.txt"
	testContent := "Hello World\nHello World\nHello World"
	if err := rootFS.WriteFile(testFile, []byte(testContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test multiple edits with "first" strategy
	edits := []FileEditEdit{
		{OldText: "World", NewText: "Universe"},
		{OldText: "Hello", NewText: "Hi"},
	}

	result, ok := g.FileEditMultiple(testFile, edits, "first")
	if !ok {
		t.Errorf("FileEditMultiple() returned false, want true")
	}
	_ = result // Suppress unused variable warning

	// Verify the file was modified - only first occurrence should be changed
	content, err := rootFS.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	expected := "Hi Universe\nHello World\nHello World"
	if string(content) != expected {
		t.Errorf("File content after edit = %q, want %q", string(content), expected)
	}
}

func TestFileEditMultipleNoEdits(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	rootFS, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFS.Close()

	responseStore := response.New(plugin.DefaultResponseStoreSize)
	g := New(cfg, rootFS, &plugin.State{Config: cfg, ReadOnly: false, ResponseStore: responseStore})

	// Create a test file
	testFile := "test.txt"
	if err := rootFS.WriteFile(testFile, []byte("Hello World"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test with non-existent text
	edits := []FileEditEdit{
		{OldText: "NotExists", NewText: "Modified"},
	}

	result, ok := g.FileEditMultiple(testFile, edits, "all")
	if !ok {
		t.Errorf("FileEditMultiple() returned false, want true")
	}
	if !strings.Contains(result, "no changes were applied") {
		t.Errorf("FileEditMultiple() result = %q, want to contain 'no changes were applied'", result)
	}
}

func newTestToolsWithRoot(t *testing.T) (*GenericTools, *os.Root) {
	t.Helper()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	rootFS, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rootFS.Close() })

	responseStore := response.New(plugin.DefaultResponseStoreSize)
	return New(cfg, rootFS, &plugin.State{Config: cfg, ReadOnly: false, ResponseStore: responseStore}), rootFS
}

// TestFileEditDispatch verifies that file_edit is reachable via GenericTools.Dispatch —
// the same code path used by the plugin.
func TestFileEditDispatch(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	if err := rootFS.WriteFile("hello.txt", []byte("Hello World"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := g.Dispatch("file_edit", `{"filename":"hello.txt","old_text":"World","new_text":"Go"}`)
	if !strings.Contains(result, "File edited successfully") {
		t.Errorf("Dispatch(file_edit) = %q, want 'File edited successfully'", result)
	}

	data, err := rootFS.ReadFile("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "Hello Go" {
		t.Errorf("file content after Dispatch = %q, want %q", string(data), "Hello Go")
	}
}

// TestFileEditMultipleDispatch verifies that file_edit_multiple is reachable via GenericTools.Dispatch.
func TestFileEditMultipleDispatch(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	if err := rootFS.WriteFile("hi.txt", []byte("foo bar foo"), 0o644); err != nil {
		t.Fatal(err)
	}

	args := `{"filename":"hi.txt","edits":[{"old_text":"foo","new_text":"baz"}],"strategy":"all"}`
	result := g.Dispatch("file_edit_multiple", args)
	if !strings.Contains(result, "File edited successfully") {
		t.Errorf("Dispatch(file_edit_multiple) = %q, want 'File edited successfully'", result)
	}

	data, err := rootFS.ReadFile("hi.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "baz bar baz" {
		t.Errorf("file content after Dispatch = %q, want %q", string(data), "baz bar baz")
	}
}

// TestFileEditBlocksCodecFormats verifies that file_edit rejects files with codec-handled extensions.
func TestFileEditBlocksCodecFormats(t *testing.T) {
	t.Parallel()

	codecExts := []string{".docx", ".pdf", ".odt", ".xlsx", ".pptx", ".csv", ".tsv", ".html", ".htm"}

	for _, ext := range codecExts {
		t.Run(ext, func(t *testing.T) {
			t.Parallel()

			g, rootFS := newTestToolsWithRoot(t)

			fname := "testfile" + ext
			if err := rootFS.WriteFile(fname, []byte("some content"), 0o644); err != nil {
				t.Fatal(err)
			}

			result, ok := g.FileEdit(fname, "some", "other")
			if ok {
				t.Errorf("FileEdit(%s) returned ok=true, want false for codec format", ext)
			}
			if !strings.Contains(result, "file_edit cannot edit") {
				t.Errorf("FileEdit(%s) = %q, want to contain 'file_edit cannot edit'", ext, result)
			}
		})
	}
}

// TestFileEditSequentialSafety verifies that two sequential file_edit calls on the
// same file produce the correct cumulative result (second edit sees the output of the first).
func TestFileEditSequentialSafety(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	if err := rootFS.WriteFile("seq.txt", []byte("alpha beta gamma"), 0o644); err != nil {
		t.Fatal(err)
	}

	r1 := g.Dispatch("file_edit", `{"filename":"seq.txt","old_text":"alpha","new_text":"one"}`)
	if !strings.Contains(r1, "File edited successfully") {
		t.Fatalf("first Dispatch(file_edit) = %q", r1)
	}

	r2 := g.Dispatch("file_edit", `{"filename":"seq.txt","old_text":"beta","new_text":"two"}`)
	if !strings.Contains(r2, "File edited successfully") {
		t.Fatalf("second Dispatch(file_edit) = %q", r2)
	}

	data, err := rootFS.ReadFile("seq.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one two gamma" {
		t.Errorf("file content = %q, want %q", string(data), "one two gamma")
	}
}

func TestStaticDefinitions(t *testing.T) {
	t.Parallel()

	// Test StaticDefinitionFileEdit
	def := StaticDefinitionFileEdit()
	if def.Type != "function" {
		t.Errorf("StaticDefinitionFileEdit().Type = %q, want %q", def.Type, "function")
	}
	if def.Function.Name != "file_edit" {
		t.Errorf("StaticDefinitionFileEdit().Function.Name = %q, want %q", def.Function.Name, "file_edit")
	}

	// Verify required parameters
	params, ok := def.Function.Parameters.(map[string]any)["required"].([]string)
	if !ok {
		t.Errorf("StaticDefinitionFileEdit() required params is not a string slice")
	} else if len(params) != 3 {
		t.Errorf("StaticDefinitionFileEdit() required params count = %d, want 3", len(params))
	}

	// Test StaticDefinitionFileEditMultiple
	defMulti := StaticDefinitionFileEditMultiple()
	if defMulti.Type != "function" {
		t.Errorf("StaticDefinitionFileEditMultiple().Type = %q, want %q", defMulti.Type, "function")
	}
	if defMulti.Function.Name != "file_edit_multiple" {
		t.Errorf("StaticDefinitionFileEditMultiple().Function.Name = %q, want %q", defMulti.Function.Name, "file_edit_multiple")
	}

	// Verify required parameters
	params, ok = defMulti.Function.Parameters.(map[string]any)["required"].([]string)
	if !ok {
		t.Errorf("StaticDefinitionFileEditMultiple() required params is not a string slice")
	} else if len(params) != 2 {
		t.Errorf("StaticDefinitionFileEditMultiple() required params count = %d, want 2", len(params))
	}
}

func gzipCompress(t *testing.T, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFileReadGzipCSV(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	csvContent := "name,age\nAlice,30\nBob,25\n"
	gzData := gzipCompress(t, []byte(csvContent))

	if err := rootFS.WriteFile("data.csv.gz", gzData, 0o644); err != nil {
		t.Fatal(err)
	}

	content, ok := g.FileRead("data.csv.gz", 0, 0)
	if !ok {
		t.Fatalf("FileRead(data.csv.gz) failed")
	}

	if !strings.Contains(content, "name") || !strings.Contains(content, "Alice") || !strings.Contains(content, "Bob") {
		t.Errorf("FileRead(data.csv.gz) unexpected output: %s", content)
	}

	// Also verify via Dispatch
	disp := g.Dispatch("file_read", `{"filename":"data.csv.gz"}`)
	if !strings.Contains(disp, "Alice") {
		t.Errorf("Dispatch(file_read) unexpected output: %s", disp)
	}
}

func TestFileReadGzipPDF(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	pdfPath := filepath.Join("..", "..", "codec", "pdf", "testdata", "test.pdf")
	rawPDF, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatalf("failed to read test pdf fixture: %v", err)
	}

	expectedTxtPath := filepath.Join("..", "..", "codec", "pdf", "testdata", "test.txt")
	expectedTxt, err := os.ReadFile(expectedTxtPath)
	if err != nil {
		t.Fatalf("failed to read expected txt fixture: %v", err)
	}

	gzPDF := gzipCompress(t, rawPDF)
	if err := rootFS.WriteFile("report.pdf.gz", gzPDF, 0o644); err != nil {
		t.Fatal(err)
	}

	content, ok := g.FileRead("report.pdf.gz", 0, 0)
	if !ok {
		t.Fatalf("FileRead(report.pdf.gz) failed")
	}

	expected := strings.ReplaceAll(string(expectedTxt), "\r\n", "\n")
	actual := strings.ReplaceAll(content, "\r\n", "\n")
	if actual != expected {
		t.Errorf("FileRead(report.pdf.gz) = %q, want %q", actual, expected)
	}
}

func TestFileReadUncompressedSupported(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	csvContent := "product,price\nApple,1.5\nBanana,0.8\n"
	if err := rootFS.WriteFile("data.csv", []byte(csvContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, ok := g.FileRead("data.csv", 0, 0)
	if !ok {
		t.Fatalf("FileRead(data.csv) failed")
	}

	if !strings.Contains(content, "product") || !strings.Contains(content, "Apple") {
		t.Errorf("FileRead(data.csv) unexpected output: %s", content)
	}
}

func TestFileReadNonGzFilenameWithGzipContent(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	csvContent := "city,country\nParis,France\nTokyo,Japan\n"
	gzData := gzipCompress(t, []byte(csvContent))

	// Note: filename is "table.csv" without .gz extension, but content is gzip compressed
	if err := rootFS.WriteFile("table.csv", gzData, 0o644); err != nil {
		t.Fatal(err)
	}

	content, ok := g.FileRead("table.csv", 0, 0)
	if !ok {
		t.Fatalf("FileRead(table.csv with gzip data) failed")
	}

	if !strings.Contains(content, "Paris") || !strings.Contains(content, "France") {
		t.Errorf("FileRead(table.csv with gzip data) unexpected output: %s", content)
	}
}

func TestFileReadMalformedGzip(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	// Case 1: .gz extension with missing/invalid gzip header (plain text pretending to be .gz)
	if err := rootFS.WriteFile("report.pdf.gz", []byte("plain text with no gzip magic"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, ok := g.FileRead("report.pdf.gz", 0, 0)
	if ok {
		t.Errorf("FileRead(report.pdf.gz with plain text) expected failure, got ok=true: %s", content)
	}

	// Case 2: .gz extension with empty file (0 bytes)
	if err := rootFS.WriteFile("empty.csv.gz", []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	content, ok = g.FileRead("empty.csv.gz", 0, 0)
	if ok {
		t.Errorf("FileRead(empty.csv.gz) expected failure, got ok=true: %s", content)
	}

	// Case 3: gzip magic bytes with invalid header
	if err := rootFS.WriteFile("invalid_header.csv.gz", []byte{0x1f, 0x8b, 0xff, 0xff, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	content, ok = g.FileRead("invalid_header.csv.gz", 0, 0)
	if ok {
		t.Errorf("FileRead(invalid_header.csv.gz) expected failure, got ok=true: %s", content)
	}

	// Case 4: valid header with corrupt body
	validGz := gzipCompress(t, []byte("col1,col2\nval1,val2\n"))
	corruptGz := append(validGz[:10], []byte("not valid compressed body")...)
	if err := rootFS.WriteFile("corrupt_body.csv.gz", corruptGz, 0o644); err != nil {
		t.Fatal(err)
	}
	content, ok = g.FileRead("corrupt_body.csv.gz", 0, 0)
	if ok {
		t.Errorf("FileRead(corrupt_body.csv.gz) expected failure, got ok=true: %s", content)
	}

	// Case 5: plain text file with corrupt gzip
	if err := rootFS.WriteFile("malformed.txt.gz", []byte{0x1f, 0x8b, 0x00, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	content, ok = g.FileRead("malformed.txt.gz", 0, 0)
	if ok {
		t.Errorf("FileRead(malformed.txt.gz) expected failure, got ok=true: %s", content)
	}
}

func listTempFilesWithExt(t *testing.T, ext string) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "magnetar-*"+ext))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestFileReadTempFileCleanup(t *testing.T) {
	g, rootFS := newTestToolsWithRoot(t)

	// 1. Success case: temp file should be removed after successful read
	tsvContent := "item\tqty\nWidget\t10\n"
	gzData := gzipCompress(t, []byte(tsvContent))
	if err := rootFS.WriteFile("cleanup_success.tsv.gz", gzData, 0o644); err != nil {
		t.Fatal(err)
	}

	beforeSuccess := listTempFilesWithExt(t, ".tsv")
	content, ok := g.FileRead("cleanup_success.tsv.gz", 0, 0)
	if !ok || !strings.Contains(content, "Widget") {
		t.Fatalf("FileRead(cleanup_success.tsv.gz) failed: %v, ok=%v", content, ok)
	}
	afterSuccess := listTempFilesWithExt(t, ".tsv")

	if len(afterSuccess) > len(beforeSuccess) {
		t.Errorf("leak detected on success: temp file was not removed: %v", afterSuccess)
	}

	// 2. Failure case (corrupt gzip): temp file should be removed after failed decompression
	if err := rootFS.WriteFile("cleanup_corrupt.tsv.gz", append(gzData[:10], []byte("corrupt data")...), 0o644); err != nil {
		t.Fatal(err)
	}

	beforeCorrupt := listTempFilesWithExt(t, ".tsv")
	_, ok = g.FileRead("cleanup_corrupt.tsv.gz", 0, 0)
	if ok {
		t.Errorf("FileRead(cleanup_corrupt.tsv.gz) expected failure, got ok=true")
	}
	afterCorrupt := listTempFilesWithExt(t, ".tsv")

	if len(afterCorrupt) > len(beforeCorrupt) {
		t.Errorf("leak detected on decompression failure: temp file was not removed: %v", afterCorrupt)
	}

	// 3. Failure case (codec error on decompressed content):
	// Decompress succeeds, but content is invalid for that format (e.g. invalid DOCX)
	gzInvalidDocx := gzipCompress(t, []byte("this is not a valid docx file at all"))
	if err := rootFS.WriteFile("cleanup_codec_err.docx.gz", gzInvalidDocx, 0o644); err != nil {
		t.Fatal(err)
	}

	beforeCodecErr := listTempFilesWithExt(t, ".docx")
	_, ok = g.FileRead("cleanup_codec_err.docx.gz", 0, 0)
	if ok {
		t.Errorf("FileRead(cleanup_codec_err.docx.gz) expected failure, got ok=true")
	}
	afterCodecErr := listTempFilesWithExt(t, ".docx")

	if len(afterCodecErr) > len(beforeCodecErr) {
		t.Errorf("leak detected on codec failure: temp file was not removed: %v", afterCodecErr)
	}
}

func TestFileReadPlainTextLimitAndOffset(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	text := "line 1\nline 2\nline 3\nline 4\nline 5"

	// Uncompressed test
	if err := rootFS.WriteFile("lines.txt", []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		limit  int
		offset int
		want   string
	}{
		{name: "all lines", limit: 0, offset: 0, want: "line 1\nline 2\nline 3\nline 4\nline 5"},
		{name: "limit 2 offset 0", limit: 2, offset: 0, want: "line 1\nline 2"},
		{name: "limit 2 offset 2", limit: 2, offset: 2, want: "line 3\nline 4"},
		{name: "limit 0 offset 3", limit: 0, offset: 3, want: "line 4\nline 5"},
		{name: "limit 10 offset 1", limit: 10, offset: 1, want: "line 2\nline 3\nline 4\nline 5"},
	}

	for _, tt := range tests {
		t.Run("uncompressed_"+tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := g.FileRead("lines.txt", tt.limit, tt.offset)
			if !ok {
				t.Fatalf("FileRead failed")
			}
			if got != tt.want {
				t.Errorf("FileRead() = %q, want %q", got, tt.want)
			}
		})
	}

	// Compressed plain-text test
	gzText := gzipCompress(t, []byte(text))
	if err := rootFS.WriteFile("lines.txt.gz", gzText, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run("compressed_"+tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := g.FileRead("lines.txt.gz", tt.limit, tt.offset)
			if !ok {
				t.Fatalf("FileRead failed")
			}
			if got != tt.want {
				t.Errorf("FileRead() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileReadUnrecoverableExtension(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)

	text := "plain text inside abc.gz"
	gzData := gzipCompress(t, []byte(text))

	if err := rootFS.WriteFile("abc.gz", gzData, 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := g.FileRead("abc.gz", 0, 0)
	if !ok {
		t.Fatalf("FileRead(abc.gz) failed")
	}
	if got != text {
		t.Errorf("FileRead(abc.gz) = %q, want %q", got, text)
	}
}

func TestFileReadErrors(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)
	tmpDir := rootFS.Name()

	// Non-existent file
	if _, ok := g.FileRead("does_not_exist.txt", 0, 0); ok {
		t.Errorf("FileRead(does_not_exist.txt) expected failure")
	}

	// Directory
	subDir := filepath.Join(tmpDir, "somedir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.FileRead("somedir", 0, 0); ok {
		t.Errorf("FileRead(somedir) expected failure")
	}

	// Nil root
	cfg := g.cfg
	nilRootTools := New(cfg, nil, g.state)
	if _, ok := nilRootTools.FileRead("any.txt", 0, 0); ok {
		t.Errorf("FileRead with nil root expected failure")
	}
}

func createTestFileTree(t *testing.T, rootDir string, files map[string]string) {
	t.Helper()

	for relPath, content := range files {
		fullPath := filepath.Join(rootDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("failed to create parent directory for %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write test file %s: %v", relPath, err)
		}
	}
}

func normalizeRelPaths(t *testing.T, paths []string) []string {
	t.Helper()

	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.ToSlash(p)
	}
	return out
}

func TestFileList(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)
	if err := os.Remove(filepath.Join(rootFS.Name(), "config.yml")); err != nil {
		t.Fatal(err)
	}
	createTestFileTree(t, rootFS.Name(), map[string]string{
		"main.go":                         "package main",
		"README.md":                       "# readme",
		"test_runner.py":                  "print('ok')",
		"src/app.go":                      "package src",
		"src/utils/helper.go":             "package utils",
		"src/utils/test_helper.py":        "print('helper')",
		"src/utils/notes.txt":             "notes",
		"docs/guide_v1.md":                "guide",
		"special/file-with_chars 1.log":   "log",
		".git/config":                     "git config",
		".hidden_dir/secret.go":           "package secret",
		"src/.cache/ignored.go":           "package ignored",
	})

	tests := []struct {
		name   string
		filter *FileListFilter
		want   []string
	}{
		{
			name:   "nil filter returns all non-hidden-dir files recursively",
			filter: nil,
			want: []string{
				"README.md",
				"docs/guide_v1.md",
				"main.go",
				"special/file-with_chars 1.log",
				"src/app.go",
				"src/utils/helper.go",
				"src/utils/notes.txt",
				"src/utils/test_helper.py",
				"test_runner.py",
			},
		},
		{
			name:   "empty filter string returns all files",
			filter: &FileListFilter{Filter: ""},
			want: []string{
				"README.md",
				"docs/guide_v1.md",
				"main.go",
				"special/file-with_chars 1.log",
				"src/app.go",
				"src/utils/helper.go",
				"src/utils/notes.txt",
				"src/utils/test_helper.py",
				"test_runner.py",
			},
		},
		{
			name:   "glob pattern matching go files",
			filter: &FileListFilter{Filter: "*.go"},
			want: []string{
				"main.go",
				"src/app.go",
				"src/utils/helper.go",
			},
		},
		{
			name:   "glob pattern matching prefix test_*.py",
			filter: &FileListFilter{Filter: "test_*.py"},
			want: []string{
				"src/utils/test_helper.py",
				"test_runner.py",
			},
		},
		{
			name:   "character class and single-char wildcard pattern",
			filter: &FileListFilter{Filter: "guide_v?.m[a-z]"},
			want: []string{
				"docs/guide_v1.md",
			},
		},
		{
			name:   "special characters in filename",
			filter: &FileListFilter{Filter: "*.log"},
			want: []string{
				"special/file-with_chars 1.log",
			},
		},
		{
			name:   "non-matching pattern returns empty slice",
			filter: &FileListFilter{Filter: "*.rs"},
			want:   nil,
		},
		{
			name:   "case sensitivity of glob pattern",
			filter: &FileListFilter{Filter: "readme.md"},
			want:   nil,
		},
		{
			name:   "invalid glob syntax returns empty slice without panic",
			filter: &FileListFilter{Filter: "[unclosed"},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := normalizeRelPaths(t, g.FileList(tt.filter))
			if len(got) != len(tt.want) {
				t.Fatalf("FileList(%+v) count = %d (%v), want %d (%v)", tt.filter, len(got), got, len(tt.want), tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("FileList(%+v)[%d] = %q, want %q", tt.filter, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFileListEmptyDirectory(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)
	if err := os.Remove(filepath.Join(rootFS.Name(), "config.yml")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootFS.Name(), "empty_sub/nested_empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := g.FileList(nil)
	if len(got) != 0 {
		t.Errorf("FileList(nil) on empty directory tree = %v, want empty", got)
	}
}

func TestFileListDispatch(t *testing.T) {
	t.Parallel()

	g, rootFS := newTestToolsWithRoot(t)
	createTestFileTree(t, rootFS.Name(), map[string]string{
		"alpha.go":     "package main",
		"sub/beta.txt": "beta",
	})

	outFiltered := g.Dispatch("file_list", `{"filter":"*.go"}`)
	if !strings.Contains(outFiltered, "alpha.go") || strings.Contains(outFiltered, "beta.txt") {
		t.Errorf("Dispatch(file_list, *.go) = %q, want alpha.go only", outFiltered)
	}

	outAll := g.Dispatch("file_list", `{}`)
	if !strings.Contains(outAll, "alpha.go") || !strings.Contains(outAll, "beta.txt") {
		t.Errorf("Dispatch(file_list, empty object) = %q, want both files", outAll)
	}

	outInvalidJSON := g.Dispatch("file_list", `{invalid`)
	if !strings.Contains(outInvalidJSON, "error: failed to parse arguments") {
		t.Errorf("Dispatch(file_list, invalid JSON) = %q, want parse error", outInvalidJSON)
	}
}
