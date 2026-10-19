package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// LogArg renders one argument for a log line. An argument over 200
// characters, typically a prompt, appears as its size and hash so the log
// never holds its text.
func LogArg(a string) string {
	if utf8.RuneCountInString(a) > 200 {
		sum := sha256.Sum256([]byte(a))
		return fmt.Sprintf("<text %d bytes sha256:%s>", len(a), hex.EncodeToString(sum[:])[:12])
	}
	return BashQuote(a)
}

// BashQuote quotes s the way bash's printf %q does, so log and dry-run lines
// can be pasted into a shell.
func BashQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return ansiCQuote(s)
		}
	}
	var b strings.Builder
	for i, r := range s {
		switch {
		case strings.ContainsRune(" !\"$&'()*,;<>?[\\]^`{|}", r):
			b.WriteByte('\\')
		case r == '#' && i == 0:
			b.WriteByte('\\')
		case r == '~' && (i == 0 || s[i-1] == '=' || s[i-1] == ':'):
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func ansiCQuote(s string) string {
	var b strings.Builder
	b.WriteString("$'")
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\v':
			b.WriteString(`\v`)
		case 0x1b:
			b.WriteString(`\E`)
		case '\'':
			b.WriteString(`\'`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\%03o`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('\'')
	return b.String()
}
