package text

import (
	"crypto/x509"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/slack-go/slack"
	"go.uber.org/zap"
	"golang.org/x/net/publicsuffix"
)

func AttachmentToText(att slack.Attachment) string {
	var parts []string

	if att.Title != "" {
		if att.TitleLink != "" {
			parts = append(parts, fmt.Sprintf("Title: [%s](%s)", att.Title, att.TitleLink))
		} else {
			parts = append(parts, fmt.Sprintf("Title: %s", att.Title))
		}
	}

	if att.AuthorName != "" {
		parts = append(parts, fmt.Sprintf("Author: %s", att.AuthorName))
	}

	if att.Pretext != "" {
		parts = append(parts, fmt.Sprintf("Pretext: %s", att.Pretext))
	}

	if att.Text != "" {
		parts = append(parts, fmt.Sprintf("Text: %s", att.Text))
	}

	for _, f := range att.Fields {
		if f.Title != "" && f.Value != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", f.Title, f.Value))
		} else if f.Title != "" {
			parts = append(parts, f.Title)
		} else if f.Value != "" {
			parts = append(parts, f.Value)
		}
	}

	if att.Footer != "" {
		ts, _ := TimestampToIsoRFC3339(string(att.Ts) + ".000000")

		parts = append(parts, fmt.Sprintf("Footer: %s @ %s", att.Footer, ts))
	}

	if blocksText := BlocksToText(att.Blocks); blocksText != "" {
		parts = append(parts, fmt.Sprintf("Blocks: %s", blocksText))
	}

	result := strings.Join(parts, "; ")

	result = strings.ReplaceAll(result, "\n", " ")
	result = strings.ReplaceAll(result, "\r", " ")
	result = strings.ReplaceAll(result, "\t", " ")
	result = strings.TrimSpace(result)

	return result
}

// BlocksToText renders Slack Block Kit blocks as text, with a blank line
// between blocks. What is inside them keeps its structure: line breaks, list
// markers and their nesting, quotes, code, tables and dividers. Styles,
// mentions and links use Slack's mrkdwn markup, as a message's text does.
func BlocksToText(blocks slack.Blocks) string {
	var parts []string

	for _, block := range blocks.BlockSet {
		var part string
		switch b := block.(type) {
		case *slack.HeaderBlock:
			if b.Text != nil {
				part = b.Text.Text
			}
		case *slack.SectionBlock:
			var lines []string
			if b.Text != nil && b.Text.Text != "" {
				lines = append(lines, b.Text.Text)
			}
			for _, field := range b.Fields {
				if field != nil && field.Text != "" {
					lines = append(lines, field.Text)
				}
			}
			part = strings.Join(lines, "\n")
		case *slack.RichTextBlock:
			part = richTextBlockToText(b)
		case *slack.TableBlock:
			part = tableToText(b)
		case *slack.DividerBlock:
			part = "---"
		case *slack.ContextBlock:
			var texts []string
			for _, elem := range b.ContextElements.Elements {
				if txt, ok := elem.(*slack.TextBlockObject); ok && txt != nil && txt.Text != "" {
					texts = append(texts, txt.Text)
				}
			}
			part = strings.Join(texts, " ")
		}
		if part = strings.Trim(part, "\n"); part != "" {
			parts = append(parts, part)
		}
	}

	return strings.Join(parts, "\n\n")
}

// MessageText returns a message's text, or its blocks rendered as text when
// they hold more lines. Slack flattens the text of a message posted with
// blocks, such as a markdown block, into a single line.
func MessageText(msgText string, blocks slack.Blocks) string {
	if blocksText := BlocksToText(blocks); contentLines(blocksText) > contentLines(msgText) {
		return blocksText
	}
	return msgText
}

func contentLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// FilesToText extracts text metadata from email file attachments.
// Separators are chosen so the metadata survives the text-processing pipeline.
func FilesToText(files []slack.File) string {
	var parts []string

	for _, f := range files {
		if f.Filetype != "email" && f.Mode != "email" {
			continue
		}

		var emailParts []string

		if len(f.From) > 0 {
			if s := formatEmailUser(f.From[0]); s != "" {
				emailParts = append(emailParts, "From: "+s)
			}
		}

		if len(f.Cc) > 0 {
			var ccParts []string
			for _, c := range f.Cc {
				if s := formatEmailUser(c); s != "" {
					ccParts = append(ccParts, s)
				}
			}
			if len(ccParts) > 0 {
				emailParts = append(emailParts, "CC: "+strings.Join(ccParts, "/"))
			}
		}

		if f.Subject != "" {
			emailParts = append(emailParts, fmt.Sprintf("Subject: %s", f.Subject))
		} else if f.Title != "" {
			emailParts = append(emailParts, fmt.Sprintf("Subject: %s", f.Title))
		}

		if len(emailParts) > 0 {
			parts = append(parts, "Email, "+strings.Join(emailParts, ", "))
		}
	}

	return strings.Join(parts, " ")
}

// richTextBlockToText joins the elements of a rich text block. A section
// carries its own line breaks; a list, quote or code block always starts and
// ends a line.
func richTextBlockToText(rtb *slack.RichTextBlock) string {
	var b strings.Builder

	for _, elem := range rtb.Elements {
		t := richTextElementToText(elem)
		if t == "" {
			continue
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(t)
	}

	return b.String()
}

func richTextElementToText(elem slack.RichTextElement) string {
	switch e := elem.(type) {
	case *slack.RichTextSection:
		return richTextSectionToText(e.Elements)
	case *slack.RichTextList:
		return richTextListToText(e)
	case *slack.RichTextQuote:
		quoted := strings.Trim(richTextSectionToText(e.Elements), "\n")
		if quoted == "" {
			return ""
		}
		lines := strings.Split(quoted, "\n")
		for i, line := range lines {
			if line == "" {
				lines[i] = ">"
			} else {
				lines[i] = "> " + line
			}
		}
		return strings.Join(lines, "\n")
	case *slack.RichTextPreformatted:
		return "```\n" + strings.Trim(richTextSectionToText(e.Elements), "\n") + "\n```"
	}
	return ""
}

// richTextListToText writes one line per item, four spaces deeper per level
// of nesting. Slack sends a nested list as a sibling list with a higher
// indent, and a list that resumes numbering with an offset.
func richTextListToText(l *slack.RichTextList) string {
	indent := strings.Repeat("    ", l.Indent)
	var lines []string
	n := l.Offset
	for _, elem := range l.Elements {
		item := strings.Trim(richTextElementToText(elem), "\n")
		if _, nested := elem.(*slack.RichTextList); nested {
			lines = append(lines, item)
			continue
		}
		marker := "- "
		if l.Style == slack.RTEListOrdered {
			n++
			marker = strconv.Itoa(n) + ". "
		}
		item = strings.ReplaceAll(item, "\n", "\n"+indent+strings.Repeat(" ", len(marker)))
		lines = append(lines, indent+marker+item)
	}
	return strings.Join(lines, "\n")
}

// tableToText writes a table block as a markdown table whose first row is the
// header. A table with no text in any cell gives nothing.
func tableToText(t *slack.TableBlock) string {
	var lines []string
	filled := false
	for i, row := range t.Rows {
		cells := make([]string, len(row))
		for j, cell := range row {
			if cell == nil {
				continue
			}
			c := strings.Trim(richTextBlockToText(cell), "\n")
			c = strings.ReplaceAll(strings.ReplaceAll(c, "|", `\|`), "\n", " ")
			cells[j] = strings.TrimSpace(c)
			filled = filled || cells[j] != ""
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
		if i == 0 {
			lines = append(lines, "|"+strings.Repeat(" --- |", len(row)))
		}
	}
	if !filled {
		return ""
	}
	return strings.Join(lines, "\n")
}

func formatEmailUser(u slack.EmailFileUserInfo) string {
	addr := strings.ReplaceAll(u.Address, "@", " at ")
	if u.Name != "" && addr != "" {
		return u.Name + " - " + addr
	} else if u.Name != "" {
		return u.Name
	} else if addr != "" {
		return addr
	}
	return ""
}

func richTextSectionToText(elems []slack.RichTextSectionElement) string {
	var b strings.Builder

	for _, elem := range elems {
		switch e := elem.(type) {
		case *slack.RichTextSectionTextElement:
			b.WriteString(styleText(e.Text, e.Style))
		case *slack.RichTextSectionLinkElement:
			link := e.URL
			if e.URL == "" {
				link = e.Text
			} else if e.Text != "" && e.Text != e.URL {
				link = "<" + e.URL + "|" + e.Text + ">"
			}
			b.WriteString(styleText(link, e.Style))
		case *slack.RichTextSectionUserElement:
			b.WriteString(styleText("<@"+e.UserID+">", e.Style))
		case *slack.RichTextSectionChannelElement:
			b.WriteString(styleText("<#"+e.ChannelID+">", e.Style))
		case *slack.RichTextSectionUserGroupElement:
			b.WriteString("<!subteam^" + e.UsergroupID + ">")
		case *slack.RichTextSectionBroadcastElement:
			if e.Range != "" {
				b.WriteString("<!" + e.Range + ">")
			}
		case *slack.RichTextSectionEmojiElement:
			emoji := ":" + e.Name + ":"
			if e.SkinTone > 0 {
				emoji += ":skin-tone-" + strconv.Itoa(e.SkinTone) + ":"
			}
			b.WriteString(styleText(emoji, e.Style))
		case *slack.RichTextSectionDateElement:
			if e.Fallback != nil {
				b.WriteString(*e.Fallback)
			} else {
				b.WriteString(e.Timestamp.Time().UTC().Format(time.RFC3339))
			}
		}
	}

	return b.String()
}

// styleText wraps each line of s in mrkdwn style markers, leaving whitespace
// and direction marks at either end outside them.
func styleText(s string, style *slack.RichTextSectionTextStyle) string {
	if style == nil {
		return s
	}
	var open, closing string
	for _, m := range []struct {
		on   bool
		mark string
	}{{style.Bold, "*"}, {style.Italic, "_"}, {style.Strike, "~"}, {style.Code, "`"}} {
		if m.on {
			open += m.mark
			closing = m.mark + closing
		}
	}
	if open == "" {
		return s
	}
	isPadding := func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(marks, r) }
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		left := strings.TrimLeftFunc(line, isPadding)
		core := strings.TrimRightFunc(left, isPadding)
		if core != "" {
			lines[i] = line[:len(line)-len(left)] + open + core + closing + left[len(core):]
		}
	}
	return strings.Join(lines, "\n")
}

func AttachmentsTo2CSV(msgText string, attachments []slack.Attachment) string {
	if len(attachments) == 0 {
		return ""
	}

	var descriptions []string
	for _, att := range attachments {
		plainText := AttachmentToText(att)
		if plainText != "" {
			descriptions = append(descriptions, fmt.Sprintf("%s", plainText))
		}
	}

	prefix := ""
	if msgText != "" {
		prefix = ". "
	}

	return prefix + strings.Join(descriptions, ", ")
}

func IsUnfurlingEnabled(text string, opt string, logger *zap.Logger) bool {
	if opt == "" || opt == "no" || opt == "false" || opt == "0" {
		return false
	}

	if opt == "yes" || opt == "true" || opt == "1" {
		return true
	}

	allowed := make(map[string]struct{}, 0)
	for _, d := range strings.Split(opt, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		allowed[d] = struct{}{}
	}

	urlRe := regexp.MustCompile(`https?://[^\s]+`)
	urls := urlRe.FindAllString(text, -1)
	for _, rawURL := range urls {
		u, err := url.Parse(rawURL)
		if err != nil || u.Host == "" {
			continue
		}
		host := strings.ToLower(u.Host)
		if idx := strings.Index(host, ":"); idx != -1 {
			host = host[:idx]
		}
		host = strings.TrimPrefix(host, "www.")
		if _, ok := allowed[host]; !ok {
			if logger != nil {
				logger.Warn("Security: attempt to unfurl non-whitelisted host",
					zap.String("host", host),
					zap.String("allowed", opt),
				)
			}
			return false
		}
	}

	txtNoURLs := urlRe.ReplaceAllString(text, " ")

	domRe := regexp.MustCompile(`\b(?:[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?\.)+[A-Za-z]{2,}\b`)
	doms := domRe.FindAllString(txtNoURLs, -1)

	for _, d := range doms {
		d = strings.ToLower(d)

		if _, icann := publicsuffix.PublicSuffix(d); !icann {
			continue
		}

		if _, ok := allowed[d]; !ok {
			if logger != nil {
				logger.Warn("Security: attempt to unfurl non-whitelisted host",
					zap.String("host", d),
					zap.String("allowed", opt),
				)
			}
			return false
		}
	}

	return true
}

func Workspace(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	parts := strings.Split(host, ".")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid Slack URL: %q", rawURL)
	}
	return parts[0], nil
}

func TimestampToIsoRFC3339(slackTS string) (string, error) {
	parts := strings.Split(slackTS, ".")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid slack timestamp format: %s", slackTS)
	}

	seconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", fmt.Errorf("failed to parse seconds: %v", err)
	}

	microseconds, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", fmt.Errorf("failed to parse microseconds: %v", err)
	}

	t := time.Unix(seconds, microseconds*1000)

	return t.UTC().Format(time.RFC3339), nil
}

func ProcessText(s string) string {
	s = normalizeLinks(s)
	s = stripUnsafeRunes(s)
	s = collapseInlineSpaces(s)
	s = slackEntities.Replace(s) // Slack stores &, < and > as entities

	return strings.TrimSpace(s)
}

func HumanizeCertificates(certs []*x509.Certificate) string {
	var descriptions []string
	for _, cert := range certs {
		subjectCN := cert.Subject.CommonName
		issuerCN := cert.Issuer.CommonName
		expiry := cert.NotAfter.Format("2006-01-02")

		description := fmt.Sprintf("CN=%s (Issuer CN=%s, expires %s)", subjectCN, issuerCN, expiry)
		descriptions = append(descriptions, description)
	}
	return strings.Join(descriptions, ", ")
}

var slackEntities = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">")

var (
	slackLinkRegex    = regexp.MustCompile(`<(https?://[^>|]+)\|([^>]+)>`)
	markdownLinkRegex = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`)
	htmlLinkRegex     = regexp.MustCompile(`<a\s+href=["']([^"']+)["'][^>]*>([^<]+)</a>`)
	inlineSpaceRegex  = regexp.MustCompile(`(\S)[ \t]+`)
)

func normalizeLinks(text string) string {
	isLastInText := func(original string, currentText string) bool {
		linkPos := strings.LastIndex(currentText, original)
		if linkPos == -1 {
			return false
		}
		afterLink := strings.TrimSpace(currentText[linkPos+len(original):])
		return afterLink == ""
	}

	render := func(url, linkText string, isLast bool) string {
		out := url + " - " + linkText
		if !isLast {
			out += ","
		}
		return out
	}

	for _, match := range slackLinkRegex.FindAllStringSubmatch(text, -1) {
		original := match[0]
		text = strings.Replace(text, original, render(match[1], match[2], isLastInText(original, text)), 1)
	}

	for _, match := range markdownLinkRegex.FindAllStringSubmatch(text, -1) {
		original := match[0]
		text = strings.Replace(text, original, render(match[2], match[1], isLastInText(original, text)), 1)
	}

	for _, match := range htmlLinkRegex.FindAllStringSubmatch(text, -1) {
		original := match[0]
		text = strings.Replace(text, original, render(match[1], match[2], isLastInText(original, text)), 1)
	}

	return text
}

// stripUnsafeRunes removes runes that are display-corrupting or carry no
// semantic content: C0/C1 controls (except \t \n \r), DEL, BOM, ZWSP,
// LRM/RLM, bidi overrides, and bidi isolates. Bidi overrides are a known
// prompt-injection vector in chat corpora. U+200C (ZWNJ) and U+200D (ZWJ)
// are preserved: they are required for Persian and Arabic letter joining
// and for emoji ZWJ sequences such as family and flag emoji.
func stripUnsafeRunes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7F:
			continue
		case r >= 0x80 && r <= 0x9F:
			continue
		case r == 0xFEFF:
			continue
		case r == 0x200B, r == 0x200E, r == 0x200F: // ZWSP, LRM, RLM; U+200C ZWNJ and U+200D ZWJ preserved
			continue
		case r >= 0x202A && r <= 0x202E:
			continue
		case r >= 0x2066 && r <= 0x2069:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// collapseInlineSpaces squeezes runs of spaces and tabs after a word into one
// space. Indentation, which carries list nesting, and code inside ``` fences
// are left as they are.
func collapseInlineSpaces(s string) string {
	parts := strings.Split(s, "```")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = inlineSpaceRegex.ReplaceAllString(parts[i], "${1} ")
	}
	return strings.Join(parts, "```")
}
