package ui

import (
	"strings"
	"testing"
)

func TestWidthPadding(t *testing.T) {
	if strWidth("主机") != 4 || pad("主机:", 8) != "主机:   " || padLeft("包", 4) != "  包" {
		t.Fatalf("%d %q %q", strWidth("主机"), pad("主机:", 8), padLeft("包", 4))
	}
	// CJK text without spaces is still wrapped at templateWrap columns
	text := strings.Repeat("中文说明", 40)
	for _, l := range strings.Split(strings.TrimRight(wrapped("  ", "x:", 2, text, "    "), "\n"), "\n") {
		if strWidth(l) > templateWrap {
			t.Fatalf("line of %d columns: %q", strWidth(l), l)
		}
	}
}
