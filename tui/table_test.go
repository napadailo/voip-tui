package tui

import (
	"sort"
	"strings"
	"testing"
)

func TestSortLess(t *testing.T) {
	rows := []Row{
		{Cells: []string{"", "a"}, Key: "empty"},
		{Cells: []string{"17k +18"}, Sort: map[int]float64{0: 17000}, Key: "17k"},
		{Cells: []string{"998"}, Key: "998"},
		{Cells: []string{"n/a"}, Key: "text"},
		{Cells: []string{"2.1k"}, Sort: map[int]float64{0: 2100}, Key: "2.1k"},
		{Cells: []string{"[red]5%[-]"}, Markup: map[int]bool{0: true}, Key: "5%"},
	}
	order := func(asc bool) string {
		rs := append([]Row(nil), rows...)
		sort.SliceStable(rs, func(i, j int) bool { return sortLess(&rs[i], &rs[j], 0, asc) })
		var keys []string
		for _, r := range rs {
			keys = append(keys, r.Key)
		}
		return strings.Join(keys, " ")
	}
	// numbers by value (Sort values and plain numbers together), then text; empty cells last both ways
	if got := order(true); got != "5% 998 2.1k 17k text empty" {
		t.Errorf("asc: %s", got)
	}
	if got := order(false); got != "17k 2.1k 998 5% text empty" {
		t.Errorf("desc: %s", got)
	}
}

func TestMarkupCellText(t *testing.T) {
	r := Row{Cells: []string{"[red]SEC[-] [yellow]BRK[-]", "[x]"}, Markup: map[int]bool{0: true}}
	if r.text(0) != "SEC BRK" || r.text(1) != "[x]" || StrWidth(r.text(0)) != 7 {
		t.Errorf("%q %q", r.text(0), r.text(1))
	}
}
