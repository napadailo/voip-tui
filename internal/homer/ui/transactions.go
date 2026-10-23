package ui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/tui/theme"
)

// txKeys names the SIP transaction of each message shown: its session and
// CSeq, so a proxy's hops of one request are one transaction. An ACK to a
// failed INVITE belongs to the INVITE; RTCP and logs have none.
func (v *CallView) txKeys() []string {
	keys := make([]string, len(v.vis))
	failed := map[string]bool{} // session and CSeq number of INVITEs answered 300 or more
	for j, i := range v.vis {
		c := v.tx.Data.Calldata[i]
		m := v.message(i)
		if msgKind(m, c.Method) != "SIP" {
			continue
		}
		num, method := cseqOf(m)
		if num == "" {
			continue
		}
		inv := c.Sid + " " + num
		switch {
		case method == "ACK" && failed[inv]:
			method = "INVITE"
		case method == "INVITE" && !v.isRequest(i):
			if code, _ := strconv.Atoi(c.Method); code >= 300 {
				failed[inv] = true
			}
		}
		keys[j] = inv + " " + method
	}
	return keys
}

// cseqOf reads the CSeq of a message: the field, else the header in raw.
func cseqOf(m map[string]any) (num, method string) {
	s := str(m, "cseq")
	if s == "" {
		for _, l := range strings.Split(str(m, "raw"), "\n") {
			if name, val, ok := strings.Cut(l, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "CSeq") {
				s = val
				break
			}
		}
	}
	f := strings.Fields(s)
	if len(f) < 2 {
		return "", ""
	}
	return f[0], strings.ToUpper(f[1])
}

// txColors tell transactions next to each other apart; read when drawn,
// so they follow the theme.
func txColors() []tcell.Color {
	return []tcell.Color{theme.Accent, theme.Marked, theme.InfoKey, theme.OK}
}

// txGraph draws each transaction as a bracket from its first message to
// its last: ┌ ├ └, ╶ for one message. Transactions open at the same time
// take columns side by side, as branches in git log --graph do. It returns
// a cell per row and the width of the column.
func txGraph(keys []string) ([]string, int) {
	first, last, color := map[string]int{}, map[string]int{}, map[string]tcell.Color{}
	colors := txColors()
	for r, k := range keys {
		if k == "" {
			continue
		}
		if _, ok := first[k]; !ok {
			first[k] = r
			color[k] = colors[len(color)%len(colors)]
		}
		last[k] = r
	}
	var slots []string // the transaction open in each column
	cells := make([]string, len(keys))
	width := 0
	for r, k := range keys {
		if k != "" && first[k] == r {
			s := 0
			for s < len(slots) && slots[s] != "" {
				s++
			}
			if s == len(slots) {
				slots = append(slots, "")
			}
			slots[s] = k
		}
		var b strings.Builder
		for _, open := range slots {
			glyph := " "
			switch {
			case open == "":
			case open != k:
				glyph = "│"
			case first[k] == r && last[k] == r:
				glyph = "╶"
			case first[k] == r:
				glyph = "┌"
			case last[k] == r:
				glyph = "└"
			default:
				glyph = "├"
			}
			if open != "" {
				b.WriteString(theme.Tag(color[open]))
			}
			b.WriteString(glyph)
		}
		cells[r] = b.String()
		width = max(width, len(slots))
		if k != "" && last[k] == r {
			for s := range slots {
				if slots[s] == k {
					slots[s] = ""
				}
			}
			for len(slots) > 0 && slots[len(slots)-1] == "" {
				slots = slots[:len(slots)-1]
			}
		}
	}
	return cells, width
}
