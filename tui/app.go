// Package tui is the terminal UI framework of the voip-tui tools: a header with info,
// key hints and a logo, a stack of views (tables and text) with
// breadcrumbs, a ":" command prompt with completion and history, a "/"
// filter, a status line, modal dialogs, themes, translations and
// auto-refresh. A tool registers its commands and views and fills the
// header through the hooks of App.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/tui/theme"
)

// Hint is a key binding shown in the header.
type Hint struct {
	Key  string
	Desc string // English, marked with N; translated when drawn
}

// View is a screen on the navigation stack.
type View interface {
	tview.Primitive
	// Name is shown in the breadcrumbs.
	Name() string
	Hints() []Hint
	// Table returns the filterable table of the view, or nil.
	Table() *TableView
	Refresh()
	Close()
}

// BackHandler views take esc and q first, to leave a pane of their own
// before the view is closed; Back reports whether it did.
type BackHandler interface{ Back() bool }

// CtrlCHandler views take ctrl-c first, for a key of their own, as a form
// clears with it; CtrlC reports whether it did. Elsewhere ctrl-c quits.
type CtrlCHandler interface{ CtrlC() bool }

// AutoRefresher views are refreshed on every tick while on top.
type AutoRefresher interface{ AutoRefresh() bool }

// Command is a ":" command.
type Command struct {
	Names    []string // the first is shown in the help
	Args     string   // usage of the arguments, e.g. "[name]"
	Help     string   // English, marked with N
	Complete []string // argument suggestions for the prompt
	Run      func(args string) error
	// Available hides the command from completion while it returns false
	// (e.g. the managed software is too old for it); nil = always.
	Available func() bool
}

// Options configure an App.
type Options struct {
	// Name of the tool, e.g. "rtpengine-tui".
	Name string
	// Description is the first line of the help (English, translated).
	Description string
	// Logo is drawn right of the header, at most 5 lines.
	Logo []string
	// Refresh is the auto-refresh interval, 0 turns it off.
	Refresh time.Duration
	// DownloadDir is where text views save files.
	DownloadDir string
	// Timeout of the contexts Async work gets, default 30s.
	Timeout time.Duration
}

// App is the application frame.
type App struct {
	*tview.Application
	opts Options

	// DownloadDir is where text views save files ("~" is expanded).
	DownloadDir string
	// Info returns the header lines as key (English, translated) and value
	// (tview markup).
	Info func() [][2]string
	// Completions returns extra prompt suggestions ("host x").
	Completions func() []string
	// Help adds the tool's sections to the help, before the commands.
	Help func(h *Help)
	// Unknown runs a command line no command matches; handled reports
	// whether it did anything (e.g. a host name switches the host).
	Unknown func(name, args string) (handled bool, err error)
	// OnTheme and OnLanguage are told about a switch, e.g. to store it in
	// the tool's config.
	OnTheme    func(name string)
	OnLanguage func(lang string)
	// OnTick runs on every auto-refresh tick, after the top view refreshed.
	OnTick func()

	root      *tview.Flex
	header    *tview.Flex
	info      *tview.TextView
	hintsView *tview.TextView
	logo      *tview.TextView
	prompt    *tview.InputField
	pages     *tview.Pages
	crumbs    *tview.TextView
	flash     *Flash

	promptMode  rune // ':' or '/'
	promptPrev  string
	history     []string
	stack       []View
	modalOpen   bool
	commands    []*Command
	lastFocused tview.Primitive

	tickMu  sync.Mutex
	refresh time.Duration
	stopTk  chan struct{}
}

// New creates the frame. Register commands and hooks, push nothing yet:
// Start runs the initial command.
func New(o Options) *App {
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	a := &App{Application: tview.NewApplication(), opts: o, DownloadDir: o.DownloadDir, refresh: o.Refresh}
	a.buildLayout()
	a.commands = a.builtinCommands()
	a.SetInputCapture(a.capture)
	return a
}

// Name is the tool name.
func (a *App) Name() string { return a.opts.Name }

// Flash returns the status line.
func (a *App) Flash() *Flash { return a.flash }

// Ctx returns a context for remote calls.
func (a *App) Ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), a.opts.Timeout)
}

func (a *App) buildLayout() {
	a.info = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	a.hintsView = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	a.logo = tview.NewTextView().SetDynamicColors(true).SetWrap(false).SetTextAlign(tview.AlignRight)
	logoW := 0
	for _, l := range a.opts.Logo {
		logoW = max(logoW, StrWidth(l))
	}
	a.header = tview.NewFlex().
		AddItem(a.info, 0, 1, false). // UpdateInfo sizes it to the text
		AddItem(a.hintsView, 0, 2, false).
		AddItem(a.logo, logoW+1, 0, false)

	a.prompt = tview.NewInputField()
	a.prompt.SetBorder(true)
	// Enter and Esc are handled here, before tview: its done callback runs while
	// holding the autocomplete lock, and closing the prompt takes that lock again.
	a.prompt.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEnter, tcell.KeyEsc:
			a.promptDone(ev.Key())
			return nil
		}
		return ev
	})
	a.prompt.SetChangedFunc(func(text string) {
		if a.promptMode == '/' {
			if t := a.TopTable(); t != nil {
				t.SetFilter(text)
			}
		}
	})

	a.pages = tview.NewPages()
	a.crumbs = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	a.flash = newFlash(a)

	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 6, 0, false).
		AddItem(a.prompt, 0, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.crumbs, 1, 0, false).
		AddItem(a.flash, 1, 0, false)
	a.SetRoot(a.root, true)
	a.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		a.restyle()
		return false
	})
}

// restyle applies the current theme to the frame widgets before each draw.
func (a *App) restyle() {
	for _, tv := range []*tview.TextView{a.info, a.hintsView, a.logo, a.crumbs, a.flash.TextView} {
		tv.SetBackgroundColor(theme.Bg)
		tv.SetTextColor(theme.Fg)
	}
	a.header.SetBackgroundColor(theme.Bg)
	a.root.SetBackgroundColor(theme.Bg)
	a.pages.SetBackgroundColor(theme.Bg)
	a.prompt.SetBackgroundColor(theme.Bg)
	a.prompt.SetFieldBackgroundColor(theme.Bg).SetFieldTextColor(theme.InfoValue)
	if a.promptMode == '/' {
		a.prompt.SetLabelColor(theme.OK).SetBorderColor(theme.OK)
	} else {
		a.prompt.SetLabelColor(theme.Warn).SetBorderColor(theme.Warn)
	}
	a.prompt.SetAutocompleteStyles(theme.SelectBg, tcell.StyleDefault.Foreground(theme.SelectFg).Background(theme.SelectBg),
		tcell.StyleDefault.Background(theme.Warn).Foreground(theme.Bg))
}

// SetTheme switches the theme and redraws everything.
func (a *App) SetTheme(name string) error {
	if err := theme.Apply(name); err != nil {
		return err
	}
	if a.OnTheme != nil {
		a.OnTheme(theme.Current())
	}
	a.redraw(false)
	return nil
}

// SetLanguage switches the interface language. Titles, headers and hints
// are translated when drawn; every open view is refreshed so that the text
// it built (rows, details, notes) is rebuilt in the new language.
func (a *App) SetLanguage(lang string) error {
	if err := i18n.Set(lang); err != nil {
		return err
	}
	if a.OnLanguage != nil {
		a.OnLanguage(i18n.Lang())
	}
	a.redraw(true)
	return nil
}

func (a *App) redraw(refresh bool) {
	a.drawLogo()
	a.UpdateInfo()
	a.updateHints()
	a.updateCrumbs()
	for _, v := range a.stack {
		if t := v.Table(); t != nil {
			t.render()
		}
		if r, ok := v.(interface{ Rerender() }); ok {
			r.Rerender()
		}
		if refresh {
			v.Refresh()
		}
	}
}

func (a *App) drawLogo() {
	a.logo.SetText(theme.Tag(theme.Logo) + tview.Escape(strings.Join(a.opts.Logo, "\n")))
}

// Start runs the initial command (on error the fallback) and the UI loop.
func (a *App) Start(initial, fallback string) error {
	a.drawLogo()
	a.UpdateInfo()
	if err := a.RunCommand(initial); err != nil {
		a.flash.Err(err)
		if fallback != "" {
			_ = a.RunCommand(fallback)
		}
	}
	a.SetRefresh(a.refresh)
	defer a.SetRefresh(0)
	return a.Run()
}

// SetRefresh changes the auto-refresh interval; 0 stops it.
func (a *App) SetRefresh(d time.Duration) {
	a.tickMu.Lock()
	defer a.tickMu.Unlock()
	if a.stopTk != nil {
		close(a.stopTk)
		a.stopTk = nil
	}
	a.refresh = d
	if d <= 0 {
		return
	}
	stop := make(chan struct{})
	a.stopTk = stop
	go func() {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.QueueUpdateDraw(func() {
					if a.modalOpen || a.prompt.HasFocus() {
						return
					}
					if r, ok := a.Top().(AutoRefresher); ok && r.AutoRefresh() {
						a.Top().Refresh()
					}
					if a.OnTick != nil {
						a.OnTick()
					}
				})
			}
		}
	}()
}

// RefreshInterval is the current auto-refresh interval.
func (a *App) RefreshInterval() time.Duration {
	a.tickMu.Lock()
	defer a.tickMu.Unlock()
	return a.refresh
}

// maxInfoWidth caps the header's info column on a narrow screen.
const maxInfoWidth = 60

// UpdateInfo redraws the header lines from the Info hook.
func (a *App) UpdateInfo() {
	if a.Info == nil {
		return
	}
	lines := a.Info()
	w := 7
	for _, l := range lines {
		w = max(w, StrWidth(T(l[0]))+1)
	}
	var b strings.Builder
	width := 0
	for _, l := range lines {
		line := fmt.Sprintf("%s%s[-] %s%s[-]", theme.Tag(theme.InfoKey), Pad(T(l[0])+":", w), theme.Tag(theme.InfoValue), l[1])
		width = max(width, StrWidth(StripTags(line)))
		b.WriteString(line + "\n")
	}
	a.info.SetText(b.String())
	// as wide as the text, so host and version are not cut; the hints take the rest
	a.header.ResizeItem(a.info, min(width+2, maxInfoWidth), 0)
}

func (a *App) updateHints() {
	var hints []Hint
	if v := a.Top(); v != nil {
		hints = append(hints, v.Hints()...)
	}
	if t := a.TopTable(); t != nil && t.Wide() {
		hints = append(hints, Hint{Key: "h/l", Desc: N("sideways")})
	}
	hints = append(hints, Hint{Key: ":", Desc: N("command")})
	if a.TopTable() != nil { // text views bind / to their own search
		hints = append(hints, Hint{Key: "/", Desc: N("filter")})
	}
	hints = append(hints, Hint{Key: "?", Desc: N("help")}, Hint{Key: "esc", Desc: N("back")})
	for i := range hints {
		hints[i].Desc = T(hints[i].Desc)
	}
	const rows = 5
	cols := (len(hints) + rows - 1) / rows
	width := 0
	for _, h := range hints {
		width = max(width, len(h.Key)+StrWidth(h.Desc)+4)
	}
	var b strings.Builder
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			i := c*rows + r
			if i >= len(hints) {
				continue
			}
			h := hints[i]
			key := "<" + h.Key + ">"
			gap := max(1, width-len(key)-StrWidth(h.Desc))
			fmt.Fprintf(&b, "%s%s[-] %s%s[-]%s", theme.Tag(theme.Key), tview.Escape(key), theme.Tag(theme.KeyDesc), tview.Escape(h.Desc), strings.Repeat(" ", gap))
		}
		b.WriteString("\n")
	}
	a.hintsView.SetText(b.String())
}

func (a *App) updateCrumbs() {
	var b strings.Builder
	for i, v := range a.stack {
		bg := theme.CrumbBg
		if i == len(a.stack)-1 {
			bg = theme.CrumbLast
		}
		fmt.Fprintf(&b, "[%s:%s:b] <%s> [-:-:-] ", theme.Name(theme.CrumbFg), theme.Name(bg), tview.Escape(v.Name()))
	}
	a.crumbs.SetText(b.String())
}

// RefreshHints redraws the key hints (views call it when their mode changes).
func (a *App) RefreshHints() { a.updateHints() }

// Top is the view on top of the stack.
func (a *App) Top() View {
	if len(a.stack) == 0 {
		return nil
	}
	return a.stack[len(a.stack)-1]
}

// TopTable is the table of the top view, or nil.
func (a *App) TopTable() *TableView {
	if v := a.Top(); v != nil {
		return v.Table()
	}
	return nil
}

// Stack returns the open views, bottom first.
func (a *App) Stack() []View { return a.stack }

// Push shows a view on top of the stack.
func (a *App) Push(v View) {
	name := fmt.Sprintf("view-%d", len(a.stack))
	a.stack = append(a.stack, v)
	a.pages.AddAndSwitchToPage(name, v, true)
	a.SetFocus(v)
	a.updateHints()
	a.updateCrumbs()
}

// Replace clears the stack and shows v (used by top-level commands).
func (a *App) Replace(v View) {
	for len(a.stack) > 0 {
		a.popSilently()
	}
	a.Push(v)
}

func (a *App) popSilently() {
	v := a.stack[len(a.stack)-1]
	v.Close()
	a.pages.RemovePage(fmt.Sprintf("view-%d", len(a.stack)-1))
	a.stack = a.stack[:len(a.stack)-1]
}

// Pop closes the top view; the last one stays.
func (a *App) Pop() {
	if len(a.stack) <= 1 {
		a.flash.Info(T("type :q to quit"))
		return
	}
	a.popSilently()
	a.pages.SwitchToPage(fmt.Sprintf("view-%d", len(a.stack)-1))
	a.SetFocus(a.Top())
	a.updateHints()
	a.updateCrumbs()
	a.UpdateInfo()
}

func (a *App) capture(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Key() == tcell.KeyCtrlC {
		if c, ok := a.Top().(CtrlCHandler); ok && !a.modalOpen && !a.prompt.HasFocus() && c.CtrlC() {
			return nil
		}
		a.Stop()
		return nil
	}
	if a.modalOpen || a.prompt.HasFocus() {
		return ev
	}
	switch a.GetFocus().(type) {
	case *tview.InputField, *tview.TextArea, *tview.DropDown, *tview.Button, *tview.Checkbox:
		return ev
	}
	switch ev.Key() {
	case tcell.KeyEsc:
		if t := a.TopTable(); t != nil && t.Filter() != "" {
			t.SetFilter("")
			return nil
		}
		a.back()
		return nil
	case tcell.KeyCtrlR:
		if v := a.Top(); v != nil {
			v.Refresh()
		}
		return nil
	case tcell.KeyRune:
		switch ev.Rune() {
		case ':':
			a.showPrompt(':')
			return nil
		case '/':
			if a.TopTable() != nil {
				a.showPrompt('/')
				return nil
			}
		case '?':
			a.Push(NewHelpView(a))
			return nil
		case 'q':
			a.back()
			return nil
		}
	}
	return ev
}

// back leaves a pane of the top view, else the view.
func (a *App) back() {
	if b, ok := a.Top().(BackHandler); ok && b.Back() {
		return
	}
	a.Pop()
}

// Prompt opens the command prompt with text typed in (e.g. "set maxsessions ").
func (a *App) Prompt(text string) {
	a.showPrompt(':')
	a.prompt.SetText(text)
}

func (a *App) showPrompt(mode rune) {
	a.promptMode = mode
	a.lastFocused = a.GetFocus()
	if mode == ':' {
		a.prompt.SetLabel(":")
		a.prompt.SetText("")
		a.prompt.SetAutocompleteFunc(a.autocomplete)
	} else {
		a.prompt.SetLabel("/")
		a.prompt.SetAutocompleteFunc(nil)
		if t := a.TopTable(); t != nil {
			a.promptPrev = t.Filter()
			a.prompt.SetText(t.Filter())
		}
	}
	a.root.ResizeItem(a.prompt, 3, 0)
	a.SetFocus(a.prompt)
}

func (a *App) hidePrompt() {
	a.prompt.SetAutocompleteFunc(nil)
	a.prompt.Autocomplete()
	a.root.ResizeItem(a.prompt, 0, 0)
	if a.lastFocused != nil {
		a.SetFocus(a.lastFocused)
	} else if v := a.Top(); v != nil {
		a.SetFocus(v)
	}
}

func (a *App) promptDone(key tcell.Key) {
	text := strings.TrimSpace(a.prompt.GetText())
	mode := a.promptMode
	switch key {
	case tcell.KeyEnter:
		a.hidePrompt()
		if mode == ':' && text != "" {
			a.history = append(a.history, text)
			if err := a.RunCommand(text); err != nil {
				a.flash.Err(err)
			}
		}
	case tcell.KeyEsc:
		a.hidePrompt()
		if mode == '/' {
			if t := a.TopTable(); t != nil {
				t.SetFilter(a.promptPrev)
			}
		}
	}
	a.promptMode = 0
}

func (a *App) autocomplete(current string) []string {
	if strings.TrimSpace(current) == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] && strings.HasPrefix(s, current) && s != current {
			seen[s] = true
			out = append(out, s)
		}
	}
	for i := len(a.history) - 1; i >= 0; i-- {
		add(a.history[i])
	}
	var names []string
	for _, c := range a.commands {
		if c.Available != nil && !c.Available() {
			continue
		}
		names = append(names, c.Names[0])
		for _, s := range c.Complete {
			names = append(names, c.Names[0]+" "+s)
		}
	}
	if a.Completions != nil {
		names = append(names, a.Completions()...)
	}
	sort.Strings(names)
	for _, n := range names {
		add(n)
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// AddCommands registers commands; they run before the built-in ones
// (theme, lang, help, quit) of the same name.
func (a *App) AddCommands(cmds ...*Command) {
	a.commands = append(cmds, a.commands...)
}

// Commands lists the registered commands, the tool's first.
func (a *App) Commands() []*Command { return a.commands }

// RunCommand executes a ":" command line.
func (a *App) RunCommand(line string) error {
	line = strings.TrimSpace(strings.TrimPrefix(line, ":"))
	name, args, _ := strings.Cut(line, " ")
	name = strings.ToLower(name)
	for _, c := range a.commands {
		for _, n := range c.Names {
			if n == name {
				return c.Run(args)
			}
		}
	}
	if a.Unknown != nil {
		if ok, err := a.Unknown(name, args); ok || err != nil {
			return err
		}
	}
	return fmt.Errorf(T("unknown command %q, see :help"), name)
}

func (a *App) builtinCommands() []*Command {
	return []*Command{
		{Names: []string{"theme", "themes", "skin"}, Args: "[name]", Help: N("switch theme, no name lists themes"), Complete: theme.Names(),
			Run: func(args string) error {
				if args = strings.TrimSpace(args); args == "" {
					a.Push(NewThemesView(a))
					return nil
				}
				if err := a.SetTheme(args); err != nil {
					return err
				}
				a.flash.Info(Tf("theme %s", args))
				return nil
			}},
		{Names: []string{"lang", "language"}, Args: "[" + strings.Join(i18n.Languages(), "|") + "]", Help: N("switch the interface language, no argument shows the current one"),
			Complete: i18n.Languages(), Run: func(args string) error {
				if args = strings.TrimSpace(args); args == "" {
					a.flash.Info(Tf("language: %s (available: %s)", i18n.Lang(), strings.Join(i18n.Languages(), ", ")))
					return nil
				}
				if err := a.SetLanguage(args); err != nil {
					return err
				}
				a.flash.Info(Tf("language %s (set language: %s in the config to keep it)", i18n.Lang(), i18n.Lang()))
				return nil
			}},
		{Names: []string{"help", "h"}, Help: N("key bindings and commands"), Run: func(string) error {
			a.Push(NewHelpView(a))
			return nil
		}},
		{Names: []string{"quit", "q", "q!", "exit"}, Help: N("quit the tool"), Run: func(string) error {
			a.Stop()
			return nil
		}},
	}
}

// Modal shows a centered dialog; done gets the label of the pressed button
// as passed (buttons are shown translated).
func (a *App) Modal(text string, buttons []string, done func(label string)) {
	shown := make([]string, len(buttons))
	for i, b := range buttons {
		shown[i] = T(b)
	}
	m := tview.NewModal().SetText(tview.Escape(text)).AddButtons(shown) // the text has data, no markup
	m.SetBorderColor(theme.Warn)
	m.SetBackgroundColor(theme.Bg)
	m.SetTextColor(theme.Fg)
	m.SetButtonStyle(theme.ButtonStyle())
	m.SetButtonActivatedStyle(theme.ButtonActivatedStyle())
	prev := a.GetFocus()
	a.modalOpen = true
	m.SetDoneFunc(func(i int, _ string) {
		a.pages.RemovePage("modal")
		a.modalOpen = false
		a.SetFocus(prev)
		label := ""
		if i >= 0 && i < len(buttons) {
			label = buttons[i]
		}
		if done != nil {
			done(label)
		}
	})
	m.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'h':
			return tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone)
		case 'l':
			return tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone)
		}
		return ev
	})
	a.pages.AddPage("modal", m, true, true)
	a.SetFocus(m)
}

// Confirm asks yes/no with Cancel focused; f runs on yes.
func (a *App) Confirm(text, yes string, f func()) {
	a.Modal(text, []string{N("Cancel"), yes}, func(label string) {
		if label == yes {
			f()
			return
		}
		a.Flash().Warnf("cancelled")
	})
}

// ModalOpen reports whether a dialog is shown.
func (a *App) ModalOpen() bool { return a.modalOpen }

// Async runs work in the background and applies the result on the UI goroutine.
func (a *App) Async(work func(ctx context.Context) func()) {
	go func() {
		ctx, cancel := a.Ctx()
		defer cancel()
		if apply := work(ctx); apply != nil {
			a.QueueUpdateDraw(apply)
		}
	}()
}

// Flash is the bottom status line.
type Flash struct {
	*tview.TextView
	app   *App
	mu    sync.Mutex
	timer *time.Timer
}

func newFlash(app *App) *Flash {
	return &Flash{TextView: tview.NewTextView().SetDynamicColors(true).SetWrap(false), app: app}
}

func (f *Flash) set(color tcell.Color, icon, msg string) {
	f.SetText(fmt.Sprintf("%s%s %s", theme.Tag(color), icon, tview.Escape(msg)))
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.timer != nil {
		f.timer.Stop()
	}
	f.timer = time.AfterFunc(6*time.Second, func() {
		f.app.QueueUpdateDraw(func() { f.SetText("") })
	})
}

// Info shows an informational message.
func (f *Flash) Info(msg string) { f.set(theme.OK, "😎", msg) }

// Infof formats an informational message, translating the format.
func (f *Flash) Infof(format string, args ...any) { f.Info(Tf(format, args...)) }

// Warn shows a warning.
func (f *Flash) Warn(msg string) { f.set(theme.Warn, "😗", msg) }

// Warnf formats a warning, translating the format.
func (f *Flash) Warnf(format string, args ...any) { f.Warn(Tf(format, args...)) }

// Err shows an error.
func (f *Flash) Err(err error) {
	if err != nil {
		f.set(theme.Err, "😡", err.Error())
	}
}

// Errf formats an error, translating the format.
func (f *Flash) Errf(format string, args ...any) { f.Err(errors.New(Tf(format, args...))) }

// Trunc shortens s to n characters with an ellipsis.
func Trunc(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// ShortPath replaces the home directory with "~".
func ShortPath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, h) {
		return "~" + p[len(h):]
	}
	return p
}

// SaveFile writes content to DownloadDir as <name>-<time><ext> (name made
// safe for a file name) and returns the path.
func (a *App) SaveFile(name, ext, content string) (string, error) {
	path := filepath.Join(expandHome(a.DownloadDir), sanitizeFileName(name)+"-"+time.Now().Format("20060102-150405")+ext)
	return WriteNew(path, []byte(content))
}

// WriteNew writes data to a new file only its owner reads, as what is
// saved holds calls and numbers: at path, or with -1, -2… before the
// extension when a file is there; it never follows a link nor overwrites.
// It returns the path written.
func WriteNew(path string, data []byte) (string, error) {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for n := 0; ; n++ {
		p := path
		if n > 0 {
			p = fmt.Sprintf("%s-%d%s", base, n, ext)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) && n < 100 {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return "", err
		}
		return p, f.Close()
	}
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}
