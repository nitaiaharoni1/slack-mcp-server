package handler

import (
	"regexp"
	"strings"

	"github.com/slack-go/slack"
	slackGoUtil "github.com/takara2314/slack-go-util"
)

// Slack accepts one table block per message; later tables stay preformatted.
const maxTablesPerMessage = 1

var tableSeparatorRe = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)

// markdownToBlocks converts markdown into Block Kit. The converter library has
// no GFM tables, so a pipe table becomes a native table block instead of a
// paragraph of pipes; everything else goes through the library.
func markdownToBlocks(markdown string) ([]slack.Block, error) {
	lines := strings.Split(markdown, "\n")
	var blocks []slack.Block
	var chunk []string
	tables := 0
	inFence := false

	flush := func() error {
		if strings.TrimSpace(strings.Join(chunk, "")) == "" {
			chunk = nil
			return nil
		}
		converted, err := slackGoUtil.ConvertMarkdownTextToBlocks(strings.Join(chunk, "\n"))
		if err != nil {
			return err
		}
		blocks = append(blocks, converted...)
		chunk = nil
		return nil
	}

	for i := 0; i < len(lines); i++ {
		bare := bareLine(lines[i])
		if strings.HasPrefix(bare, "```") {
			inFence = !inFence
		}
		if inFence || !isTableStart(lines, i) {
			chunk = append(chunk, lines[i])
			continue
		}
		end := i + 2
		for end < len(lines) && strings.HasPrefix(bareLine(lines[end]), "|") {
			end++
		}
		if tables >= maxTablesPerMessage {
			chunk = append(chunk, "```")
			chunk = append(chunk, lines[i:end]...)
			chunk = append(chunk, "```")
		} else {
			if err := flush(); err != nil {
				return nil, err
			}
			blocks = append(blocks, tableBlock(lines[i], lines[i+1], lines[i+2:end]))
			tables++
		}
		i = end - 1
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return blocks, nil
}

// bareLine strips whitespace and the bidi marks callers put at the start of
// right-to-left lines, so a Hebrew table row still reads as a table row.
func bareLine(line string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "‏‎"))
}

func isTableStart(lines []string, i int) bool {
	return i+1 < len(lines) &&
		strings.HasPrefix(bareLine(lines[i]), "|") &&
		strings.Contains(bareLine(lines[i+1]), "-") &&
		tableSeparatorRe.MatchString(bareLine(lines[i+1]))
}

func tableCells(line string) []string {
	row := strings.TrimSuffix(strings.TrimPrefix(bareLine(line), "|"), "|")
	cells := strings.Split(row, "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

func tableBlock(header, separator string, body []string) *slack.TableBlock {
	headers := tableCells(header)
	table := slack.NewTableBlock("")
	var settings []slack.ColumnSetting
	for _, sep := range tableCells(separator) {
		align := slack.ColumnAlignmentLeft
		switch {
		case strings.HasPrefix(sep, ":") && strings.HasSuffix(sep, ":"):
			align = slack.ColumnAlignmentCenter
		case strings.HasSuffix(sep, ":"):
			align = slack.ColumnAlignmentRight
		}
		settings = append(settings, slack.ColumnSetting{Align: align, IsWrapped: true})
	}
	table.WithColumnSettings(settings...)
	table.AddRow(tableRow(headers, len(headers), true)...)
	for _, line := range body {
		table.AddRow(tableRow(tableCells(line), len(headers), false)...)
	}
	return table
}

// tableRow pads or trims a row to the header's width: Slack rejects a table
// whose rows differ in length.
func tableRow(cells []string, width int, bold bool) []*slack.RichTextBlock {
	row := make([]*slack.RichTextBlock, width)
	for i := range row {
		text := " " // Slack rejects an empty text element
		if i < len(cells) && strings.Trim(cells[i], "*") != "" {
			text = strings.Trim(cells[i], "*")
		}
		el := &slack.RichTextSectionTextElement{Type: slack.RTSEText, Text: text}
		if bold {
			el.Style = &slack.RichTextSectionTextStyle{Bold: true}
		}
		row[i] = slack.NewRichTextBlock("", slack.NewRichTextSection(el))
	}
	return row
}
