package tui

import (
	"strings"
	"testing"
)

func TestCardAlignsLabelsPerSection(t *testing.T) {
	out := StripTags(NewCard("name").Sub("a", "", "b").Text("prose").
		Section("First").KV("x", "1").KV("longer", "2").
		Section("Second", "note").KV("y", "3").Line("more").
		String())
	want := "name\na · b\n\nprose\n\nFirst\n  x       1\n  longer  2\n\nSecond · note\n  y  3\n     more\n"
	if out != want {
		t.Fatalf("got\n%q\nwant\n%q", out, want)
	}
}

func TestCardEscapes(t *testing.T) {
	out := NewCard("[red]").KV("k", Strong("[x]")).String()
	if strings.Contains(out, "[red]\n") || !strings.Contains(out, "[red[]") || !strings.Contains(out, "[x[]") {
		t.Fatalf("title and values must be escaped: %q", out)
	}
}
