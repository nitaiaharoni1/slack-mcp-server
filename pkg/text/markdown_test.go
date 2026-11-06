package text

import "testing"

func TestPrepareMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "English stays exactly as written",
			in:   "# Title\n\nPlain **bold** line.\n\n- a\n- b\n\n1. one\n2. two",
			want: "# Title\n\nPlain **bold** line.\n\n- a\n- b\n\n1. one\n2. two",
		},
		{
			name: "Hebrew line is wrapped in RLM",
			in:   "שלום עולם",
			want: rlm + "שלום עולם" + rlm,
		},
		{
			name: "caller's RLM does not double up",
			in:   rlm + "שלום עולם",
			want: rlm + "שלום עולם" + rlm,
		},
		{
			name: "caller's RLM in front of markdown does not hide it",
			in:   rlm + "- פריט",
			want: "- " + rlm + "פריט" + rlm,
		},
		{
			name: "mark goes inside bold that opens a line",
			in:   `**"מודגש"** בהתחלה`,
			want: "**" + rlm + `"מודגש"** בהתחלה` + rlm,
		},
		{
			name: "mark goes inside bold that closes a line",
			in:   "בסוף **מודגש.**",
			want: rlm + "בסוף **מודגש." + rlm + "**",
		},
		{
			name: "dashes under a line are a divider, not a heading",
			in:   "line\n---\nnext",
			want: "line\n\n---\nnext",
		},
		{
			name: "mention inside Hebrew keeps its @ in front",
			in:   "שלום <@U1> כאן",
			want: rlm + "שלום " + lrm + "<@U1> כאן" + rlm,
		},
		{
			name: "mention opening a Hebrew line",
			in:   "<@U1> פותח",
			want: rlm + lrm + "<@U1> פותח" + rlm,
		},
		{
			name: "Hebrew bullets stay native",
			in:   "- ראשון\n- שני\n  - מקונן",
			want: "- " + rlm + "ראשון" + rlm + "\n- " + rlm + "שני" + rlm + "\n  - " + rlm + "מקונן" + rlm,
		},
		{
			name: "Hebrew tasks stay native",
			in:   "- [ ] פתוחה\n- [x] סגורה",
			want: "- [ ] " + rlm + "פתוחה" + rlm + "\n- [x] " + rlm + "סגורה" + rlm,
		},
		{
			name: "Hebrew numbers become text, nested ones keep their parent",
			in:   "1. ראשון\n2. שני\n   1. פנימי\n3. שלישי",
			want: rlm + "1. ראשון" + rlm + "\n" + rlm + "2. שני" + rlm + "\n" + rlm + "2.1. פנימי" + rlm + "\n" + rlm + "3. שלישי" + rlm,
		},
		{
			name: "numbering continues across a blank line",
			in:   "1. א\n\n2. ב",
			want: rlm + "1. א" + rlm + "\n\n" + rlm + "2. ב" + rlm,
		},
		{
			name: "Hebrew bullets after English bullets are their own list",
			in:   "- English\n- Two\n\n- עברית\n- שני",
			want: "- English\n- Two\n\n" + rlm + "● עברית" + rlm + "\n" + rlm + "● שני" + rlm,
		},
		{
			name: "Hebrew numbers after English numbers restart",
			in:   "1. English\n2. Two\n\n1. עברית\n2. שני",
			want: "1. English\n2. Two\n\n" + rlm + "1. עברית" + rlm + "\n" + rlm + "2. שני" + rlm,
		},
		{
			name: "continuation line stays in its item",
			in:   "- a\n\n  continuation\n- b",
			want: "- a\n\n  continuation\n- b",
		},
		{
			name: "Hebrew table reads from the right and is padded",
			in:   "| שם | ציון |\n|---|--:|\n| דנה | 9 |\n| יוסי |",
			want: "| " + rlm + "ציון" + rlm + " | " + rlm + "שם" + rlm + " |\n| :--- | ---: |\n| 9 | " + rlm + "דנה" + rlm + " |\n|  | " + rlm + "יוסי" + rlm + " |",
		},
		{
			name: "English table keeps its columns",
			in:   "| name | score |\n|---|--:|\n| a | 9 |",
			want: "| name | score |\n| --- | ---: |\n| a | 9 |",
		},
		{
			name: "code is untouched",
			in:   "```\nשלום **x**\n```\nאחרי",
			want: "```\nשלום **x**\n```\n" + rlm + "אחרי" + rlm,
		},
		{
			name: "indented code is untouched",
			in:   "    indented עברית\n\nאחרי",
			want: "    indented עברית\n\n" + rlm + "אחרי" + rlm,
		},
		{
			name: "quote keeps its markdown",
			in:   "> ציטוט **מודגש**\n> - פריט",
			want: "> " + rlm + "ציטוט **מודגש" + rlm + "**\n> - " + rlm + "פריט" + rlm,
		},
		{
			name: "direction follows the majority of words, Hebrew wins a tie",
			in:   "ה-deploy של vistral-staging עבר\ndeploy done: vistral-staging is fine, עבר",
			want: rlm + "ה-deploy של vistral-staging עבר" + rlm + "\n" + lrm + "deploy done: vistral-staging is fine, עבר" + lrm,
		},
		{
			name: "a line with no words gets no marks",
			in:   "https://example.com\n:tada:",
			want: "https://example.com\n:tada:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PrepareMarkdown(tt.in); got != tt.want {
				t.Errorf("PrepareMarkdown(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBidiLines(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{rlm + "שלום", rlm + "שלום" + rlm},
		{"שלום\nhello\n\n<@U1> שלום", rlm + "שלום" + rlm + "\n" + lrm + "hello" + lrm + "\n\n" + rlm + lrm + "<@U1> שלום" + rlm},
	}
	for _, tt := range tests {
		if got := BidiLines(tt.in); got != tt.want {
			t.Errorf("BidiLines(%q)\n got %q\nwant %q", tt.in, got, tt.want)
		}
	}
}

func TestInlineMrkdwn(t *testing.T) {
	tests := []struct{ in, want string }{
		{"**a** *b* ~~c~~ [d](https://e.com)", "*a* _b_ ~c~ <https://e.com|d>"},
		{"העברה עם **הערה**, עם סוף.", rlm + "העברה עם *הערה*, עם סוף." + rlm},
	}
	for _, tt := range tests {
		if got := InlineMrkdwn(tt.in); got != tt.want {
			t.Errorf("InlineMrkdwn(%q)\n got %q\nwant %q", tt.in, got, tt.want)
		}
	}
}

func TestMarkdownFallback(t *testing.T) {
	in := "# Title\n\n**bold** ~~x~~ `c` [d](https://e.com)\n\n---\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```\n**kept**\n```"
	want := "Title\n\nbold x c d\n\n\na | b\n1 | 2\n\n**kept**"
	if got := MarkdownFallback(in); got != want {
		t.Errorf("MarkdownFallback(%q)\n got %q\nwant %q", in, got, want)
	}
}
