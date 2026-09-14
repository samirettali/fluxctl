package main

import "testing"

func TestHTMLToText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"paragraphs", "<p>One</p><p>Two &amp; three</p>", "One\n\nTwo & three"},
		{"inline", "<p>A <a href=\"x\">link</a> and <strong>bold</strong>.</p>", "A link and bold."},
		{"drops scripts and styles", "<style>p{}</style><p>Kept</p><script>alert(1)</script>", "Kept"},
		{"list items", "<ul><li>a</li><li>b</li></ul>", "a\nb"},
		{"table cells", "<table><tr><td>Name</td><td>Value</td></tr><tr><th>k</th><td>v</td></tr></table>", "Name\tValue\nk\tv"},
		{"definition list", "<dl><dt>Term</dt><dd>Def</dd></dl>", "Term\nDef"},
		{"headings", "<h1>Title</h1><p>Body</p>", "Title\n\nBody"},
		{"source newlines are not breaks", "<p>  spaced \n\n\n  out  </p>", "spaced out"},
		{"nbsp", "<p>a&nbsp;b</p>", "a b"},
		{"self closing br", "line<br/>break", "line\nbreak"},
		{"self closing svg keeps what follows", `<svg class="i"/><p>A</p><svg><path/></svg><p>B</p>`, "A\n\nB"},
		{"tab inside cell text is collapsed", "<td>a \t b</td><td>c</td>", "a b\tc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := htmlToText(tc.in); got != tc.want {
				t.Fatalf("htmlToText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
