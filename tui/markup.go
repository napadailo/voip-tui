package tui

import (
	"fmt"
	"regexp"
	"strings"
)

// Tokens of tview markup, as tview's own parser tells them apart: an escaped
// tag ("[red[]" shows "[red]"), a region tag and a style tag.
var (
	escapedTagRe = regexp.MustCompile(`^\[[^\[\]]+\[+\]`)
	regionTagRe  = regexp.MustCompile(`^\["[^"]*"\]`)
	styleTagRe   = regexp.MustCompile(`^\[[a-zA-Z0-9#:\-]+\]`)
)

type markupToken struct {
	raw, text string // as written and as shown
	style     bool
	atomic    bool // shown text differs from raw: cannot be split
	start     int  // offset of text in the shown string
}

func tokenizeMarkup(markup string) ([]markupToken, string) {
	var toks []markupToken
	var plain strings.Builder
	add := func(t markupToken) {
		t.start = plain.Len()
		plain.WriteString(t.text)
		toks = append(toks, t)
	}
	lit := 0 // start of the pending literal text
	flush := func(i int) {
		if i > lit {
			add(markupToken{raw: markup[lit:i], text: markup[lit:i]})
		}
	}
	for i := 0; i < len(markup); {
		if markup[i] != '[' {
			i++
			continue
		}
		rest := markup[i:]
		var t markupToken
		switch {
		case escapedTagRe.MatchString(rest):
			m := escapedTagRe.FindString(rest)
			t = markupToken{raw: m, text: m[:len(m)-2] + "]", atomic: true}
		case regionTagRe.MatchString(rest):
			t = markupToken{raw: regionTagRe.FindString(rest)}
		case styleTagRe.MatchString(rest):
			t = markupToken{raw: styleTagRe.FindString(rest), style: true}
		default:
			i++
			continue
		}
		flush(i)
		add(t)
		i += len(t.raw)
		lit = i
	}
	flush(len(markup))
	return toks, plain.String()
}

// styleState follows the colors and attributes set by style tags, to restore
// them after a highlighted match.
type styleState struct{ fg, bg, attr string }

func (s *styleState) apply(tag string) {
	parts := strings.SplitN(tag[1:len(tag)-1], ":", 4)
	for i, p := range parts {
		if p == "" {
			continue
		}
		switch i {
		case 0:
			s.fg = p
		case 1:
			s.bg = p
		case 2:
			s.attr = p
		}
	}
}

func (s styleState) tag() string {
	or := func(v string) string {
		if v == "" {
			return "-"
		}
		return v
	}
	return "[" + or(s.fg) + ":" + or(s.bg) + ":" + or(s.attr) + "]"
}

// HighlightMatches marks the matches of re in the text shown by markup with
// regions "m0", "m1", … colored black on yellow, keeping the colors of the
// rest. It returns the new markup and the number of regions.
func HighlightMatches(markup string, re *regexp.Regexp) (string, int) {
	toks, plain := tokenizeMarkup(markup)
	var matches [][]int
	for _, m := range re.FindAllStringIndex(plain, -1) {
		if m[0] < m[1] {
			matches = append(matches, m)
		}
	}
	if len(matches) == 0 {
		return markup, 0
	}
	var b strings.Builder
	var st styleState
	next, regions, in := 0, 0, false
	open := func() {
		fmt.Fprintf(&b, `["m%d"][black:yellow]`, regions)
		in = true
	}
	closeMatch := func() {
		b.WriteString(`[""]` + st.tag())
		in = false
		regions++
	}
	for _, t := range toks {
		end := t.start + len(t.text)
		switch {
		case t.style:
			st.apply(t.raw)
			if !in {
				b.WriteString(t.raw)
			}
		case t.text == "": // a region tag
			b.WriteString(t.raw)
		case t.atomic:
			if !in && next < len(matches) && matches[next][0] < end {
				open()
			}
			b.WriteString(t.raw)
			if in && matches[next][1] <= end {
				closeMatch()
				// matches that end inside this token merged into the closed one
				for next++; next < len(matches) && matches[next][0] < end; next++ {
				}
			}
		default:
			for p := t.start; p < end; {
				if !in {
					if next >= len(matches) || matches[next][0] >= end {
						b.WriteString(t.raw[p-t.start:])
						break
					}
					s := max(matches[next][0], p)
					b.WriteString(t.raw[p-t.start : s-t.start])
					p = s
					open()
				}
				e := min(matches[next][1], end)
				b.WriteString(t.raw[p-t.start : e-t.start])
				p = e
				if e == matches[next][1] {
					closeMatch()
					next++
				}
			}
		}
	}
	if in {
		closeMatch()
	}
	return b.String(), regions
}
