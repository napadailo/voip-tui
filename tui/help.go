package tui

import (
	"fmt"
	"strings"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/tui/theme"
)

// Help builds the text of the help view. Section titles and descriptions
// are translated by the caller (T).
type Help struct{ b strings.Builder }

// Section starts a titled section.
func (h *Help) Section(title string) {
	fmt.Fprintf(&h.b, "\n%s\n%s\n", title, strings.Repeat("─", StrWidth(title)))
}

// Key adds a key binding line.
func (h *Help) Key(key, desc string) { fmt.Fprintf(&h.b, "  %-18s %s\n", key, desc) }

// Line adds free text.
func (h *Help) Line(text string) { h.b.WriteString(text + "\n") }

// NewHelpView shows the keys and commands: navigation, the tool's sections
// (App.Help), commands, themes and languages.
func NewHelpView(a *App) *TextViewer {
	v := NewTextViewer(a, "help", "plain")
	v.Loader = func() { v.SetContent(a.helpText(), "plain") }
	v.Loader()
	return v
}

func (a *App) helpText() string {
	h := &Help{}
	if a.opts.Description != "" {
		h.Line(T(a.opts.Description))
	}
	h.Section(T("Navigation (vim)"))
	h.Key("j / k", T("down / up"))
	h.Key("g / G", T("first / last row"))
	h.Key("ctrl-d / ctrl-u", T("half page down / up"))
	h.Key("ctrl-f / ctrl-b", T("page down / up"))
	h.Key("H / M / L", T("top / middle / bottom of the screen"))
	h.Key("h / l", T("scroll columns left / right"))
	h.Key("enter", T("drill down"))
	h.Key("esc / q", T("back (esc first clears an active filter)"))
	h.Key(":", T("command prompt (tab/arrows complete, history included)"))
	h.Key("/", T("filter rows (regex, '!' prefix inverts) · in text: search, n/N next/prev"))
	h.Key("space", T("mark row"))
	h.Key("o / O", T("cycle sort column / reverse order"))
	h.Key("ctrl-r", T("refresh current view"))
	h.Key("?", T("this help"))
	h.Key(":q, ctrl-c", T("quit"))
	if a.Help != nil {
		a.Help(h)
	}
	h.Section(T("Commands"))
	for _, c := range a.commands {
		fmt.Fprintf(&h.b, "  :%-34s %s\n", strings.Join(c.Names, ", ")+" "+c.Args, T(c.Help))
	}
	h.Section(T("Themes"))
	h.Line("  " + strings.Join(theme.Names(), ", "))
	h.Section(T("Language"))
	h.Line("  " + Tf(":lang %s, or language: in the config", strings.Join(i18n.Languages(), "|")))
	return h.b.String()
}
