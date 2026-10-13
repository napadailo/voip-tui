package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Translated text is aligned by terminal columns, not bytes or runes: CJK
// characters take two columns, fmt's %-10s counts them as one.

// StrWidth is the number of terminal columns s takes.
func StrWidth(s string) int { return runewidth.StringWidth(s) }

// Pad fills s with spaces on the right up to w columns.
func Pad(s string, w int) string {
	if d := w - StrWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// PadLeft fills s with spaces on the left up to w columns.
func PadLeft(s string, w int) string {
	if d := w - StrWidth(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}
