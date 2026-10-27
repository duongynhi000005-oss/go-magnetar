package chat

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/wmentor/go-magnetar/internal/codec"
	"github.com/wmentor/go-magnetar/internal/common"
	hstore "github.com/wmentor/go-magnetar/internal/history"
)

const maxDisplayedSuggestions = 8

var suggestionStyle = lipgloss.NewStyle().Faint(true)

var supportedTextExtensions = map[string]bool{
	".txt":      true,
	".md":       true,
	".markdown": true,
	".rst":      true,
	".json":     true,
	".jsonl":    true,
	".yaml":     true,
	".yml":      true,
	".toml":     true,
	".ini":      true,
	".cfg":      true,
	".conf":     true,
	".xml":      true,
	".go":       true,
	".py":       true,
	".js":       true,
	".ts":       true,
	".tsx":      true,
	".jsx":      true,
	".rs":       true,
	".c":        true,
	".cpp":      true,
	".h":        true,
	".hpp":      true,
	".java":     true,
	".rb":       true,
	".php":      true,
	".sh":       true,
	".bash":     true,
	".zsh":      true,
	".sql":      true,
	".css":      true,
	".scss":     true,
	".log":      true,
}

func isSupportedIndexExtension(ext string) bool {
	ext = strings.ToLower(ext)
	if supportedTextExtensions[ext] {
		return true
	}
	_, err := codec.GetCodec(ext)
	return err == nil
}

func isJSONLExtension(ext string) bool {
	return strings.EqualFold(ext, ".jsonl")
}

// completePath returns file and directory paths matching partial.
// Directories are suffixed with "/" so the user can continue completing inside them.
// Regular files are filtered with extFilter when non-nil.
func completePath(partial string, extFilter func(ext string) bool) []string {
	expanded := common.ExpandHome(partial)
	var homePrefix string
	if strings.HasPrefix(partial, "~/") || strings.HasPrefix(partial, `~\`) || partial == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			homePrefix = home
		}
	}

	pattern := expanded + "*"
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}

	sort.Strings(matches)
	results := make([]string, 0, len(matches))

	for _, match := range matches {
		info, statErr := os.Stat(match)
		if statErr != nil {
			continue
		}

		display := match
		if homePrefix != "" && strings.HasPrefix(display, homePrefix) {
			rest := strings.TrimPrefix(display, homePrefix)
			display = "~" + rest
		}
		display = filepath.ToSlash(display)
		if strings.HasPrefix(partial, "./") && !strings.HasPrefix(display, "./") {
			display = "./" + display
		}

		if info.IsDir() {
			if !strings.HasSuffix(display, "/") {
				display += "/"
			}
			results = append(results, display)
			continue
		}

		if extFilter == nil || extFilter(filepath.Ext(match)) {
			results = append(results, display)
		}
	}

	return results
}

// completeURLs extracts URLs from command history that match partial.
func completeURLs(partial string, hist *hstore.Storage) []string {
	if hist == nil {
		return nil
	}

	records := hist.Records()
	seen := make(map[string]struct{})
	var urls []string

	for i := len(records) - 1; i >= 0; i-- {
		for _, token := range strings.Fields(records[i]) {
			if !strings.HasPrefix(token, "http://") && !strings.HasPrefix(token, "https://") {
				continue
			}
			if !strings.HasPrefix(token, partial) {
				continue
			}
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			urls = append(urls, token)
		}
	}

	sort.Strings(urls)
	return urls
}

// completeCommandInput handles Tab completion for /index, /idxtab, and /fetch commands.
// It returns the updated input string and the slice of matching suggestions.
func completeCommandInput(line string, hist *hstore.Storage) (string, []string) {
	trimmedLeft := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmedLeft, "/") {
		return line, nil
	}

	spaceIdx := strings.IndexAny(trimmedLeft, " \t")
	if spaceIdx == -1 {
		return line, nil
	}

	cmdName := strings.ToLower(strings.TrimPrefix(trimmedLeft[:spaceIdx], "/"))
	switch cmdName {
	case "index", "i", "idxtab", "fetch", "f":
	default:
		return line, nil
	}

	// Split line into prefix (everything up to the last whitespace-delimited token) and partial token.
	lastSpace := strings.LastIndexAny(line, " \t")
	prefix := line[:lastSpace+1]
	partial := line[lastSpace+1:]

	// Count previous arguments after the command name to handle flags or positional args.
	middle := strings.Fields(trimmedLeft[spaceIdx:])
	var prevArgs []string
	if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
		prevArgs = middle
	} else if len(middle) > 1 {
		prevArgs = middle[:len(middle)-1]
	}

	// Do not complete flag values for -m.
	if len(prevArgs) > 0 && prevArgs[len(prevArgs)-1] == "-m" {
		return line, nil
	}

	var suggestions []string

	switch cmdName {
	case "index", "i":
		if strings.HasPrefix(partial, "http://") || strings.HasPrefix(partial, "https://") {
			suggestions = completeURLs(partial, hist)
		} else {
			suggestions = completePath(partial, isSupportedIndexExtension)
		}
	case "idxtab":
		suggestions = completePath(partial, isJSONLExtension)
	case "fetch", "f":
		if len(prevArgs) == 0 {
			if strings.HasPrefix(partial, "http://") || strings.HasPrefix(partial, "https://") {
				suggestions = completeURLs(partial, hist)
			} else {
				urls := completeURLs(partial, hist)
				paths := completePath(partial, nil)
				suggestions = append(urls, paths...)
			}
		} else if len(prevArgs) == 1 {
			suggestions = completePath(partial, nil)
		}
	}

	if len(suggestions) == 0 {
		return line, nil
	}

	if len(suggestions) == 1 {
		return prefix + suggestions[0], suggestions
	}

	lcp := longestCommonPrefix(suggestions)
	if len(lcp) > len(partial) {
		return prefix + lcp, suggestions
	}

	return line, suggestions
}

func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, prefix) {
			if prefix == "" {
				return ""
			}
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func formatSuggestions(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}
	limit := min(len(suggestions), maxDisplayedSuggestions)
	var b strings.Builder
	for i := range limit {
		b.WriteString("\n  ")
		b.WriteString(suggestionStyle.Render(suggestions[i]))
	}
	if len(suggestions) > limit {
		b.WriteString("\n  ")
		b.WriteString(suggestionStyle.Render("..."))
	}
	return b.String()
}
