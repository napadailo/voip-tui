package tui

import (
	"regexp"
	"testing"

	"github.com/rivo/tview"
)

func TestHighlightMatches(t *testing.T) {
	re := regexp.MustCompile("(?i)rtp")
	for _, c := range []struct {
		name, in, want string
		n              int
	}{
		{"plain", "a RTP b rtp", `a ["m0"][black:yellow]RTP[""][-:-:-] b ["m1"][black:yellow]rtp[""][-:-:-]`, 2},
		{"color restored", "[aqua]x RTP y[-]", `[aqua]x ["m0"][black:yellow]RTP[""][aqua:-:-] y[-]`, 1},
		{"cumulative style", "[aqua][::b]RTP[-]", `[aqua][::b]["m0"][black:yellow]RTP[""][aqua:-:b][-]`, 1},
		{"tag inside match", "R[red]TP z", `["m0"][black:yellow]RTP[""][red:-:-] z`, 1},
		{"escaped tag kept whole", "[rtp[] x", `["m0"][black:yellow][rtp[][""][-:-:-] x`, 1},
		{"no match", "[aqua]none[-]", "[aqua]none[-]", 0},
	} {
		got, n := HighlightMatches(c.in, re)
		if got != c.want || n != c.n {
			t.Errorf("%s: got %q (%d), want %q (%d)", c.name, got, n, c.want, c.n)
		}
	}
}

// The shown text must not change: only regions and colors are added.
func TestHighlightMatchesKeepsText(t *testing.T) {
	in := "[aqua]Call[-]\n" + tview.Escape("[x] SSRC f0fd") + " [::b]RTP[::-] 10.0.0.1:4000 → rtp"
	out, n := HighlightMatches(in, regexp.MustCompile("(?i)rtp|0\\.0"))
	if n != 3 {
		t.Fatalf("regions %d, want 3: %q", n, out)
	}
	_, before := tokenizeMarkup(in)
	_, after := tokenizeMarkup(out)
	if before != after {
		t.Fatalf("shown text changed:\n%q\n%q", before, after)
	}
}
