package text

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// MaxMarkdownChars is Slack's limit for the text of a markdown block; a
// longer one fails with msg_too_long.
const MaxMarkdownChars = 12000

// Slack renders a rich text block in the direction of its first strong
// character but always aligns it left. The marks below set that direction
// per line, so Hebrew reads right to left with its punctuation in place.
const (
	rlm   = "‏"
	lrm   = "‎"
	marks = "‎‏؜"
)

type direction int

const (
	dirNone direction = iota
	dirLTR
	dirRTL
)

var (
	fenceRe     = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	headingRe   = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.*)$`)
	itemRe      = regexp.MustCompile(`^([ \t]*)([-*+]|\d{1,9}[.)])([ \t]+)(\[[ xX]\][ \t]+)?(.*)$`)
	quoteRe     = regexp.MustCompile(`^ {0,3}> ?(.*)$`)
	tableSepRe  = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)
	mentionRe   = regexp.MustCompile("‎?(<[@#!][^>]+>)")
	leadDelimRe = regexp.MustCompile(`^(\*{1,3}|_{1,3}|~~)`)
	tailDelimRe = regexp.MustCompile(`(\*{1,3}|_{1,3}|~~)$`)
	// Tokens that carry no reading direction of their own.
	noDirRe       = regexp.MustCompile("`[^`]*`|<[^>]*>|\\]\\([^)]*\\)|https?://\\S+|:[a-z0-9_+-]+:")
	mdLinkRe      = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	inlineMarksRe = regexp.MustCompile("\\*\\*|__|~~|`")
	mdBoldRe      = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdItalicRe    = regexp.MustCompile(`\*([^*\s][^*]*)\*`)
	mdStrikeRe    = regexp.MustCompile(`~~(.+?)~~`)
	mdLinkPartsRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
)

var manualBullets = []string{"●", "○", "▪"}

// PrepareMarkdown readies markdown for a Slack markdown block. Slack parses the
// markdown itself; this only fixes what its rendering gets wrong:
//   - every right-to-left line gets RLM marks, every left-to-right line inside
//     right-to-left text gets LRM marks, and caller-supplied marks in front of
//     a line's markdown (which would hide the markdown) move inside it;
//   - mentions inside right-to-left lines get an LRM so their @ stays in front;
//   - a numbered list in right-to-left text, or a list running against the
//     text's direction, becomes numbered lines, because Slack puts the numbers
//     at the far edge of the message, away from the text;
//   - tables of right-to-left text read from the right;
//   - "---" under a line is a divider, not a heading underline.
func PrepareMarkdown(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	out, _ := prepareLines(lines, dirNone)
	return strings.Join(out, "\n")
}

// BidiLines marks the direction of each line of plain or mrkdwn text, the
// same way PrepareMarkdown does, without touching anything else.
func BidiLines(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	base := dirNone
	for i, line := range lines {
		bare := stripMarks(line)
		dir := lineDir(bare)
		if base == dirNone {
			base = dir
		}
		lines[i] = markLine(bare, dir, base)
	}
	return strings.Join(lines, "\n")
}

// MarkdownFallback turns markdown into the plain text Slack shows in
// notifications and previews of a message whose body is a markdown block.
func MarkdownFallback(md string) string {
	var out []string
	inFence := false
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		bare := stripMarks(line)
		if fenceRe.MatchString(bare) {
			inFence = !inFence
			continue
		}
		if !inFence {
			if isHR(bare) || tableSepRe.MatchString(strings.TrimSpace(bare)) && strings.Contains(bare, "-") {
				continue
			}
			if m := headingRe.FindStringSubmatch(bare); m != nil {
				bare = m[2]
			}
			if m := quoteRe.FindStringSubmatch(bare); m != nil {
				bare = m[1]
			}
			if strings.HasPrefix(strings.TrimSpace(bare), "|") {
				bare = strings.Join(tableCells(bare), " | ")
			}
			bare = mdLinkRe.ReplaceAllString(bare, "$1")
			bare = inlineMarksRe.ReplaceAllString(bare, "")
		}
		out = append(out, bare)
	}
	return BidiLines(strings.TrimSpace(strings.Join(out, "\n")))
}

// InlineMrkdwn converts markdown's inline styles to Slack's mrkdwn and marks each
// line's direction, for text that cannot be a markdown block.
func InlineMrkdwn(s string) string {
	s = mdBoldRe.ReplaceAllString(s, "\x00$1\x00")
	s = mdItalicRe.ReplaceAllString(s, "_${1}_")
	s = mdStrikeRe.ReplaceAllString(s, "~$1~")
	s = mdLinkPartsRe.ReplaceAllString(s, "<$2|$1>")
	return BidiLines(strings.ReplaceAll(s, "\x00", "*"))
}

// prepareLines processes one run of markdown lines. base is the direction of
// the rich text block the lines land in, decided by its first line with text.
func prepareLines(lines []string, base direction) ([]string, direction) {
	var out []string
	for i := 0; i < len(lines); {
		bare := stripMarks(lines[i])
		switch {
		case fenceRe.MatchString(bare):
			end := fenceEnd(lines, i)
			out = append(out, bare)
			out = append(out, lines[i+1:end]...)
			i = end
			continue
		case strings.TrimSpace(bare) == "":
			out = append(out, "")
		case isIndentedCode(lines[i], out):
			out = append(out, lines[i])
		case headingRe.MatchString(bare):
			m := headingRe.FindStringSubmatch(bare)
			content := trimMarks(m[2])
			dir := lineDir(content)
			out = append(out, m[1]+" "+markLine(content, dir, dir))
			base = dirNone
		case isHR(bare):
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, "---")
			base = dirNone
		case isTableStart(lines, i):
			end := i + 2
			for end < len(lines) && strings.HasPrefix(strings.TrimSpace(stripMarks(lines[end])), "|") {
				end++
			}
			out = append(out, renderTable(lines[i], lines[i+1], lines[i+2:end])...)
			i = end
			base = dirNone
			continue
		case quoteRe.MatchString(bare):
			end := i
			var inner []string
			for end < len(lines) {
				m := quoteRe.FindStringSubmatch(stripMarks(lines[end]))
				if m == nil {
					break
				}
				inner = append(inner, m[1])
				end++
			}
			var prepared []string
			prepared, base = prepareLines(inner, base)
			for _, line := range prepared {
				if line == "" {
					out = append(out, ">")
				} else {
					out = append(out, "> "+line)
				}
			}
			i = end
			continue
		case isListItem(bare):
			end := listEnd(lines, i)
			var list []string
			list, base = renderList(lines[i:end], base)
			out = append(out, list...)
			i = end
			continue
		default:
			bare = strings.TrimLeft(bare, " \t")
			dir := lineDir(bare)
			if base == dirNone {
				base = dir
			}
			out = append(out, markLine(bare, dir, base))
		}
		i++
	}
	return out, base
}

// renderList keeps a list as markdown when Slack draws it well, and writes it
// as numbered or bulleted lines when it does not (see PrepareMarkdown).
func renderList(lines []string, base direction) ([]string, direction) {
	var content []string
	ordered := false
	for _, line := range lines {
		if m := itemRe.FindStringSubmatch(stripMarks(line)); m != nil {
			content = append(content, m[5])
			ordered = ordered || isOrderedMarker(m[2])
		} else {
			content = append(content, line)
		}
	}
	dir := lineDir(strings.Join(content, " "))
	if base == dirNone {
		base = dir
	}
	manual := ordered && (dir == dirRTL || base == dirRTL) || dir != dirNone && dir != base

	type level struct {
		indent  int
		ordered bool
		n       int
	}
	var stack []level
	var out []string
	for _, line := range lines {
		bare := stripMarks(line)
		if strings.TrimSpace(bare) == "" {
			out = append(out, "")
			continue
		}
		m := itemRe.FindStringSubmatch(bare)
		if m == nil { // continuation line of an item
			text := trimMarks(strings.TrimSpace(bare))
			indent := ""
			if !manual {
				indent = bare[:len(bare)-len(strings.TrimLeft(bare, " \t"))]
			}
			out = append(out, indent+markLine(text, lineDir(text), base))
			continue
		}
		text := trimMarks(m[5])
		if !manual {
			out = append(out, m[1]+m[2]+m[3]+m[4]+markLine(text, lineDir(text), base))
			continue
		}

		indent := len(strings.ReplaceAll(m[1], "\t", "    "))
		for len(stack) > 0 && stack[len(stack)-1].indent > indent {
			stack = stack[:len(stack)-1]
		}
		isOrdered := isOrderedMarker(m[2])
		if len(stack) == 0 || stack[len(stack)-1].indent < indent || stack[len(stack)-1].ordered != isOrdered {
			if len(stack) > 0 && stack[len(stack)-1].indent == indent {
				stack = stack[:len(stack)-1]
			}
			start := 1
			if isOrdered {
				start, _ = strconv.Atoi(strings.TrimRight(m[2], ".)"))
			}
			stack = append(stack, level{indent: indent, ordered: isOrdered, n: start - 1})
		}
		top := &stack[len(stack)-1]
		var label string
		switch {
		case m[4] != "":
			label = strings.TrimSpace(m[4])
		case isOrdered:
			top.n++
			var nums []string
			for _, l := range stack {
				if l.ordered {
					nums = append(nums, strconv.Itoa(l.n))
				}
			}
			label = strings.Join(nums, ".") + "."
		default:
			label = manualBullets[min(len(stack)-1, len(manualBullets)-1)]
		}
		// The mark also keeps markdown from reading the line as a list item.
		mark := lrm
		if d := lineDir(text); d == dirRTL || d == dirNone && base == dirRTL {
			mark = rlm
		}
		out = append(out, wrapMarks(label+" "+text, mark))
	}
	return out, base
}

// renderTable rewrites a table with every row as wide as its header, and
// reverses the columns of a right-to-left table so it reads from the right.
func renderTable(header, separator string, body []string) []string {
	head := tableCells(header)
	width := len(head)
	aligns := fitRow(tableCells(separator), width)
	rows := [][]string{head}
	all := strings.Join(head, " ")
	for _, line := range body {
		row := fitRow(tableCells(line), width)
		rows = append(rows, row)
		all += " " + strings.Join(row, " ")
	}
	rtl := lineDir(all) == dirRTL

	for i, a := range aligns {
		left, right := strings.HasPrefix(a, ":"), strings.HasSuffix(a, ":")
		if rtl && left != right {
			left, right = right, left
		} else if rtl && !left {
			right = true
		}
		a = "---"
		if left {
			a = ":" + a
		}
		if right {
			a += ":"
		}
		aligns[i] = a
	}
	row := func(cells []string) string {
		cells = append([]string(nil), cells...)
		if rtl {
			for i, j := 0, len(cells)-1; i < j; i, j = i+1, j-1 {
				cells[i], cells[j] = cells[j], cells[i]
			}
		}
		for i, c := range cells {
			if dir := lineDir(c); dir == dirRTL {
				cells[i] = markLine(c, dir, dir)
			}
		}
		return "| " + strings.Join(cells, " | ") + " |"
	}
	out := []string{row(rows[0]), row(aligns)}
	for _, r := range rows[1:] {
		out = append(out, row(r))
	}
	return out
}

func fitRow(cells []string, width int) []string {
	row := make([]string, width)
	copy(row, cells)
	return row
}

// tableCells splits a table row on its unescaped pipes.
func tableCells(line string) []string {
	row := strings.TrimSpace(stripMarks(line))
	row = strings.TrimPrefix(row, "|")
	if strings.HasSuffix(row, "|") && !strings.HasSuffix(row, `\|`) {
		row = row[:len(row)-1]
	}
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|':
			cell.WriteString(`\|`)
			i++
		case row[i] == '|':
			cells = append(cells, trimMarks(strings.TrimSpace(cell.String())))
			cell.Reset()
		default:
			cell.WriteByte(row[i])
		}
	}
	return append(cells, trimMarks(strings.TrimSpace(cell.String())))
}

func isTableStart(lines []string, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	head, sep := strings.TrimSpace(stripMarks(lines[i])), strings.TrimSpace(stripMarks(lines[i+1]))
	return strings.HasPrefix(head, "|") && strings.Contains(sep, "-") && tableSepRe.MatchString(sep)
}

// markLine wraps a line in the mark for its direction when it needs one.
func markLine(s string, dir, base direction) string {
	switch {
	case dir == dirRTL || dir == dirNone && base == dirRTL && strings.TrimSpace(s) != "":
		return wrapMarks(s, rlm)
	case dir == dirLTR && base == dirRTL:
		return wrapMarks(s, lrm)
	}
	return s
}

// wrapMarks puts mark at both ends of s. Next to a bold, italic or strike
// delimiter the mark goes inside it: markdown ignores a delimiter that has a
// mark on its outer side and punctuation on its inner side.
func wrapMarks(s, mark string) string {
	body := strings.TrimRight(s, " \t\\")
	tail := s[len(body):]
	if mark == rlm {
		body = mentionRe.ReplaceAllString(body, lrm+"$1")
	}
	lead := leadDelimRe.FindString(body)
	trail := tailDelimRe.FindString(body[len(lead):])
	return lead + mark + body[len(lead):len(body)-len(trail)] + mark + trail + tail
}

// lineDir is the reading direction of a line: right to left once at least a
// third of its words are Hebrew or Arabic, since a Hebrew sentence often
// carries English product names and terms.
func lineDir(s string) direction {
	var rtl, ltr int
	for _, word := range strings.Fields(noDirRe.ReplaceAllString(s, " ")) {
		for _, r := range word {
			if unicode.In(r, unicode.Hebrew, unicode.Arabic, unicode.Syriac, unicode.Thaana, unicode.Nko) {
				rtl++
				break
			}
			if unicode.IsLetter(r) {
				ltr++
				break
			}
		}
	}
	switch {
	case rtl > 0 && rtl*2 >= ltr:
		return dirRTL
	case ltr > 0:
		return dirLTR
	}
	return dirNone
}

// stripMarks drops direction marks from the start of a line, also between its
// indentation, and from its end, keeping the indentation itself.
func stripMarks(line string) string {
	i := 0
	var indent strings.Builder
	for i < len(line) {
		switch {
		case line[i] == ' ' || line[i] == '\t':
			indent.WriteByte(line[i])
			i++
		case strings.HasPrefix(line[i:], "‎"), strings.HasPrefix(line[i:], "‏"):
			i += 3
		case strings.HasPrefix(line[i:], "؜"):
			i += 2
		default:
			return indent.String() + strings.TrimRight(line[i:], marks)
		}
	}
	return indent.String()
}

func trimMarks(s string) string { return strings.Trim(s, marks) }

func isListItem(line string) bool { return itemRe.MatchString(line) && !isHR(line) }

func isOrderedMarker(marker string) bool { return marker[0] >= '0' && marker[0] <= '9' }

// listEnd returns the index after the list that starts at lines[i]: its items,
// their indented continuation lines, and blank lines between them.
func listEnd(lines []string, i int) int {
	first := itemRe.FindStringSubmatch(stripMarks(lines[i]))
	end := i + 1
	blank := false
	for j := i + 1; j < len(lines); j++ {
		bare := stripMarks(lines[j])
		switch {
		case fenceRe.MatchString(bare):
			return end
		case strings.TrimSpace(bare) == "":
			blank = true
			continue
		case isListItem(bare):
			if blank && startsNewList(first, itemRe.FindStringSubmatch(bare)) {
				return end
			}
		case strings.HasPrefix(bare, "  "), strings.HasPrefix(bare, "\t"):
		default:
			return end
		}
		blank = false
		end = j + 1
	}
	return end
}

// startsNewList reports an item, after a blank line, that a reader sees as the
// start of another list even though markdown would merge the two.
func startsNewList(first, item []string) bool {
	if len(item[1]) > len(first[1]) {
		return false
	}
	if isOrderedMarker(item[2]) != isOrderedMarker(first[2]) {
		return true
	}
	if isOrderedMarker(item[2]) && strings.TrimRight(item[2], ".)") == "1" {
		return true
	}
	return lineDir(item[5]) != lineDir(first[5])
}

// fenceEnd returns the index after the code fence that opens at lines[i].
func fenceEnd(lines []string, i int) int {
	open := fenceRe.FindStringSubmatch(stripMarks(lines[i]))[1]
	for j := i + 1; j < len(lines); j++ {
		if m := fenceRe.FindStringSubmatch(stripMarks(lines[j])); m != nil && m[1][0] == open[0] && len(m[1]) >= len(open) {
			return j + 1
		}
	}
	return len(lines)
}

// isIndentedCode reports a line indented as a code block: four spaces or a
// tab, after a blank line or another code line, outside any list.
func isIndentedCode(line string, out []string) bool {
	if !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "\t") {
		return false
	}
	return len(out) == 0 || out[len(out)-1] == "" || strings.HasPrefix(out[len(out)-1], "    ") || strings.HasPrefix(out[len(out)-1], "\t")
}

func isHR(line string) bool {
	s := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(line), " ", ""), "\t", "")
	if len(s) < 3 || strings.Trim(s, s[:1]) != "" {
		return false
	}
	return s[0] == '-' || s[0] == '*' || s[0] == '_'
}
