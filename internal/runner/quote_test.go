package runner

import (
	"strings"
	"testing"
)

// The cases match bash 5's printf %q.
func TestBashQuote(t *testing.T) {
	cases := map[string]string{
		"":                  "''",
		"plain/path-1.2_x":  "plain/path-1.2_x",
		"a b":               `a\ b`,
		"<pane>":            `\<pane\>`,
		"ACM-5 (fast)":      `ACM-5\ \(fast\)`,
		`{"a":1,"b":2}`:     `\{\"a\":1\,\"b\":2\}`,
		"it's":              `it\'s`,
		"#a":                `\#a`,
		"a#":                "a#",
		"~a":                `\~a`,
		"a~":                "a~",
		"a=~b":              `a=\~b`,
		"a:~b":              `a:\~b`,
		"=a":                "=a",
		"a\nb":              `$'a\nb'`,
		"a\tb'c":            `$'a\tb\'c'`,
		"\x01":              `$'\001'`,
		"\u00e9":            "\u00e9",
		"!$&*;?[]^`|\\":     `\!\$\&\*\;\?\[\]\^\` + "`" + `\|\\`,
		"key=value:%@+.,/-": `key=value:%@+.\,/-`,
	}
	for in, want := range cases {
		if got := BashQuote(in); got != want {
			t.Errorf("BashQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestLogArgHidesLongText(t *testing.T) {
	long := strings.Repeat("a", 201)
	got := LogArg(long)
	if !strings.HasPrefix(got, "<text 201 bytes sha256:") || len(got) != len("<text 201 bytes sha256:>")+12 {
		t.Errorf("LogArg(long) = %s", got)
	}
	if got := LogArg(strings.Repeat("a", 200)); got != strings.Repeat("a", 200) {
		t.Errorf("an argument of 200 characters is hidden: %s", got)
	}
}
