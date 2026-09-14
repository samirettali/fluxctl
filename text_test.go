package main

import "testing"

func TestHTMLToText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"paragraphs", "<p>One</p><p>Two &amp; three</p>", "One\n\nTwo & three"},
		{"inline", "<p>A <a href=\"x\">link</a> and <strong>bold</strong>.</p>", "A link and bold."},
		{"drops scripts and styles", "<style>p{}</style><p>Kept</p><script>alert(1)</script>", "Kept"},
		{"list items", "<ul><li>a</li><li>b</li></ul>", "a\n\nb"},
		{"collapses whitespace", "<p>  spaced \n\n\n  out  </p>", "spaced\n\nout"},
		{"nbsp", "<p>a&nbsp;b</p>", "a b"},
		{"self closing br", "line<br/>break", "line\nbreak"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := htmlToText(tc.in); got != tc.want {
				t.Fatalf("htmlToText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
