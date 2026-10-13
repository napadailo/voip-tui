package tui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/tui/theme"
)

// KVText renders key/value rows as aligned markup: keys (English, marked
// with N or as [2]string literals) are translated, a key starting with "#"
// is a section title, an empty row is a blank line.
func KVText(rows [][2]string) string { return KVTextWidth(rows, 0) }

// KVTextWidth aligns values after at least minKey columns of key.
func KVTextWidth(rows [][2]string, minKey int) string {
	w := minKey
	for _, r := range rows {
		if !strings.HasPrefix(r[0], "#") {
			w = max(w, StrWidth(T(r[0])))
		}
	}
	var b strings.Builder
	for _, r := range rows {
		switch {
		case r[0] == "" && r[1] == "":
			b.WriteString("\n")
		case strings.HasPrefix(r[0], "#"):
			fmt.Fprintf(&b, "%s[::b]%s[::-][-]\n", theme.Tag(theme.Title), tview.Escape(T(r[0][1:])))
		default:
			fmt.Fprintf(&b, "%s%s[-] %s\n", theme.Tag(theme.InfoKey), tview.Escape(Pad(T(r[0])+":", w+1)), tview.Escape(r[1]))
		}
	}
	return b.String()
}
