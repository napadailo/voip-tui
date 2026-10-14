package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/tui/theme"
)

// Card lays out a detail view the same way everywhere: a bold title, a dim
// line under it, prose, then sections of "label  value" lines with the
// labels of each section aligned. Labels and section names are translated
// (mark them with N), except labels added with KVRaw; values are markup, see
// Strong, Paint and Faint.
type Card struct {
	title string
	parts []cardPart
}

type cardKind int

const (
	cardSub cardKind = iota
	cardText
	cardSection
	cardKV
	cardLine
)

type cardPart struct {
	kind        cardKind
	label, text string
	raw         bool // the label is data, not translated
}

// labelText is the label as shown.
func (p cardPart) labelText() string {
	if p.raw {
		return p.label
	}
	return T(p.label)
}

// NewCard starts a card titled title.
func NewCard(title string) *Card { return &Card{title: title} }

// Sub adds a dim line under the title: the non-empty parts joined by " · ".
func (c *Card) Sub(parts ...string) *Card {
	var keep []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			keep = append(keep, p)
		}
	}
	if len(keep) > 0 {
		c.parts = append(c.parts, cardPart{kind: cardSub, text: strings.Join(keep, " · ")})
	}
	return c
}

// Text adds a paragraph of plain text.
func (c *Card) Text(s string) *Card {
	if strings.TrimSpace(s) != "" {
		c.parts = append(c.parts, cardPart{kind: cardText, text: s})
	}
	return c
}

// Section starts a section; note (plain text) follows the name, dimmed.
func (c *Card) Section(name string, note ...string) *Card {
	c.parts = append(c.parts, cardPart{kind: cardSection, label: name, text: strings.Join(note, " ")})
	return c
}

// KV adds a line: the translated label, then the value markup.
func (c *Card) KV(label, value string) *Card {
	c.parts = append(c.parts, cardPart{kind: cardKV, label: label, text: value})
	return c
}

// KVRaw adds a line whose label is data from the component (a command, a
// key, an interface name) and is shown as is.
func (c *Card) KVRaw(label, value string) *Card {
	c.parts = append(c.parts, cardPart{kind: cardKV, label: label, text: value, raw: true})
	return c
}

// Line adds markup in the value column, without a label (continuations,
// table headers).
func (c *Card) Line(markup string) *Card {
	c.parts = append(c.parts, cardPart{kind: cardLine, text: markup})
	return c
}

// String renders the card as tview markup.
func (c *Card) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s[::b]%s[::-][-]\n", theme.Tag(theme.Title), tview.Escape(c.title))
	width := 0 // of the labels in the current section
	for i, p := range c.parts {
		if i == 0 || p.kind == cardSection {
			width = c.labelWidth(i)
		}
		switch p.kind {
		case cardSub:
			b.WriteString(Faint(p.text) + "\n")
		case cardText:
			b.WriteString("\n" + tview.Escape(p.text) + "\n")
		case cardSection:
			fmt.Fprintf(&b, "\n%s[::b]%s[::-][-]", theme.Tag(theme.Accent), tview.Escape(T(p.label)))
			if p.text != "" {
				b.WriteString(Faint(" · " + p.text))
			}
			b.WriteString("\n")
		case cardKV:
			fmt.Fprintf(&b, "  %s%s[-]  %s\n", theme.Tag(theme.InfoKey), tview.Escape(Pad(p.labelText(), width)), p.text)
		case cardLine:
			fmt.Fprintf(&b, "  %s  %s\n", Pad("", width), p.text)
		}
	}
	return b.String()
}

// labelWidth is the widest label from part from to the next section.
func (c *Card) labelWidth(from int) int {
	w := 0
	for i := from; i < len(c.parts); i++ {
		p := c.parts[i]
		if i > from && p.kind == cardSection {
			break
		}
		if p.kind == cardKV {
			w = max(w, StrWidth(p.labelText()))
		}
	}
	return w
}

// Strong is a value to notice: bold, in the info value color.
func Strong(s string) string {
	return theme.Tag(theme.InfoValue) + "[::b]" + tview.Escape(s) + "[::-][-]"
}

// Paint is s in color c.
func Paint(c tcell.Color, s string) string { return theme.Tag(c) + tview.Escape(s) + "[-]" }

// Faint is s dimmed: secondary details.
func Faint(s string) string { return theme.Tag(theme.Dim) + tview.Escape(s) + "[-]" }
