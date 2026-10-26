package docx

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/fumiama/go-docx"
	"github.com/pkg/errors"
)

var (
	headingRegex     = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	bulletListRegex  = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	orderedListRegex = regexp.MustCompile(`^(\s*)\d+\.\s+(.*)$`)
	inlineTokenRegex = regexp.MustCompile(`(\*\*\*[^*]+\*\*\*|\*\*[^*]+\*\*|__[^_]+__|\*[^*\n]+\*|_[^_\n]+_|` + "`[^`]+`" + `|\[[^\]]+\]\([^)]+\))`)
)

// WriteCodec converts Markdown content into a DOCX file.
type WriteCodec struct{}

// WriteFile parses Markdown content and writes a formatted DOCX file to filename.
func (c *WriteCodec) WriteFile(filename string, content string) error {
	doc := docx.New().WithDefaultTheme().WithA4Page()

	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	inCodeBlock := false
	var codeLines []string
	var paraLines []string

	flushParagraph := func() {
		if len(paraLines) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(paraLines, " "))
		paraLines = nil
		if text == "" {
			return
		}
		p := doc.AddParagraph()
		appendInlineRuns(p, text)
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if inCodeBlock {
				flushParagraph()
				p := doc.AddParagraph()
				r := p.AddText(strings.Join(codeLines, "\n"))
				r.Font("Courier New", "Courier New", "Courier New", "default")
				codeLines = nil
				inCodeBlock = false
			} else {
				flushParagraph()
				inCodeBlock = true
			}
			continue
		}

		if inCodeBlock {
			codeLines = append(codeLines, line)
			continue
		}

		if trimmed == "" {
			flushParagraph()
			continue
		}

		if m := headingRegex.FindStringSubmatch(trimmed); m != nil {
			flushParagraph()
			level := len(m[1])
			p := doc.AddParagraph().Style(fmt.Sprintf("Heading%d", level))
			appendInlineRuns(p, strings.TrimSpace(m[2]))
			continue
		}

		if m := bulletListRegex.FindStringSubmatch(line); m != nil {
			flushParagraph()
			lvl := len(m[1]) / 2
			p := doc.AddParagraph().NumPr("1", strconv.Itoa(lvl))
			appendInlineRuns(p, strings.TrimSpace(m[2]))
			continue
		}

		if m := orderedListRegex.FindStringSubmatch(line); m != nil {
			flushParagraph()
			lvl := len(m[1]) / 2
			p := doc.AddParagraph().NumPr("2", strconv.Itoa(lvl))
			appendInlineRuns(p, strings.TrimSpace(m[2]))
			continue
		}

		paraLines = append(paraLines, trimmed)
	}

	if inCodeBlock && len(codeLines) > 0 {
		p := doc.AddParagraph()
		r := p.AddText(strings.Join(codeLines, "\n"))
		r.Font("Courier New", "Courier New", "Courier New", "default")
	}
	flushParagraph()

	outFile, err := os.Create(filename)
	if err != nil {
		return errors.Wrapf(err, "create %q error", filename)
	}
	defer outFile.Close()

	if _, err := doc.WriteTo(outFile); err != nil {
		return errors.Wrapf(err, "write docx %q error", filename)
	}

	return nil
}

func appendInlineRuns(p *docx.Paragraph, text string) {
	matches := inlineTokenRegex.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		p.AddText(text)
		return
	}

	lastIdx := 0
	for _, loc := range matches {
		start, end := loc[0], loc[1]
		if start > lastIdx {
			p.AddText(text[lastIdx:start])
		}
		token := text[start:end]
		switch {
		case strings.HasPrefix(token, "***") && strings.HasSuffix(token, "***"):
			p.AddText(token[3 : len(token)-3]).Bold().Italic()
		case (strings.HasPrefix(token, "**") && strings.HasSuffix(token, "**")) ||
			(strings.HasPrefix(token, "__") && strings.HasSuffix(token, "__")):
			p.AddText(token[2 : len(token)-2]).Bold()
		case (strings.HasPrefix(token, "*") && strings.HasSuffix(token, "*")) ||
			(strings.HasPrefix(token, "_") && strings.HasSuffix(token, "_")):
			p.AddText(token[1 : len(token)-1]).Italic()
		case strings.HasPrefix(token, "`") && strings.HasSuffix(token, "`"):
			r := p.AddText(token[1 : len(token)-1])
			r.Font("Courier New", "Courier New", "Courier New", "default")
		case strings.HasPrefix(token, "[") && strings.Contains(token, "](") && strings.HasSuffix(token, ")"):
			splitIdx := strings.Index(token, "](")
			label := token[1:splitIdx]
			link := token[splitIdx+2 : len(token)-1]
			p.AddLink(label, link)
		default:
			p.AddText(token)
		}
		lastIdx = end
	}

	if lastIdx < len(text) {
		p.AddText(text[lastIdx:])
	}
}
