package ui

import (
	"context"
	"strconv"
	"strings"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// newDescribeView shows what probing found out about the current host.
func newDescribeView(app *App) View {
	v := newTextViewer(app, "describe "+app.host.Name, "markup")
	v.SetLoading()
	var levels []rtpengine.LogLevel
	var levelsErr error
	render := func() {
		c := app.Client()
		i := c.Info()
		h := app.host
		card := tui.NewCard(h.Name).Sub(c.Target(), Tf("via %s", h.Via)+" ("+i.CLI+")", labels(h))

		card.Section(N("rtpengine"))
		card.KV(N("version"), tui.Strong(i.Version.String())+tui.Faint(" · "+Tf("source: %s", i.Version.Source)))
		if i.VersionErr != nil && i.Version.Floor {
			card.Line(tui.Paint(theme.Warn, T("estimated from the CLI command set; set version: in the config to pin it")))
		}
		if i.CLIErr != nil {
			card.KV(N("CLI error"), tui.Paint(theme.Err, i.CLIErr.Error()))
		}
		process := func(pid, cmdline string) string {
			if pid == "" {
				return tui.Faint("-")
			}
			return tui.Strong(pid) + tui.Faint("  "+cmdline)
		}
		card.KV(N("process"), process(i.PID, i.CmdLine))
		card.KV(N("recording"), process(i.RecPID, i.RecCmdLine))
		card.KV(N("kernel"), orFaintDash(strings.Join(i.Kernel, ", ")))
		card.KV(N("packages"), orFaintDash(strings.Join(i.Packages, "; ")))
		card.KV(N("config"), tview.Escape(orDash(i.ConfigPath))+tui.Faint(" ["+i.Section+"]"))
		card.KV(N("recording config"), tview.Escape(orDash(i.RecConfPath))+tui.Faint(" ["+i.RecSection+"]"))
		if i.ConfErr != nil {
			card.KV(N("config error"), tui.Paint(theme.Err, i.ConfErr.Error()))
		}
		card.KV(N("probed"), tui.Faint(i.ProbedAt.Format("15:04:05")+" · "+Tf("in %s", i.ProbeLatency.Round(1e6).String())))

		card.Section(N("Logging"), T("e: change"))
		switch {
		case levelsErr != nil:
			card.Line(tui.Paint(theme.Err, levelsErr.Error()))
		case levels == nil:
			card.Line(tui.Faint(T("loading…")))
		}
		for _, l := range levels {
			name := l.Name
			if name == "" {
				name = N("log level") // before mr9.5: one level for everything
			}
			level := tui.Strong(strconv.Itoa(l.Level)) + " " + tui.Faint(rtpengine.LevelName(l.Level))
			if l.Level >= 7 { // debug floods the log on a busy host
				level = tui.Paint(theme.Warn, strconv.Itoa(l.Level)+" "+rtpengine.LevelName(l.Level))
			}
			if l.Desc != "" {
				level += tui.Faint("  " + l.Desc)
			}
			card.KV(name, level)
		}

		card.Section(N("CLI commands"), i.Caps.Source)
		for _, cmd := range rtpengine.MatrixCommands() {
			mark := tui.Paint(theme.Err, T("no"))
			if i.Caps.Has(cmd) {
				mark = tui.Paint(theme.OK, T("yes"))
			}
			card.KV(cmd, mark+tui.Faint("  "+Tf("since %s", rtpengine.Since(cmd))))
		}
		v.SetContent(card.String(), "markup")
	}
	load := func(probe bool) {
		c := app.Client()
		app.Async(func(ctx context.Context) func() {
			var err error
			if probe {
				_, err = c.Probe(ctx)
			}
			lv, lerr := c.LogLevels(ctx)
			return func() {
				if probe {
					app.UpdateInfo()
				}
				if err != nil {
					app.Flash().Err(err)
				}
				levels, levelsErr = lv, lerr
				if levels == nil && lerr == nil {
					levels = []rtpengine.LogLevel{}
				}
				render()
			}
		})
	}
	v.Loader = func() { load(true) }
	v.Keys['e'] = func() { app.Push(newLogLevelsView(app)) }
	v.Extra = []Hint{{Key: "e", Desc: N("log levels")}}
	if app.Client().Probed() {
		render()
		load(false)
	} else {
		load(true)
	}
	return v
}

// orFaintDash is s, or a dim "-" when it is empty.
func orFaintDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return tui.Faint("-")
	}
	return tview.Escape(s)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// helpSections are the rtpengine-tui sections of the ? help.
func helpSections(h *tui.Help) {
	h.Section(T("Hosts"))
	h.Key("enter", T("sessions of the host"))
	h.Key("space", T("make current (marked *, commands like :config work on it)"))
	h.Key("c / m / i / p", T("config files · limits · interfaces · proxies"))
	h.Key("d", T("describe: version, process, config paths, log levels, CLI commands"))
	h.Key("s", T("interactive ssh shell"))
	h.Section(T("Sessions"))
	h.Key("enter", T("call details (call <id> info on mr10.5+, list sessions <id> before)"))
	h.Key("space", T("mark row (sessions: terminate works on marked rows)"))
	h.Key("y", T("in call details: raw reply / parsed view"))
	h.Key("ctrl-k", T("terminate marked/current calls"))
	h.Key("v", "all / own / foreign")
	h.Key("D", T("per-call debug logging (mr9.5+)"))
	h.Key("a", T("toggle auto-refresh"))
	h.Section(T("Config (rtpengine.conf, rtpengine-recording.conf)"))
	h.Key("enter / d", T("option details: description, releases that have it; on a template: its flags explained"))
	h.Key("v", T("all entries / daemon sections / findings only"))
	h.Key("y / Y", T("raw rtpengine.conf / rtpengine-recording.conf"))
	h.Line(T("  status: ok · runtime differs (params current ≠ file) · overridden (duplicate key)"))
	h.Line(T("          not in version (option of another release) · unknown (typo) · section (interface/templates/other)"))
	h.Section(T("Limits"))
	h.Key("e / enter", T("prefill :set <name> in the prompt"))
}
