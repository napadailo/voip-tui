package ui

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/internal/opensips/config"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// describePrefix starts the name of the host card, so that switching hosts
// keeps it open.
const describePrefix = "info "

// newDescribeView is the card of the current host, as describe in
// rtpengine-tui: what the probe found, the log levels of the processes and
// the MI commands opensips-tui uses. A refresh probes the host again.
func newDescribeView(app *App) View {
	h, cl := app.host, app.Client()
	v := newTextViewer(app, describePrefix+h.Name, "markup")
	var lv levels
	showRaw := false
	render := func() {
		_, unreachable := app.HostState(h.Name)
		if showRaw {
			v.SetContent(toJSON(map[string]any{"host": h, "unreachable": unreachable, "probe": cl.Info()}), "json")
			return
		}
		v.SetContent(describeCard(h, cl, unreachable, lv).String(), "markup")
	}
	loadLevels := func() {
		if !cl.Supports("core.log_level").OK {
			render()
			return
		}
		app.Async(func(ctx context.Context) func() {
			var n levels
			n.procs, n.err = cl.ProcessLevels(ctx)
			if cl.Supports("core.xlog_level").OK {
				x, err := cl.XlogLevel(ctx)
				n.xlog, n.xlogErr = &x, err
			}
			return func() {
				lv = n
				lv.loaded = true
				render()
			}
		})
	}
	v.Loader = func() { app.probe(h.Name, loadLevels) }
	v.Keys['e'] = func() { app.Push(newLogLevelsView(app)) }
	v.Keys['y'] = func() {
		showRaw = !showRaw
		render()
		v.Text().ScrollToBeginning() // the other layout has other lines
	}
	v.Extra = []Hint{{Key: "e", Desc: N("log levels")}, {Key: "y", Desc: N("JSON/parsed")}}
	if cl.Info() != nil {
		render()
		loadLevels()
	} else {
		v.SetLoading()
		app.probe(h.Name, loadLevels)
	}
	return v
}

// levels is what the host card shows under Logging.
type levels struct {
	loaded  bool
	procs   []opensips.ProcLevel
	err     error
	xlog    *int64
	xlogErr error
}

// levelText is a level with its name; debug floods the log on a busy host.
func levelText(l int64) string {
	if l >= 4 {
		return tui.Paint(theme.Warn, strconv.FormatInt(l, 10)+" "+opensips.LevelName(l))
	}
	return tui.Strong(strconv.FormatInt(l, 10)) + " " + tui.Faint(opensips.LevelName(l))
}

// describeCard lays out the host as describe in rtpengine-tui: the
// OpenSIPS process, logging, and the MI commands opensips-tui uses.
func describeCard(h *config.Host, cl *opensips.Client, unreachable string, lv levels) *tui.Card {
	i := cl.Info()
	via := Tf("via %s", h.Via)
	if i != nil {
		if mi := miLabel(h, i.Backend); mi != "" {
			via += " (" + mi + ")"
		}
	}
	ro := ""
	if cl.ReadOnly {
		ro = T("read-only")
	}
	card := tui.NewCard(h.Name).Sub(address(h), via, h.Labels.String(), ro)

	card.Section(N("OpenSIPS"))
	switch {
	case unreachable != "":
		card.KV(N("status"), tui.Paint(theme.Err, unreachable))
	case i == nil:
		card.KV(N("status"), tui.Faint(T("probing…")))
	default:
		version := tui.Strong(i.Running.String())
		if i.Running.IsZero() {
			version = tui.Paint(theme.Warn, T("unknown"))
		}
		if i.Server != "" {
			version += tui.Faint(" · " + i.Server)
		}
		card.KV(N("version"), version)
		switch {
		case i.RestartPending():
			card.KV(N("installed"), tui.Paint(theme.Warn, Tf("%s, running %s: restart OpenSIPS", i.Binary, i.Running)))
		case !i.Binary.IsZero() && i.Running.IsZero():
			card.KV(N("installed"), tview.Escape(i.Binary.String()))
		}
		if i.Backend == "" && i.Error != "" {
			card.KV(N("MI error"), tui.Paint(theme.Err, i.Error))
		}
		process := tui.Faint("-")
		if i.PID != "" {
			process = tui.Strong(i.PID)
			if up := i.UptimeNow(); up >= 0 {
				process += " " + tview.Escape(Tf("up %s", opensips.Duration(up)))
			}
			process += tui.Faint("  " + i.CmdLine)
		}
		card.KV(N("process"), process)
		packages := tui.Faint("-")
		if len(i.Packages) > 0 {
			packages = tview.Escape(strings.Join(i.Packages, "; "))
		}
		card.KV(N("packages"), packages)
		config := tui.Faint("-")
		switch {
		case i.ConfigPath() != "":
			config = tview.Escape(i.ConfigPath())
		case i.CmdLine != "":
			config = tui.Faint(T("no -f: opensips.cfg of the install prefix"))
		}
		card.KV(N("config"), config)
		if i.Backend != "" && i.Error != "" {
			card.KV(N("error"), tui.Paint(theme.Err, i.Error))
		}
		for _, w := range i.Warnings {
			card.KV(N("warning"), tui.Paint(theme.Warn, w))
		}
		card.KV(N("probed"), tui.Faint(i.ProbedAt.Format("15:04:05")+" · "+Tf("in %s", i.Latency.Round(1e6).String())))
	}

	card.Section(N("Logging"), T("e: change"))
	switch s := cl.Supports("core.log_level"); {
	case !s.OK:
		card.Line(tui.Faint(s.Reason))
	case lv.err != nil:
		card.Line(tui.Paint(theme.Err, lv.err.Error()))
	case !lv.loaded:
		card.Line(tui.Faint(T("loading…")))
	}
	for _, l := range opensips.LogLevels(lv.procs) {
		card.KVRaw("log_level", levelText(l.Level)+tui.Faint("  "+Tf("%d processes", l.Processes)))
	}
	switch s := cl.Supports("core.xlog_level"); {
	case !s.OK:
		card.KVRaw("xlog_level", tui.Faint(s.Reason))
	case lv.xlogErr != nil:
		card.KVRaw("xlog_level", tui.Paint(theme.Err, lv.xlogErr.Error()))
	case lv.xlog != nil:
		card.KVRaw("xlog_level", levelText(*lv.xlog)+tui.Faint("  "+T("xlog() in the script")))
	}

	card.Section(N("MI commands"), T("the ones opensips-tui uses"))
	for _, m := range miCommands(cl) {
		mark := tui.Paint(theme.OK, T("yes"))
		note := Tf("since %s", m.since)
		if !m.ok {
			mark, note = tui.Paint(theme.Err, T("no")), m.reason
		}
		card.KVRaw(m.command, mark+tui.Faint("  "+strings.Join(m.titles, ", ")+" · "+note))
	}
	return card
}

// miCommand is an MI command of the operations table, with the operations
// that use it.
type miCommand struct {
	command, since, reason string
	titles                 []string
	ok                     bool
}

// miCommands lists the MI commands of the operations, checked against the
// host: the variant for its version, or the first one.
func miCommands(cl *opensips.Client) []miCommand {
	byCmd := map[string]*miCommand{}
	var names []string
	for name := range opensips.Operations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		op := opensips.Operations[name]
		s := cl.Supports(name)
		v := op.Variants[0]
		if s.OK {
			v = *s.Variant
		}
		m := byCmd[v.Command]
		if m == nil {
			m = &miCommand{command: v.Command, since: v.Since.Branch(), reason: s.Reason}
			byCmd[v.Command] = m
		}
		m.titles = append(m.titles, T(op.Title))
		if s.OK {
			m.ok, m.since = true, v.Since.Branch()
		}
	}
	var out []miCommand
	for _, m := range byCmd {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].command < out[j].command })
	return out
}

// address is where a host is reached: the ssh target, or the mi_http URL
// with via: direct.
func address(h *config.Host) string {
	if h.Via == config.ViaDirect {
		return h.MI
	}
	return h.Target()
}

// miLabel names how MI reaches OpenSIPS on a host.
func miLabel(h *config.Host, backend string) string {
	switch backend {
	case opensips.BackendCLI:
		if h.Sudo {
			return "sudo opensips-cli"
		}
		return "opensips-cli"
	case opensips.BackendFIFO:
		return "mi_fifo " + h.FIFO
	case opensips.BackendHTTP:
		if h.Via == config.ViaDirect {
			return "mi_http " + h.MI
		}
		return "mi_http " + h.MI + " (ssh -W)"
	case opensips.BackendLegacy:
		return "opensipsctl fifo"
	}
	return ""
}
