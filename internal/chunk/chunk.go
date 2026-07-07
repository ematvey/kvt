package chunk

import (
	"fmt"
	"regexp"
	"strings"
)

const approxTokenLimit = 180

var headingPattern = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

type Document struct {
	Path            string
	Title           string
	Type            string
	Description     string
	FrontmatterText string
	Body            string
}

type Chunk struct {
	Ordinal   int
	Text      string
	EmbedText string
}

func Split(doc Document) ([]Chunk, error) {
	chunks := make([]Chunk, 0, 8)

	header := collapseText(strings.Join(nonEmpty(
		doc.Title,
		doc.Type,
		doc.Description,
		doc.FrontmatterText,
	), "\n"))
	if header != "" {
		chunks = append(chunks, Chunk{
			Ordinal:   len(chunks),
			Text:      header,
			EmbedText: header,
		})
	}

	sections, err := splitSections(strings.TrimSpace(doc.Body))
	if err != nil {
		return nil, err
	}
	for _, section := range sections {
		for _, piece := range sectionChunks(section) {
			text := strings.TrimSpace(piece)
			if text == "" {
				continue
			}
			searchText := strings.TrimSpace(strings.Join(nonEmpty(section.breadcrumb(), text), "\n\n"))
			prefix := strings.Join(nonEmpty(doc.Title, doc.Type, section.breadcrumb()), " | ")
			embedText := strings.TrimSpace(strings.Join(nonEmpty(prefix, collapseText(text)), "\n\n"))
			chunks = append(chunks, Chunk{
				Ordinal:   len(chunks),
				Text:      searchText,
				EmbedText: embedText,
			})
		}
	}

	if len(chunks) == 0 {
		return []Chunk{{
			Ordinal:   0,
			Text:      "",
			EmbedText: strings.Join(nonEmpty(doc.Title, doc.Type, doc.Description), " "),
		}}, nil
	}

	return chunks, nil
}

type section struct {
	headings []string
	blocks   []string
}

func (s section) breadcrumb() string {
	return strings.Join(nonEmpty(s.headings...), " > ")
}

func splitSections(body string) ([]section, error) {
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}

	lines := strings.Split(body, "\n")
	sections := make([]section, 0, 8)
	headings := []string{}
	current := section{}

	appendCurrent := func() {
		if len(current.blocks) == 0 {
			return
		}
		copyHeadings := append([]string(nil), headings...)
		copyBlocks := append([]string(nil), current.blocks...)
		sections = append(sections, section{
			headings: copyHeadings,
			blocks:   copyBlocks,
		})
		current = section{}
	}

	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if match := headingPattern.FindStringSubmatch(trimmed); match != nil {
			appendCurrent()
			level := len(match[1])
			headings = trimHeadingStack(headings, level-1)
			headings = append(headings, strings.TrimSpace(match[2]))
			i++
			continue
		}
		if trimmed == "" {
			i++
			continue
		}

		switch {
		case isFenceStart(trimmed):
			block, next, err := consumeFence(lines, i)
			if err != nil {
				return nil, err
			}
			current.blocks = append(current.blocks, block)
			i = next
		case isTableLine(trimmed):
			block, next := consumeTable(lines, i)
			current.blocks = append(current.blocks, block)
			i = next
		default:
			block, next := consumeParagraph(lines, i)
			current.blocks = append(current.blocks, block)
			i = next
		}
	}
	appendCurrent()
	return sections, nil
}

func trimHeadingStack(stack []string, keep int) []string {
	if keep < 0 {
		return nil
	}
	if keep >= len(stack) {
		return stack
	}
	out := append([]string(nil), stack[:keep]...)
	return out
}

func sectionChunks(section section) []string {
	if len(section.blocks) == 0 {
		return nil
	}
	chunks := make([]string, 0, len(section.blocks))
	current := []string{}
	currentTokens := 0

	flush := func() {
		if len(current) == 0 {
			return
		}
		chunks = append(chunks, strings.TrimSpace(strings.Join(current, "\n\n")))
		current = nil
		currentTokens = 0
	}

	for _, block := range section.blocks {
		tokens := tokenCount(block)
		if isAtomicBlock(block) {
			if currentTokens > 0 && currentTokens+tokens > approxTokenLimit {
				flush()
			}
			current = append(current, strings.TrimSpace(block))
			currentTokens += tokens
			if currentTokens >= approxTokenLimit {
				flush()
			}
			continue
		}
		for _, paragraph := range splitOversizedParagraphs(block) {
			paraTokens := tokenCount(paragraph)
			if currentTokens > 0 && currentTokens+paraTokens > approxTokenLimit {
				flush()
			}
			current = append(current, strings.TrimSpace(paragraph))
			currentTokens += paraTokens
		}
	}
	flush()
	return chunks
}

func splitOversizedParagraphs(block string) []string {
	block = strings.TrimSpace(block)
	if block == "" {
		return nil
	}
	paragraphs := strings.Split(block, "\n\n")
	out := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		if tokenCount(paragraph) <= approxTokenLimit {
			out = append(out, paragraph)
			continue
		}
		words := strings.Fields(paragraph)
		for len(words) > 0 {
			n := approxTokenLimit
			if n > len(words) {
				n = len(words)
			}
			out = append(out, strings.Join(words[:n], " "))
			words = words[n:]
		}
	}
	return out
}

func isAtomicBlock(block string) bool {
	trimmed := strings.TrimSpace(block)
	return isFenceStart(trimmed) || isTableLine(firstNonEmptyLine(trimmed))
}

func firstNonEmptyLine(block string) string {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func consumeFence(lines []string, start int) (string, int, error) {
	open := strings.TrimSpace(lines[start])
	fence := fenceDelimiter(open)
	if fence == "" {
		return "", start, fmt.Errorf("invalid fenced block start: %q", open)
	}
	block := []string{lines[start]}
	for i := start + 1; i < len(lines); i++ {
		block = append(block, lines[i])
		if strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
			return strings.TrimSpace(strings.Join(block, "\n")), i + 1, nil
		}
	}
	return strings.TrimSpace(strings.Join(block, "\n")), len(lines), nil
}

func consumeTable(lines []string, start int) (string, int) {
	block := []string{}
	i := start
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || headingPattern.MatchString(trimmed) || isFenceStart(trimmed) {
			break
		}
		if !isTableLine(trimmed) {
			break
		}
		block = append(block, lines[i])
	}
	return strings.TrimSpace(strings.Join(block, "\n")), i
}

func consumeParagraph(lines []string, start int) (string, int) {
	block := []string{}
	i := start
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || headingPattern.MatchString(trimmed) || isFenceStart(trimmed) || isTableLine(trimmed) {
			break
		}
		block = append(block, lines[i])
	}
	return strings.TrimSpace(strings.Join(block, "\n")), i
}

func isFenceStart(line string) bool {
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

func fenceDelimiter(line string) string {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```"
	case strings.HasPrefix(line, "~~~"):
		return "~~~"
	default:
		return ""
	}
}

func isTableLine(line string) bool {
	if strings.Count(line, "|") < 2 {
		return false
	}
	return strings.Contains(line, "|")
}

func tokenCount(text string) int {
	return len(strings.Fields(text))
}

func collapseText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, strings.Join(strings.Fields(line), " "))
	}
	return strings.Join(out, "\n")
}

func nonEmpty(items ...string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
