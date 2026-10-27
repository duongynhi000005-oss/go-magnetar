package indexcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wmentor/go-magnetar/internal/agent/indexer"
	"github.com/wmentor/go-magnetar/internal/common"
	"github.com/wmentor/go-magnetar/internal/plugin"
	"github.com/wmentor/go-magnetar/internal/printer"
)

func init() {
	plugin.Register("indexcmd", &Plugin{})
}

type Plugin struct {
	idx *indexer.Indexer
}

func (p *Plugin) Init(s *plugin.State, hub plugin.Hub) error {
	if !s.Config.Bool("rag.enable") || s.Config.String("rag.llm.base_url") == "" {
		return nil
	}

	root, err := os.OpenRoot("/")
	if err != nil {
		return fmt.Errorf("indexcmd: failed to open root: %w", err)
	}
	defer root.Close()

	p.idx, err = indexer.New(s.Config, root, s)
	if err != nil {
		return fmt.Errorf("indexcmd: failed to create indexer: %w", err)
	}

	hub.RegisterChatCommand(plugin.ChatCommand{
		Name:    "index",
		Aliases: []string{"i"},
		Help:    "Index file, directory (-r/--recursive), or URL into RAG knowledge base (options: -r/--recursive, -m <message>, --exclude <dirs>)",
		Execute: p.execute,
	})

	hub.RegisterChatCommand(plugin.ChatCommand{
		Name:    "idxtab",
		Aliases: []string{},
		Help:    "Index multiple files/URLs from a JSON lines file (one per line, format: {\"source\":\"path|url\",\"message\":\"text\"})",
		Execute: p.executeTab,
	})

	return nil
}

type indexOptions struct {
	target    string
	message   string
	recursive bool
	excludes  []string
}

func tokenizeArgs(raw string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inQuote := rune(0)
	escaped := false

	for _, r := range raw {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && inQuote == '"' {
			escaped = true
			continue
		}
		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			} else {
				cur.WriteRune(r)
			}
			continue
		}
		if r == '"' || r == '\'' {
			inQuote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}

	if inQuote != 0 {
		return nil, fmt.Errorf("unterminated quote in arguments")
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

func parseIndexArgs(raw string) (indexOptions, error) {
	var opts indexOptions

	tokens, err := tokenizeArgs(strings.TrimSpace(raw))
	if err != nil {
		return opts, err
	}
	if len(tokens) == 0 {
		return opts, fmt.Errorf("usage: /index <path|url> [-r|--recursive] [-m <message>] [--exclude <dirs>]")
	}

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == "-r" || tok == "--recursive":
			opts.recursive = true
		case tok == "-m" || tok == "--message":
			if i+1 >= len(tokens) {
				return opts, fmt.Errorf("missing value for %s", tok)
			}
			opts.message = tokens[i+1]
			i++
		case strings.HasPrefix(tok, "-m="):
			opts.message = strings.TrimPrefix(tok, "-m=")
		case strings.HasPrefix(tok, "--message="):
			opts.message = strings.TrimPrefix(tok, "--message=")
		case tok == "--exclude":
			if i+1 >= len(tokens) {
				return opts, fmt.Errorf("missing value for --exclude")
			}
			for _, part := range strings.Split(tokens[i+1], ",") {
				if name := strings.TrimSpace(part); name != "" {
					opts.excludes = append(opts.excludes, name)
				}
			}
			i++
		case strings.HasPrefix(tok, "--exclude="):
			for _, part := range strings.Split(strings.TrimPrefix(tok, "--exclude="), ",") {
				if name := strings.TrimSpace(part); name != "" {
					opts.excludes = append(opts.excludes, name)
				}
			}
		default:
			if opts.target == "" {
				opts.target = tok
			}
		}
	}

	if opts.target == "" {
		return opts, fmt.Errorf("usage: /index <path|url> [-r|--recursive] [-m <message>] [--exclude <dirs>]")
	}

	return opts, nil
}

func (p *Plugin) execute(ctx context.Context, params *plugin.ChatCommandInput) (*plugin.ChatCommandResponse, error) {
	opts, err := parseIndexArgs(params.Args)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(opts.target, "http://") || strings.HasPrefix(opts.target, "https://") {
		if err := p.idx.IndexURL(opts.target, opts.message); err != nil {
			return nil, fmt.Errorf("index URL failed: %w", err)
		}
		return plugin.NopChatCommandResponse, nil
	}

	target := common.ExpandHome(opts.target)
	if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
		if err := p.idx.IndexDirectory(target, opts.message, opts.recursive, opts.excludes...); err != nil {
			return nil, fmt.Errorf("index directory failed: %w", err)
		}
		return plugin.NopChatCommandResponse, nil
	}

	if err := p.idx.IndexFile(target, opts.message); err != nil {
		return nil, fmt.Errorf("index file failed: %w", err)
	}
	return plugin.NopChatCommandResponse, nil
}

type Entry struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

func (p *Plugin) executeTab(ctx context.Context, params *plugin.ChatCommandInput) (*plugin.ChatCommandResponse, error) {
	args := params.Args
	filename := common.ExpandHome(strings.TrimSpace(args))

	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open file error: %w", err)
	}
	defer file.Close()

	workDir := filepath.Dir(filename)

	scanner := bufio.NewScanner(file)
	count := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			printer.Error("json parse error", "line", line, "error", err)
			continue
		}

		source := entry.Source
		message := entry.Message

		if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
			if err := p.idx.IndexURL(source, message); err != nil {
				printer.Error("index URL failed", "url", source, "error", err)
				continue
			}
		} else {
			if !filepath.IsAbs(source) {
				source = filepath.Join(workDir, source)
			}
			if err := p.idx.IndexFile(source, message); err != nil {
				printer.Error("index file failed", "file", source, "error", err)
				continue
			}
		}

		count++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan error: %w", err)
	}

	return plugin.NopChatCommandResponse, nil
}
