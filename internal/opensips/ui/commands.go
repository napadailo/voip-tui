package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/tui"
)

// MI commands that only read: :mi runs them without confirmation.
var safeCommands = map[string]bool{"version": true, "uptime": true, "which": true, "ps": true, "get_statistics": true,
	"list_statistics": true, "ul_show_contact": true, "dlg_list": true, "dlg_list_ctx": true, "profile_get_size": true,
	"list_all_profiles": true}

// IsSafeMI reports read-only MI calls.
func IsSafeMI(cmd string, args []string) bool {
	if cmd == "log_level" {
		return len(args) == 0
	}
	if safeCommands[cmd] {
		return true
	}
	if len(args) > 0 {
		return false
	}
	for _, suf := range []string{"_list", "_show", "_dump", "_status", "_list_shtags", "_list_topology"} {
		if strings.HasSuffix(cmd, suf) {
			return true
		}
	}
	return false
}

func (a *App) registerCommands() {
	cmds := []*tui.Command{
		{Names: []string{"hosts", "host", "nodes", "node", "ctx"}, Args: "[name]", Help: N("list OpenSIPS hosts or switch to one (keeps the current view)"), Run: func(args string) error {
			if args = strings.TrimSpace(args); args != "" {
				return a.SwitchHost(args)
			}
			a.Replace(newHostsView(a))
			return nil
		}},
		{Names: []string{"mi"}, Args: "<command> [k=v|arg …]", Help: N("run any MI command; state-changing ones ask first"), Run: func(args string) error {
			words, err := splitArgs(args)
			if err != nil {
				return err
			}
			if len(words) == 0 {
				return a.openResource(opensips.FindResource("commands"))
			}
			if a.Client() == nil {
				return errors.New(T("no host selected, see :hosts"))
			}
			a.RunMI(a.Client(), words[0], words[1:])
			return nil
		}},
		{Names: []string{"info", "describe", "overview", "loglevel", "loglevels"}, Help: N("version, process, MI access, log levels and MI commands of the host"), Run: func(string) error {
			return a.openView("info", false)
		}},
		{Names: []string{"stats", "stat", "statistics", "st"}, Help: N("statistics by category, rates between refreshes; a: all of them"), Run: func(string) error {
			return a.openView("stats", false)
		}},
		{Names: []string{"refresh"}, Args: "<seconds>", Help: N("auto-refresh period"), Run: func(args string) error {
			n, err := strconv.Atoi(strings.TrimSpace(args))
			if err != nil || n < 1 {
				return errors.New(T("usage: refresh <seconds>"))
			}
			a.SetRefresh(time.Duration(n) * time.Second)
			a.UpdateInfo()
			return nil
		}},
	}
	for _, r := range opensips.Resources {
		r := r
		cmds = append(cmds, &tui.Command{
			Names: append([]string{r.Name}, r.Aliases...),
			Help:  r.Help,
			Run:   func(string) error { return a.openResource(r) },
			// resources the host's OpenSIPS lacks are left out of completion
			Available: func() bool {
				cl := a.Client()
				return cl == nil || cl.Info() == nil || cl.Supports(r.Op).OK
			},
		})
	}
	a.AddCommands(cmds...)
}

func (a *App) openResource(r *opensips.Resource) error {
	return a.openView(r.Name, false)
}

// openView opens a resource view or the host card ("info") of the current
// host; push keeps the view below for esc, otherwise it is replaced. A
// view the host cannot show falls back to the host card with the reason.
func (a *App) openView(name string, push bool) error {
	c := a.Client()
	if c == nil {
		return errors.New(T("no host selected, see :hosts"))
	}
	show := a.Replace
	if push {
		show = a.Push
	}
	if name == "stats" {
		if s := c.Supports("stats.get"); c.Info() != nil && !s.OK {
			return fmt.Errorf("%s: %s", name, s.Reason)
		}
		show(newStatsPanel(a))
		return nil
	}
	r := opensips.FindResource(name)
	if r != nil && c.Info() != nil {
		if s := c.Supports(r.Op); !s.OK {
			if name == a.cfg.DefaultView || push {
				a.Flash().Warnf("%s: %s, showing info", r.Name, s.Reason)
				r = nil
			} else {
				return fmt.Errorf("%s: %s", r.Name, s.Reason)
			}
		}
	}
	if r == nil {
		show(newDescribeView(a))
		return nil
	}
	show(newResourceView(a, r, c))
	return nil
}

// currentView names the view on top, for keeping it across a host switch.
func (a *App) currentView() string {
	switch v := a.Top().(type) {
	case *ResourceView:
		return v.res.Name
	case *TextViewer:
		if strings.HasPrefix(v.Name(), describePrefix) {
			return "info"
		}
	case *autoText:
		if strings.HasPrefix(v.Name(), statsPrefix) {
			return "stats"
		}
	}
	return a.cfg.DefaultView
}

// SwitchHost makes another host current and keeps the view if the host supports it.
func (a *App) SwitchHost(name string) error {
	host, ok := a.cfg.Find(name)
	if !ok {
		return fmt.Errorf(T("host %q not found in %s"), name, a.cfg.Path())
	}
	view := a.currentView()
	a.host = host
	a.cfg.CurrentHost = name
	a.UpdateInfo()
	if r := opensips.FindResource(view); r != nil && a.Client().Info() != nil && !a.Client().Supports(r.Op).OK {
		a.Flash().Warnf("%s: %s, showing info", r.Name, a.Client().Supports(r.Op).Reason)
		view = "info"
	}
	return a.openView(view, false)
}

// RunMI runs a raw MI command and shows the answer; state-changing commands ask first.
func (a *App) RunMI(c *opensips.Client, cmd string, args []string) {
	h := c.Info()
	if h == nil {
		a.Flash().Warnf("%s: not probed yet", c.Host.Name)
		return
	}
	if h.Backend == "" {
		a.Flash().Errf("%s: %s", c.Host.Name, h.Error)
		return
	}
	if h.Commands != nil && !h.Commands[cmd] {
		a.Flash().Errf("%s: no MI command %s", c.Host.Name, cmd)
		return
	}
	var params opensips.Params
	if len(args) > 0 {
		params = args
	}
	rendered := c.Render(cmd, params)
	run := func() {
		tv := newTextViewer(a, cmd+"("+c.Host.Name+")", "json")
		tv.SetLoading()
		a.Push(tv)
		a.Async(func(ctx context.Context) func() {
			ans, err := c.MI(ctx, cmd, params)
			return func() {
				if err != nil {
					tv.SetContent(rendered+"\n\n"+Tf("error: %v", err), "plain")
					a.Flash().Err(err)
					return
				}
				syntax := "json"
				if _, ok := ans.(string); ok {
					syntax = "plain"
				}
				tv.SetContent(toJSON(ans), syntax)
			}
		})
	}
	switch {
	case IsSafeMI(cmd, args):
		run()
	case c.ReadOnly:
		a.Flash().Warnf("%s: read-only mode runs only reading MI commands", cmd)
	default:
		a.Confirm(Tf("This MI command may change %s (OpenSIPS %s):\n\n%s", c.Host.Name, h.Version(), rendered), T("Run"), run)
	}
}

// splitArgs splits a command line respecting quotes.
func splitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	in := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, errors.New(T("unterminated quote"))
	}
	if in {
		out = append(out, cur.String())
	}
	return out, nil
}

// helpSections are the opensips-tui sections of the ? help.
func (a *App) helpSections(h *tui.Help) {
	h.Section(T("Resource views"))
	h.Key("d / enter", T("card of the row: dialog, contact, destination, gateway, host"))
	h.Key("y", T("row as JSON"))
	h.Key("r", T("refresh"))
	h.Key("s", T("ssh to the host (the UI is suspended)"))
	h.Key("i", T("interactive opensips-cli on the host"))
	for _, r := range opensips.Resources {
		for _, act := range r.Actions {
			h.Key(act.Key+" ("+r.Name+")", T(act.Label)+" - "+act.Op)
		}
	}
	h.Key("enter (commands)", T("run the MI command without parameters"))
	h.Section(T("Hosts view"))
	h.Key("enter", T("dialogs of the host"))
	h.Key("space", T("make the host current (marked *)"))
	h.Key("d", T("host card: version, MI access, log levels, MI commands"))
	h.Key("r", T("probe the host again"))
	h.Key(":<host>", T("switch to the host, as :host <name>"))
	h.Section(T("Log levels (e in :info)"))
	h.Key("+ / -", T("one step up or down, at once"))
	h.Key("enter", T("type a level for log_level or xlog_level"))
	if c := a.Client(); c != nil && c.Info() != nil {
		info := c.Info()
		h.Section(Tf("On %s (OpenSIPS %s)", c.Host.Name, info.Version()))
		for _, n := range opensips.ProfileNotes(info.Version()) {
			h.Line("  " + n)
		}
		h.Line("")
		for _, r := range opensips.Resources {
			state := T("available")
			if s := c.Supports(r.Op); !s.OK {
				state = s.Reason
			}
			h.Line(fmt.Sprintf("  %-12s %s", r.Name, state))
		}
	}
}
