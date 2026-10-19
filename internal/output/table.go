package output

import (
	"io"
	"strings"
	"text/tabwriter"
)

// Table writes rows as columns separated by two spaces, like column -t.
func Table(w io.Writer, header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range append([][]string{header}, rows...) {
		if _, err := io.WriteString(tw, strings.Join(r, "\t")+"\n"); err != nil {
			return err
		}
	}
	return tw.Flush()
}
