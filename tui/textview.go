package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/tui/theme"
)

// TextViewer shows text (command output, JSON, markup) with vim keys,
// search, wrap and save.
type TextViewer struct {
	*tview.Flex
	app     *App
	name    string
	text    *tview.TextView
	input   *tview.InputField
	content string
	syntax  string // a registered syntax: plain, markup, json, or one of the tool's
	search  *regexp.Regexp
	matches int
	current int
	wrap    bool

	// Extra are hints of the view's own keys, shown after the common ones.
	Extra []Hint
	// Keys and CtrlKeys are the view's own key actions.
	Keys     map[rune]func()
	CtrlKeys map[tcell.Key]func()
	// Loader (re)loads the content; Refresh and the auto-refresh call it.
	Loader func()
}

// NewTextViewer builds an empty viewer titled name.
func NewTextViewer(app *App, name, syntax string) *TextViewer {
	v := &TextViewer{app: app, name: name, syntax: syntax, Keys: map[rune]func(){}, CtrlKeys: map[tcell.Key]func(){}}
	v.text = tview.NewTextView().SetDynamicColors(true).SetRegions(true).SetWrap(false)
	v.text.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	v.text.SetTitle(" " + name + " ")
	v.input = tview.NewInputField().SetLabel("/")
	v.input.SetDoneFunc(func(k tcell.Key) {
		if k == tcell.KeyEnter {
			v.setSearch(v.input.GetText())
		}
		v.Flex.ResizeItem(v.input, 0, 0)
		app.SetFocus(v.text)
	})
	v.Flex = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.text, 0, 1, true).
		AddItem(v.input, 0, 0, false)
	v.text.SetInputCapture(v.capture)
	return v
}

// Draw applies the current theme.
func (v *TextViewer) Draw(screen tcell.Screen) {
	v.text.SetBackgroundColor(theme.Bg).SetBorderColor(theme.Border).SetTitleColor(theme.Title)
	v.text.SetTextColor(theme.Fg)
	v.input.SetBackgroundColor(theme.Bg)
	v.input.SetFieldBackgroundColor(theme.Bg).SetLabelColor(theme.OK)
	v.Flex.SetBackgroundColor(theme.Bg)
	v.Flex.Draw(screen)
}

// Name is the title, shown in the breadcrumbs.
func (v *TextViewer) Name() string { return v.name }

// Table is nil: a text view has no filterable table.
func (v *TextViewer) Table() *TableView { return nil }

// Close does nothing.
func (v *TextViewer) Close() {}

// Text is the underlying tview text view.
func (v *TextViewer) Text() *tview.TextView { return v.text }

// Content is the text as set, before highlighting.
func (v *TextViewer) Content() string { return v.content }

// Refresh reruns the loader, if any.
func (v *TextViewer) Refresh() {
	if v.Loader != nil {
		v.Loader()
	}
}

// Rerender re-applies colors after a theme switch.
func (v *TextViewer) Rerender() { v.render() }

// Hints are the common text keys plus the view's own.
func (v *TextViewer) Hints() []Hint {
	h := []Hint{{Key: "/", Desc: N("search")}, {Key: "n/N", Desc: N("next/prev")}, {Key: "w", Desc: N("wrap")}, {Key: "h/l", Desc: N("sideways")},
		{Key: "s", Desc: N("save")}, {Key: "ctrl-d/u", Desc: N("½ page")}}
	return append(h, v.Extra...)
}

// SetWrap turns line wrapping (at word boundaries) on or off, as w does.
func (v *TextViewer) SetWrap(on bool) {
	v.wrap = on
	v.text.SetWrap(on).SetWordWrap(on)
}

// Focus forwards focus to the text area.
func (v *TextViewer) Focus(delegate func(p tview.Primitive)) { delegate(v.text) }

// SetLoading shows a placeholder.
func (v *TextViewer) SetLoading() { v.text.SetText(theme.Tag(theme.Dim) + T("loading…")) }

// SetContent replaces the text, keeping the scroll position.
func (v *TextViewer) SetContent(content, syntax string) {
	first := v.content == ""
	v.content = strings.ReplaceAll(content, "\r\n", "\n")
	if syntax != "" {
		v.syntax = syntax
	}
	row, col := v.text.GetScrollOffset()
	v.render()
	if first {
		v.text.ScrollToBeginning()
	} else {
		v.text.ScrollTo(row, col)
	}
}

// SetError shows an error instead of content.
func (v *TextViewer) SetError(err error) {
	v.text.SetText(theme.Tag(theme.Err) + tview.Escape(err.Error()))
}

func (v *TextViewer) capture(ev *tcell.EventKey) *tcell.EventKey {
	_, _, _, h := v.text.GetInnerRect()
	row, col := v.text.GetScrollOffset()
	switch ev.Key() {
	case tcell.KeyCtrlD:
		v.text.ScrollTo(row+h/2, col)
		return nil
	case tcell.KeyCtrlU:
		v.text.ScrollTo(max(0, row-h/2), col)
		return nil
	case tcell.KeyCtrlE:
		v.text.ScrollTo(row+1, col)
		return nil
	case tcell.KeyCtrlY:
		v.text.ScrollTo(max(0, row-1), col)
		return nil
	case tcell.KeyRune:
		if f, ok := v.Keys[ev.Rune()]; ok {
			f()
			return nil
		}
		switch ev.Rune() {
		case '/':
			v.input.SetText("")
			v.Flex.ResizeItem(v.input, 1, 0)
			v.app.SetFocus(v.input)
			return nil
		case 'n', 'N':
			if v.search == nil {
				v.app.Flash().Warnf("no search: press / to search")
				return nil
			}
			if ev.Rune() == 'n' {
				v.jump(1)
			} else {
				v.jump(-1)
			}
			return nil
		case 'w':
			v.SetWrap(!v.wrap)
			if v.wrap {
				v.app.Flash().Infof("wrap on")
			} else {
				v.app.Flash().Infof("wrap off: h/l scroll sideways")
			}
			return nil
		case 's':
			v.save()
			return nil
		}
	default:
		if f, ok := v.CtrlKeys[ev.Key()]; ok {
			f()
			return nil
		}
	}
	return ev
}

func (v *TextViewer) setSearch(pattern string) {
	v.search = nil
	if pattern != "" {
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(pattern))
		}
		v.search = re
	}
	v.current = 0
	v.render()
	if v.search != nil {
		if v.matches == 0 {
			v.app.Flash().Warnf("pattern not found: %s", pattern)
			return
		}
		v.jump(0)
	}
}

func (v *TextViewer) jump(delta int) {
	if v.matches == 0 {
		return
	}
	v.current = ((v.current+delta)%v.matches + v.matches) % v.matches
	v.text.Highlight(fmt.Sprintf("m%d", v.current)).ScrollToHighlight()
	v.app.Flash().Infof("match %d/%d", v.current+1, v.matches)
}

func (v *TextViewer) render() {
	markup := tview.Escape(v.content)
	if f, ok := syntaxes[v.syntax]; ok {
		markup = f(v.content)
	}
	v.matches = 0
	if v.search != nil {
		markup, v.matches = HighlightMatches(markup, v.search)
	}
	v.text.SetText(markup)
}

func (v *TextViewer) save() {
	ext := ".txt"
	if v.syntax == "json" {
		ext = ".json"
	}
	content := v.content
	if v.syntax == "markup" {
		content = StripTags(content)
	}
	path, err := v.app.SaveFile(v.name, ext, content)
	if err != nil {
		v.app.Flash().Err(err)
		return
	}
	v.app.Flash().Infof("saved %s", path)
}

// syntaxes highlight content of a syntax into tview markup.
var syntaxes = map[string]func(string) string{
	"markup": func(s string) string { return s },
	"json":   ColorizeJSON,
}

// RegisterSyntax adds a highlighter for SetContent(…, name). Tools register
// theirs (call details, statistics) from init.
func RegisterSyntax(name string, f func(content string) string) { syntaxes[name] = f }

var jsonKeyRe = regexp.MustCompile(`^(\s*)("(?:[^"\\]|\\.)*")(\s*:\s*)(.*)$`)

// ColorizeJSON highlights pretty-printed JSON.
func ColorizeJSON(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if m := jsonKeyRe.FindStringSubmatch(line); m != nil {
			fmt.Fprintf(&b, "%s%s%s[-]%s%s\n", m[1], theme.Tag(theme.JSONKey), tview.Escape(m[2]), m[3], colorizeJSONValue(m[4]))
			continue
		}
		b.WriteString(colorizeJSONValue(line) + "\n")
	}
	return b.String()
}

func colorizeJSONValue(v string) string {
	t := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), ","))
	switch {
	case strings.HasPrefix(t, `"`):
		return theme.Tag(theme.JSONString) + tview.Escape(v) + "[-]"
	case t == "true" || t == "false" || t == "null":
		return theme.Tag(theme.InfoKey) + tview.Escape(v) + "[-]"
	case t != "" && (t[0] >= '0' && t[0] <= '9' || t[0] == '-'):
		return theme.Tag(theme.OK) + tview.Escape(v) + "[-]"
	}
	return tview.Escape(v)
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._@-]+`)

// SafeFileName turns s into a file name: unusual characters become "_".
func SafeFileName(s string) string { return sanitizeFileName(s) }

func sanitizeFileName(s string) string {
	s = unsafeFileChars.ReplaceAllString(s, "_")
	if len(s) > 60 {
		s = s[:60]
	}
	return strings.Trim(s, "_")
}

var tagRe = regexp.MustCompile(`\[[^\[\]]*\]`)

// StripTags removes tview color and region tags.
func StripTags(s string) string { return tagRe.ReplaceAllString(s, "") }
