package indexer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wmentor/go-magnetar/internal/chunk"
	"github.com/wmentor/go-magnetar/internal/codec"
	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/plugin"
	"github.com/wmentor/go-magnetar/internal/printer"
	"github.com/wmentor/go-magnetar/internal/store"
	"github.com/wmentor/go-magnetar/internal/tools/rag"
	"github.com/wmentor/go-magnetar/internal/tools/web"
)

// DefaultExcludedDirs lists directory names skipped by default during recursive directory indexing.
var DefaultExcludedDirs = []string{".git", "node_modules", "__pycache__", "pycache"}

// Indexer chunks content into overlapping segments and stores them in the RAG knowledge base.
type Indexer struct {
	cfg *config.Config
	rag *rag.RAGTools
	web *web.WebTools
}

// New creates a new Indexer instance.
func New(cfg *config.Config, root *os.Root, state *plugin.State) (*Indexer, error) {
	vecStore, err := store.NewStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("init RAG store error: %w", err)
	}

	ragTools, err := rag.New(cfg, vecStore)
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to initialise RAG tools: %w", err)
	}

	webTools, err := web.New(cfg, root, state)
	if err != nil {
		return nil, fmt.Errorf("indexer: failed to initialise web tools: %w", err)
	}

	return &Indexer{
		cfg: cfg,
		rag: ragTools,
		web: webTools,
	}, nil
}

// IndexFile indexes a single file into the RAG knowledge base.
func (idx *Indexer) IndexFile(filename string, msg string) error {
	_, err := idx.indexFileWithCount(filename, msg)
	return err
}

func (idx *Indexer) indexFileWithCount(filename string, msg string) (int, error) {
	printer.Print(printer.IconFile, "indexer: indexing file", "file", filename)

	ext := strings.ToLower(filepath.Ext(filename))

	var data []byte
	var err error

	if reader, e := codec.GetCodec(ext); e == nil {
		text, err := reader.ReadFile(filename)
		if err != nil {
			return 0, fmt.Errorf("indexer: failed to read %s file %q: %w", ext, filename, err)
		}
		data = []byte(text)
	} else {
		data, err = os.ReadFile(filename)
		if err != nil {
			return 0, fmt.Errorf("indexer: failed to read file %q: %w", filename, err)
		}
	}

	return idx.chunkAndSave(string(data), msg)
}

// IndexDirectory recursively indexes all supported files in dirPath into the RAG knowledge base.
func (idx *Indexer) IndexDirectory(dirPath string, msg string, recursive bool, excludes ...string) error {
	if !recursive {
		return fmt.Errorf("indexer: directory %q requires -r/--recursive flag", dirPath)
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		return fmt.Errorf("indexer: failed to access directory %q: %w", dirPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("indexer: %q is not a directory", dirPath)
	}

	excluded := make(map[string]struct{}, len(DefaultExcludedDirs)+len(excludes))
	for _, d := range DefaultExcludedDirs {
		excluded[d] = struct{}{}
	}
	for _, raw := range excludes {
		for _, part := range strings.Split(raw, ",") {
			if name := strings.TrimSpace(part); name != "" {
				excluded[name] = struct{}{}
			}
		}
	}

	cleanRoot := filepath.Clean(dirPath)
	filesIndexed := 0
	chunksIndexed := 0

	walkErr := filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			printer.Warn("indexer: failed to access path", "path", path, "error", err)
			return nil
		}

		if d.IsDir() {
			if filepath.Clean(path) != cleanRoot {
				if _, skip := excluded[d.Name()]; skip {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if !d.Type().IsRegular() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !codec.IsSupported(ext) {
			printer.Warn("indexer: skipping unsupported file", "file", path)
			return nil
		}

		chunks, indexErr := idx.indexFileWithCount(path, msg)
		if indexErr != nil {
			printer.Warn("indexer: failed to index file", "file", path, "error", indexErr)
			return nil
		}

		filesIndexed++
		chunksIndexed += chunks
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("indexer: failed to walk directory %q: %w", dirPath, walkErr)
	}

	printer.Print(
		printer.IconDone,
		fmt.Sprintf("indexer: indexed %d files, %d chunks", filesIndexed, chunksIndexed),
		"dir", dirPath,
		"files", filesIndexed,
		"chunks", chunksIndexed,
	)

	return nil
}

// IndexURL fetches content from a URL, chunks it, and stores it in the RAG knowledge base.
func (idx *Indexer) IndexURL(rawURL string, msg string) error {
	printer.Print(printer.IconURL, "indexer: indexing URL", "url", rawURL)

	content, err := idx.web.WebFetch(rawURL)
	if err != nil {
		return fmt.Errorf("indexer: failed to fetch URL %q: %w", rawURL, err)
	}

	_, err = idx.chunkAndSave(content, msg)
	return err
}

// chunkAndSave splits content into overlapping chunks and saves each one to RAG.
func (idx *Indexer) chunkAndSave(content string, msg string) (int, error) {
	cfg := chunk.Config{
		MaxSize: idx.cfg.Int("rag.chunk.size"),
		Overlap: idx.cfg.Int("rag.chunk.overlap"),
	}
	chunks := chunk.Split(content, cfg)
	saved := 0

	for i, c := range chunks {
		if idx.rag.RagSave(c, msg, i+1) {
			saved++
		}
	}
	return saved, nil
}
