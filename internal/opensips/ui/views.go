package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

func toRows(t *opensips.Table) []Row {
	rows := make([]Row, 0, len(t.Rows))
	for i := range t.Rows {
		r := &t.Rows[i]
		row := Row{Key: r.Key, Data: r}
		for _, c := range r.Cells {
			row.Cells = append(row.Cells, c.Text)
			row.Colors = append(row.Colors, levelColor(c.Level))
		}
		rows = append(rows, row)
	}
	return rows
}

func source(r *Row) *opensips.Row {
	if r == nil {
		return nil
	}
	sr, _ := r.Data.(*opensips.Row)
	return sr
}

func toJSON(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(out)
}

// ResourceView shows one OpenSIPS resource of one host, refreshed every refreshSeconds.
type ResourceView struct {
	*TableView
	app     *App
	res     *opensips.Resource
	client  *opensips.Client
	memo    map[string]any
	loading bool
	extra   []Hint // hints of keys bound outside the resource's actions
}

func newResourceView(app *App, res *opensips.Resource, client *opensips.Client) *ResourceView {
	v := &ResourceView{TableView: NewTableView(app, res.Title, nil), app: app, res: res, client: client, memo: map[string]any{}}
	v.SetTitleInfo(client.Host.Name)
	if res.Name == "allstats" {
		// a switches to the statistics by category, as y switches to JSON
		v.Bind('a', func() {
			if err := app.openView("stats", false); err != nil {
				app.Flash().Err(err)
			}
		})
		v.extra = []Hint{{Key: "a", Desc: N("by category")}}
	}
	v.OnEnter(func(r *Row) {
		if res.Name == "commands" {
			app.RunMI(client, r.Key, nil)
			return
		}
		v.describe()
	})
	v.Bind('d', v.describe)
	v.Bind('y', v.json)
	v.Bind('r', v.Refresh)
	v.Bind('s', func() { app.Shell(client.Host.Name, false) })
	v.Bind('i', func() { app.Shell(client.Host.Name, true) })
	for _, act := range res.Actions {
		act := act
		v.Bind([]rune(act.Key)[0], func() { v.runAction(act) })
	}
	v.Refresh()
	return v
}

func (v *ResourceView) Name() string      { return v.res.Name }
func (v *ResourceView) Table() *TableView { return v.TableView }
func (v *ResourceView) Close()            {}
func (v *ResourceView) AutoRefresh() bool { return v.res.Name != "commands" }

func (v *ResourceView) actions() []opensips.Action {
	var out []opensips.Action
	for _, a := range v.res.Actions {
		if v.client.Supports(a.Op).OK {
			out = append(out, a)
		}
	}
	return out
}

func (v *ResourceView) Hints() []Hint {
	var h []Hint
	if v.res.Name == "commands" {
		h = append(h, Hint{Key: "enter", Desc: N("run")})
	}
	for _, a := range v.actions() {
		h = append(h, Hint{Key: a.Key, Desc: a.Label})
	}
	if _, ok := cards[v.res.Name]; ok {
		h = append(h, Hint{Key: "y", Desc: "JSON"})
	}
	h = append(h, v.extra...)
	return append(h, Hint{Key: "d", Desc: N("describe")}, Hint{Key: "space", Desc: N("mark")}, Hint{Key: "s", Desc: "ssh"}, Hint{Key: "i", Desc: "opensips-cli"})
}

// Refresh fetches the resource in the background.
func (v *ResourceView) Refresh() {
	if v.loading {
		return
	}
	v.loading = true
	if len(v.Rows()) == 0 {
		v.SetLoading(true)
	}
	v.app.Async(func(ctx context.Context) func() {
		if err := v.app.ensureProbed(ctx, v.client); err != nil {
			return func() {
				v.loading = false
				v.SetLoading(false)
				v.app.Flash().Errf("%s: %v", v.client.Host.Name, err)
				v.app.UpdateInfo()
			}
		}
		if s := v.client.Supports(v.res.Op); !s.OK && v.res.Name != "info" {
			return func() {
				v.loading = false
				v.SetHeaders([]string{N("UNAVAILABLE")})
				v.SetRows([]Row{{Key: "why", Cells: []string{s.Reason}, Color: theme.Err}})
				v.app.Flash().Warnf("%s: %s", v.res.Name, s.Reason)
				v.app.UpdateInfo()
			}
		}
		t, err := v.res.Fetch(ctx, v.client, v.memo)
		return func() {
			v.loading = false
			v.SetLoading(false)
			if err != nil {
				v.app.Flash().Errf("%s: %v", v.client.Host.Name, err)
				return
			}
			v.SetHeaders(t.Columns)
			v.SetRows(toRows(t))
			v.app.UpdateInfo()
			v.app.RefreshHints()
		}
	})
}

// describe opens the card of the row, or its JSON for a resource without
// a card.
func (v *ResourceView) describe() {
	sr := source(v.Selected())
	if sr == nil {
		return
	}
	card, ok := cards[v.res.Name]
	m := opensips.AsMap(sr.Data)
	if !ok || m == nil {
		v.json()
		return
	}
	v.app.Push(newCardView(v.app, v.res.Name+"/"+sr.Key, func() *tui.Card { return card(m) }, sr.Data))
}

// json shows the row as it came from MI.
func (v *ResourceView) json() {
	sr := source(v.Selected())
	if sr == nil {
		return
	}
	tv := newTextViewer(v.app, v.res.Name+"/"+sr.Key, "json")
	tv.SetContent(toJSON(sr.Data), "json")
	tv.Keys['y'] = v.app.Pop // y again goes back to the table
	tv.Extra = []Hint{{Key: "y", Desc: N("back to the table")}}
	v.app.Push(tv)
}

func (v *ResourceView) runAction(act opensips.Action) {
	if s := v.client.Supports(act.Op); !s.OK {
		v.app.Flash().Warnf("%s: %s", T(act.Label), s.Reason)
		return
	}
	type call struct {
		cmd    string
		params opensips.Params
	}
	var calls []call
	label, verbs := act.Label, map[string]bool{}
	if act.Args == nil {
		cmd, p, err := v.client.CommandFor(act.Op, nil)
		if err != nil {
			v.app.Flash().Err(err)
			return
		}
		calls = append(calls, call{cmd, p})
	} else {
		for _, r := range v.Marked() {
			sr := source(r)
			if sr == nil {
				continue
			}
			cmd, p, err := v.client.CommandFor(act.Op, act.Args(sr))
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			calls = append(calls, call{cmd, p})
			if act.Verb != nil {
				verbs[act.Verb(sr)] = true
			}
		}
	}
	if len(calls) == 0 {
		return
	}
	// "enable" or "disable" when every row goes the same way
	if len(verbs) == 1 {
		for verb := range verbs {
			label = verb
		}
	}
	var b strings.Builder
	b.WriteString(upperFirst(Tf("%s on %s (OpenSIPS %s)?", T(label), v.client.Host.Name, v.client.Info().Version())) + "\n\n")
	for i, c := range calls {
		if i == 3 {
			b.WriteString(Tf("… and %d more", len(calls)-3) + "\n")
			break
		}
		b.WriteString(v.client.Render(c.cmd, c.params) + "\n")
	}
	v.app.Confirm(b.String(), upperFirst(T(label)), func() {
		v.app.Async(func(ctx context.Context) func() {
			var errs []string
			for _, c := range calls {
				if _, err := v.client.MI(ctx, c.cmd, c.params); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", v.client.Render(c.cmd, c.params), err))
				}
			}
			return func() {
				v.ClearMarks()
				switch {
				case len(errs) > 0:
					v.app.Flash().Errf("%s", strings.Join(errs, "; "))
				case len(calls) == 1:
					v.app.Flash().Infof("%s: %s", v.client.Host.Name, v.client.Render(calls[0].cmd, calls[0].params))
				default:
					v.app.Flash().Infof("%s: %s × %d", v.client.Host.Name, T(label), len(calls))
				}
				v.Refresh()
			}
		})
	})
}

// HostsView lists configured OpenSIPS hosts with their probe state and
// active dialogs, as the host list of rtpengine-tui: enter opens the
// dialogs of a host (the sessions there), space makes it current, d opens
// its card.
type HostsView struct {
	*TableView
	app      *App
	dialogs  map[string]string // active dialogs by host, "" not known yet
	fetching map[string]bool
}

func newHostsView(app *App) *HostsView {
	v := &HostsView{TableView: NewTableView(app, N("Hosts"), []string{"", N("NAME"), N("ADDRESS"), N("VIA"), N("VERSION"), N("STATUS"), N("DIALOGS"), N("LABELS")}),
		app: app, dialogs: map[string]string{}, fetching: map[string]bool{}}
	v.OnEnter(func(*Row) {
		v.withHost(func() {
			if err := app.openView("dialogs", true); err != nil {
				app.Flash().Err(err)
			}
		})
	})
	// space selects the host here instead of marking rows
	v.Bind(' ', func() { v.withHost(func() { app.Flash().Infof("current host: %s", app.host.Name) }) })
	v.Bind('d', func() { v.withHost(func() { app.Push(newDescribeView(app)) }) })
	v.Bind('r', func() {
		if r := v.Selected(); r != nil {
			app.Probe(r.Key)
			v.Refresh()
		}
	})
	v.Bind('s', func() { v.withHost(func() { app.Shell(app.host.Name, false) }) })
	v.Bind('i', func() { v.withHost(func() { app.Shell(app.host.Name, true) }) })
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
		v.app.host = h
		v.app.cfg.CurrentHost = h.Name
		v.app.UpdateInfo()
		v.render()
		f()
	}
}

func (v *HostsView) Name() string      { return "hosts" }
func (v *HostsView) Table() *TableView { return v.TableView }
func (v *HostsView) Close()            {}
func (v *HostsView) AutoRefresh() bool { return true }
func (v *HostsView) Hints() []Hint {
	return []Hint{{Key: "enter", Desc: N("dialogs")}, {Key: "space", Desc: N("select")}, {Key: "d", Desc: N("describe")},
		{Key: "r", Desc: N("re-probe")}, {Key: "s", Desc: "ssh"}, {Key: "i", Desc: "opensips-cli"}}
}

// Refresh renders the probe state and asks every host that has the dialog
// module for its active dialogs (re-probing is r per host).
func (v *HostsView) Refresh() {
	v.render()
	for i := range v.app.cfg.Hosts {
		name := v.app.cfg.Hosts[i].Name
		cl := v.app.clients[name]
		if v.fetching[name] || cl.Info() == nil || !cl.Supports("stats.get").OK || !cl.Info().Commands["dlg_list"] {
			continue
		}
		v.fetching[name] = true
		v.app.Async(func(ctx context.Context) func() {
			n, err := cl.ActiveDialogs(ctx)
			return func() {
				v.fetching[name] = false
				v.dialogs[name] = "-"
				if err == nil {
					v.dialogs[name] = strconv.FormatInt(n, 10)
				}
				v.render()
			}
		})
	}
}

func (v *HostsView) render() {
	var rows []Row
	for _, c := range v.app.cfg.Hosts {
		cur := ""
		if v.app.host != nil && c.Name == v.app.host.Name {
			cur = "*"
		}
		probing, unreachable := v.app.HostState(c.Name)
		h := v.app.clients[c.Name].Info()
		via, version, status := c.Via, "", T("probing…")
		vc, sc := tcell.Color(0), theme.Dim
		switch {
		case unreachable != "":
			status, sc = unreachable, theme.Err
		case h != nil:
			version = h.Version().String()
			if h.Running.IsZero() && h.Binary.IsZero() {
				version = ""
			}
			if h.RestartPending() {
				version, vc = Tf("%s (installed %s)", h.Running, h.Binary), theme.Warn
			}
			if h.Backend == opensips.BackendLegacy {
				via += " → opensipsctl"
			}
			switch {
			case h.Error != "":
				status, sc = h.Error, theme.Warn
				if h.Backend == "" {
					sc = theme.Err
				}
			case len(h.Warnings) > 0:
				status, sc = h.Warnings[0], theme.Warn
			case h.RestartPending():
				status, sc = Tf("running %s, installed %s: restart OpenSIPS", h.Running, h.Binary), theme.Warn
			default:
				status, sc = "OK", theme.OK
			}
		case !probing:
			status = "-"
		}
		rows = append(rows, Row{Key: c.Name, Cells: []string{cur, c.Name, address(&c), via, version, status, v.dialogs[c.Name], c.Labels.String()},
			Colors: []tcell.Color{theme.OK, 0, 0, 0, vc, sc}})
	}
	v.SetRows(rows)
	v.SetTitleInfo(tui.ShortPath(v.app.cfg.Path()))
}

// upperFirst capitalizes the first letter: a question or a button label
// built from an action name.
func upperFirst(s string) string {
	for i, r := range s {
		return string(unicode.ToUpper(r)) + s[i+utf8.RuneLen(r):]
	}
	return s
}
