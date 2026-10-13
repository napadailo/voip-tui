package ui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/tui/theme"
)

// newTemplateView explains a signalling template token by token, with the
// descriptions of docs/ng_control_protocol.md.
func newTemplateView(app *App, s rtpengine.Setting) *TextViewer {
	v := newTextViewer(app, "template "+s.Key, "markup")
	v.Loader = func() { v.SetContent(templateText(s, app.Client().Info().Version), "markup") }
	v.Loader()
	return v
}

func templateText(s rtpengine.Setting, ver rtpengine.Version) string {
	// parsed again rather than taken from s.Template: the notes are in the
	// language of the moment
	parsed := rtpengine.ParseTemplate(s.Key, s.Value, ver)
	t := &parsed
	st, note := rtpengine.TemplateFindings(parsed, ver)
	var b strings.Builder
	esc := tview.Escape
	keyW := 0
	for _, k := range []string{T("Section"), T("Applied"), T("Status"), T("Transport"), T("Host runs")} {
		keyW = max(keyW, strWidth(k))
	}
	kv := func(k, val string) {
		fmt.Fprintf(&b, "%s%s[-] %s\n", theme.Tag(theme.InfoKey), pad(k+":", keyW+1), val)
	}
	fmt.Fprintf(&b, "%s[::b]%s[::-][-]\n", theme.Tag(theme.Title), esc(Tf("Template %s", s.Key)))
	kv(T("Section"), esc(fmt.Sprintf("[%s] · %s:%d", s.Section, s.File, s.Line)))
	use := esc(rtpengine.TemplateUse(s.Key))
	if rtpengine.TemplateNamed(s.Key) {
		use += dim(Tf(`, e.g. rtpengine_offer("template=%s")`, s.Key))
	} else {
		use = theme.Tag(theme.Warn) + use + "[-]" + dim(T(", without template= in the proxy script"))
	}
	kv(T("Applied"), use)
	status := theme.Tag(theme.OK) + "ok[-]"
	if st != rtpengine.StatusOK {
		status = theme.Tag(theme.Err) + esc(note) + "[-]"
	}
	kv(T("Status"), status)
	if t.Transport != "" {
		kv(T("Transport"), esc(t.Transport)+dim(" "+T("(transport-protocol from the RTP/SRTP/AVPF/DTLS shortcuts)")))
	}
	kv(T("Host runs"), esc(ver.String()))

	fmt.Fprintf(&b, "\n%s[::b]%s[::-][-]%s\n", theme.Tag(theme.Title), T("Flags"), dim(" · "+Tf("%d, in the order rtpengine applies them", len(t.Items))))
	for _, it := range t.Items {
		b.WriteString("\n")
		tone := theme.Accent
		if it.Status == rtpengine.StatusUnknown || it.Status == rtpengine.StatusNotInVersion {
			tone = theme.Err
		}
		fmt.Fprintf(&b, "  %s[::b]%s[::-][-]", theme.Tag(tone), esc(it.Token))
		if it.Note != "" {
			fmt.Fprintf(&b, "  %s%s[-]", theme.Tag(tone), esc(it.Note))
		}
		b.WriteString("\n")
		label := T(it.Kind)
		switch {
		case strings.HasPrefix(it.Name, it.Kind): // "codec strip", "replace"
			label = it.Name
		case it.Name != "" && it.Name != it.Token:
			label += " " + it.Name
		}
		meaning := it.Meaning
		if meaning == "" && it.Status == rtpengine.StatusOK {
			meaning = T("known to rtpengine, not described in the NG protocol docs")
		}
		if meaning != "" {
			b.WriteString(wrapped("    ", dim(label+":"), strWidth(label)+1, meaning, "      "))
		}
		for _, val := range it.Values {
			vt := theme.InfoValue
			text := val.Meaning
			if val.Note != "" {
				vt, text = theme.Err, val.Note
			}
			if text == "" {
				text = T("known to rtpengine, not described in the NG protocol docs")
			}
			head := theme.Tag(vt) + esc(val.Value) + "[-] " + dim("→")
			b.WriteString(wrapped("      ", head, strWidth(val.Value)+2, text, "        "))
		}
	}

	fmt.Fprintf(&b, "\n%s[::b]%s[::-][-]\n  %s\n", theme.Tag(theme.Title), T("As written"), esc(s.Value))
	return b.String()
}

// templateWrap is the width descriptions are wrapped at; the docs are prose
// and read badly as one long line.
const templateWrap = 110

// wrapped writes indent+head followed by text, wrapping text at templateWrap
// columns with cont as the indent of continuation lines. headLen is the
// visible width of head (it may contain color tags).
func wrapped(indent, head string, headLen int, text, cont string) string {
	var b strings.Builder
	b.WriteString(indent + head)
	col := len(indent) + headLen
	for _, w := range strings.Fields(text) {
		// a run of CJK text has no spaces: it may break after any character
		pieces := []string{w}
		if strWidth(w) > len([]rune(w)) {
			pieces = strings.Split(w, "")
		}
		for i, p := range pieces {
			pl := strWidth(p)
			sep := i == 0 // a space only between words
			need := pl
			if sep {
				need++
			}
			switch {
			case col+need > templateWrap && col > len(cont):
				b.WriteString("\n" + cont)
				col = len(cont)
			case sep:
				b.WriteString(" ")
				col++
			}
			b.WriteString(tview.Escape(p))
			col += pl
		}
	}
	b.WriteString("\n")
	return b.String()
}
