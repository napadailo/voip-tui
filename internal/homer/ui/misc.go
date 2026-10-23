package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// HostsView lists the configured Homer hosts with their login state, as
// the host list of rtpengine-tui: enter opens the calls of a host, space
// makes it current, d opens its card, r logs in again.
type HostsView struct {
	*TableView
	app *App
}

func newHostsView(app *App) *HostsView {
	v := &HostsView{TableView: NewTableView(app, N("Hosts"), []string{"", N("NAME"), N("ADDRESS"), N("AUTH"), N("USER"), N("STATUS"), N("NODES"), N("LABELS")}), app: app}
	v.OnEnter(func(*Row) {
		v.withHost(func() {
			if err := app.whenReady(true, func(show func(View)) error { return app.search("1_call", "", show) }); err != nil {
				app.Flash().Err(err)
			}
		})
	})
	// space selects the host here instead of marking rows
	v.Bind(' ', func() { v.withHost(func() { app.Flash().Infof("current host: %s", app.host.Name) }) })
	v.Bind('d', func() {
		v.withHost(func() {
			_ = app.whenReady(true, func(show func(View)) error {
				show(newDescribeView(app))
				return nil
			})
		})
	})
	v.Bind('r', func() {
		if r := v.Selected(); r != nil {
			app.Probe(r.Key)
			v.Refresh()
		}
	})
	v.Refresh()
	return v
}

// withHost makes the selected row the current host, then runs f.
func (v *HostsView) withHost(f func()) {
	r := v.Selected()
	if r == nil {
		return
	}
	if h, ok := v.app.cfg.Find(r.Key); ok {
		v.app.setHost(h)
		v.app.UpdateInfo()
		v.Refresh()
		f()
	}
}

func (v *HostsView) Name() string      { return "hosts" }
func (v *HostsView) Table() *TableView { return v.TableView }
func (v *HostsView) Close()            {}
func (v *HostsView) AutoRefresh() bool { return true }
func (v *HostsView) Hints() []Hint {
	return []Hint{{Key: "enter", Desc: N("calls")}, {Key: "space", Desc: N("select")}, {Key: "d", Desc: N("describe")}, {Key: "r", Desc: N("log in again")}}
}

// Refresh renders the login state of every host.
func (v *HostsView) Refresh() {
	var rows []Row
	for i := range v.app.cfg.Hosts {
		h := &v.app.cfg.Hosts[i]
		cur := ""
		if v.app.host != nil && h.Name == v.app.host.Name {
			cur = "*"
		}
		v.app.mu.RLock()
		st := v.app.hosts[h.Name]
		probing, probed, failed := st.probing, st.probed, st.err
		m, c := st.meta, st.client
		v.app.mu.RUnlock()
		auth := h.Auth()
		if h.Insecure {
			auth += " · " + T("TLS not verified")
		}
		user, status, nodes := h.Username, T("probing…"), ""
		sc := theme.Dim
		switch {
		case failed != "":
			status, sc = failed, theme.Err
		case probing || !probed:
		default:
			if c != nil {
				user = userLabel(c, m.profile)
			}
			status, sc = "OK", theme.OK
			if len(m.warnings) > 0 {
				status, sc = m.warnings[0], theme.Warn
			}
			var vals []string
			for _, n := range m.nodes {
				vals = append(vals, n.Value)
			}
			nodes = strings.Join(vals, ",")
		}
		rows = append(rows, Row{Key: h.Name, Cells: []string{cur, h.Name, h.URL, auth, user, status, nodes, h.Labels.String()},
			Colors: []tcell.Color{theme.OK, 0, 0, 0, 0, sc}})
	}
	v.SetRows(rows)
	v.SetTitleInfo(tui.ShortPath(v.app.cfg.Path()))
}

// helpSections are the homer-tui sections of the ? help.
func (a *App) helpSections(h *tui.Help) {
	h.Section(T("Search results (:calls, :reg, :sip, :logs, :search)"))
	h.Key("enter", T("open the call (correlated transaction) of marked/current rows"))
	h.Key("space", T("mark row (multi-select for call/pcap/export)"))
	h.Key("s", T("search form (fields from the profile mapping)"))
	h.Key("e", T("edit the query in the command prompt"))
	h.Key("v", T("sessions or single messages"))
	h.Key("j / G / ctrl-d", T("near the end the rows read on, further back in the window"))
	h.Key("[ / ]", T("move the time window back / forward"))
	h.Key("a", T("toggle auto-refresh (relative ranges)"))
	h.Key("p / t", T("download PCAP / text of marked/current sessions"))
	h.Key("d", T("card of the row: a message, or a session with its messages"))
	h.Key("y", T("row as JSON, y again closes it"))

	h.Section(T("Call view"))
	h.Key("1 2 3 4 / tab", T("flow (ladder) · messages · QoS (RTCP/RTP) · HEP logs"))
	h.Key("enter", T("full message in a pager"))
	h.Key("z", T("toggle the detail pane"))
	h.Key("h l / ← → / 0 $", T("scroll the flow by host, when the hosts do not fit"))
	h.Key("f", T("which messages to show: SIP, RTCP, LOG; until homer-tui quits"))
	h.Key("a", T("one lane per alias instead of per address"))
	h.Key("u", T("a call that comes back gets its hosts again on the right (on at start)"))
	h.Key("v", T("a SIP message in plain words or as it is"))
	h.Key("v", T("on the QoS tab: the reports as graphs per stream or as a table"))
	h.Key("w", T("on the Logs tab: the logs whole and wrapped, or as a table"))
	h.Key("m", T("focus the message pane to scroll it; m, esc or q go back"))
	h.Key("F", T("follow the call: reload it on every tick until it ends"))
	h.Key("p / t / y", T("PCAP / text / JSON"))

	h.Section(T("Settings views"))
	h.Key("d / enter", T("card of the item"))
	h.Key("y", T("item as JSON, y again closes it"))
	h.Key("e", T("edit in $EDITOR (HOMER_TUI_EDITOR, VISUAL, EDITOR, vi)"))
	h.Key("c", T("create from template"))
	h.Key("shift-d", T("delete (asks to confirm)"))
	h.Key("shift-t", T("(aliases) toggle active"))
	h.Key("x", T("(mappings) reset to the built-in one; the card shows what it is, search fields, the flow, problems"))
	h.Key("b", T("(mappings) build the flow from blocks: LOG, RTCP, RTP, SIP, LEG; s saves"))

	h.Section(T("Hosts view"))
	h.Key("enter", T("calls of the host"))
	h.Key("space", T("make the host current (marked *)"))
	h.Key("d", T("host card: user, database nodes, modules, auth types, profiles"))
	h.Key("r", T("log in again"))
	h.Key(":<host>", T("switch to the host, as :host <name>"))

	h.Section(T("Search syntax"))
	h.Line(T(`  :calls from_user=100 to_user=200 since=2h limit=500
  :calls callid=abc@host                   (any mapping field: id, short name or title)
  :calls method=INVITE;BYE                 (; = any of)
  :calls from_user=10%                     (% = LIKE)
  :calls user_agent!=friendly-scanner      (!= = NOT)
  :calls from="2026-10-06 10:00" to=11:30  (absolute window, also -3h, RFC3339, unix)
  :reg node=node1,node2 or                 (nodes, OR between conditions)
  :calls since=1d where data_header.from_user = '100' AND data_header.method = 'INVITE'
  :search 5_default since=30m              (any hepid_profile, see :mappings)`))
	h.Section(T("Profiles on this server"))
	for _, m := range a.Mappings() {
		h.Line(fmt.Sprintf("  %-18s %s", m.Key(), m.Title()))
	}
}
