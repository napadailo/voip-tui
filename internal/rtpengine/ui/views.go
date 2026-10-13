package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/internal/rtpengine/config"
)

// HostsView lists configured rtpengine machines with live session counts.
type HostsView struct {
	*TableView
	app *App
	mu  sync.Mutex
	num map[string]hostState
}

type hostState struct {
	n    rtpengine.NumSessions
	max  string
	err  error
	done bool
}

func newHostsView(app *App) *HostsView {
	v := &HostsView{TableView: NewTableView(app, N("Hosts"), []string{"", N("NAME"), N("ADDRESS"), N("VIA"), N("VERSION"), N("STATUS"), N("OWN"), N("FOREIGN"), N("TOTAL"), N("MAX"), N("LABELS")}), app: app, num: map[string]hostState{}}
	v.OnEnter(func(*Row) { v.withHost(func() { app.Push(newSessionsView(app, "all")) }) })
	// space selects the host here instead of marking rows
	v.Bind(' ', func() {
		v.withHost(func() {
			v.render()
			app.Flash().Infof("current host: %s", app.host.Name)
		})
	})
	v.Bind('d', func() { v.withHost(func() { app.Push(newDescribeView(app)) }) })
	v.Bind('c', func() { v.withHost(func() { app.Push(newSettingsView(app)) }) })
	v.Bind('s', func() { v.withHost(app.Shell) })
	v.Bind('i', func() { v.withHost(func() { app.Push(newInterfacesView(app)) }) })
	v.Bind('m', func() { v.withHost(func() { app.Push(newTunablesView(app)) }) })
	v.Bind('p', func() { v.withHost(func() { app.Push(newProxiesView(app)) }) })
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
		v.app.ensureProbed(f)
	}
}

func (v *HostsView) Name() string      { return "hosts" }
func (v *HostsView) Table() *TableView { return v.TableView }
func (v *HostsView) Close()            {}
func (v *HostsView) AutoRefresh() bool { return true }
func (v *HostsView) Hints() []Hint {
	return []Hint{{Key: "enter", Desc: N("sessions")}, {Key: "space", Desc: N("select")}, {Key: "c", Desc: N("config files")}, {Key: "m", Desc: N("limits")}, {Key: "i", Desc: N("interfaces")}, {Key: "p", Desc: N("proxies")}, {Key: "d", Desc: N("describe")}, {Key: "s", Desc: N("shell")}}
}

// Refresh probes unprobed hosts (all on ctrl-r) and polls session counts.
func (v *HostsView) Refresh() {
	v.render()
	for i := range v.app.cfg.Hosts {
		h := &v.app.cfg.Hosts[i]
		c := v.app.ClientFor(h.Name)
		v.app.Async(func(ctx context.Context) func() {
			var st hostState
			if !c.Probed() || c.Info().CLIErr != nil {
				_, st.err = c.Probe(ctx)
			}
			if st.err == nil {
				st.n, st.err = c.NumSessions(ctx)
				if out, err := c.Do(ctx, "list maxsessions"); err == nil {
					st.max = rtpengine.ParseValue(out)
				}
			}
			st.done = true
			return func() {
				v.mu.Lock()
				v.num[h.Name] = st
				v.mu.Unlock()
				v.render()
				if v.app.host != nil && v.app.host.Name == h.Name {
					v.app.UpdateInfo()
				}
			}
		})
	}
}

func (v *HostsView) render() {
	var rows []Row
	v.mu.Lock()
	defer v.mu.Unlock()
	for i := range v.app.cfg.Hosts {
		h := &v.app.cfg.Hosts[i]
		c := v.app.ClientFor(h.Name)
		info := c.Info()
		st := v.num[h.Name]
		cur := ""
		if v.app.host != nil && v.app.host.Name == h.Name {
			cur = "*"
		}
		const (
			colVersion, colStatus, colOwn, colForeign, colTotal, colMax = 4, 5, 6, 7, 8, 9
		)
		r := Row{Key: h.Name, Cells: []string{cur, h.Name, c.Target(), h.Via, info.Version.String(), T("probing"), "", "", "", "", labels(h)}, Tone: ToneDim}
		r.CellTones = map[int]Tone{}
		if !info.Version.Known() && !info.ProbedAt.IsZero() {
			r.Cells[colVersion] = T("unknown")
		}
		if st.done {
			switch {
			case st.err != nil:
				r.Cells[colStatus], r.Tone = trunc(st.err.Error(), 48), ToneErr
			default:
				r.Cells[colStatus], r.Tone = T("up"), ToneFg
				if info.PID == "" && c.Shell != nil && h.Via != config.ViaDirect {
					r.Cells[colStatus] = T("up (no pid)")
				}
				r.CellTones[colStatus] = ToneOK
				r.Cells[colOwn], r.Cells[colForeign], r.Cells[colTotal], r.Cells[colMax] = strconv.Itoa(st.n.Own), strconv.Itoa(st.n.Foreign), strconv.Itoa(st.n.Total), st.max
				if m, err := strconv.Atoi(st.max); err == nil && m > 0 && st.n.Total*10 >= m*9 {
					r.CellTones[colTotal] = ToneWarn
				}
			}
		}
		rows = append(rows, r)
	}
	v.SetRows(rows)
	v.SetTitleInfo(shortPath(v.app.cfg.Path()))
}

func labels(h *config.Host) string {
	var p []string
	for k, val := range h.Labels {
		if val == "" { // a tag: labels: prod
			p = append(p, k)
			continue
		}
		p = append(p, k+"="+val)
	}
	sort.Strings(p)
	return strings.Join(p, ",")
}

// SessionsView is `list sessions all|own|foreign`.
type SessionsView struct {
	*TableView
	app   *App
	which string
	proxy string // only calls from this proxy address, "" for all
	auto  bool
	busy  bool
}

func newSessionsView(app *App, which string) *SessionsView {
	v := &SessionsView{TableView: NewTableView(app, N("Sessions"), []string{N("CALL-ID"), N("AGE"), N("CREATED"), N("PROXY"), N("OWNER"), N("KEYSPACE"), N("STATE")}), app: app, which: which, auto: true}
	v.OnEnter(func(r *Row) { app.Push(newCallView(app, r.Key)) })
	v.BindKey(tcell.KeyCtrlK, v.terminate)
	v.Bind('v', func() {
		v.which = map[string]string{"all": "own", "own": "foreign", "foreign": "all"}[v.which]
		v.Refresh()
	})
	v.Bind('a', func() {
		v.auto = !v.auto
		app.Flash().Infof("auto-refresh %v", T(map[bool]string{true: N("on"), false: N("off")}[v.auto]))
	})
	v.Bind('D', v.debug)
	v.Bind('L', func() {
		if r := v.Selected(); r != nil {
			s, _ := r.Data.(rtpengine.Session)
			app.Push(newCallLogView(app, r.Key, s.Created.Unix()))
		}
	})
	app.ensureProbed(v.Refresh)
	return v
}

// newProxySessionsView lists the calls one proxy address has on the host.
func newProxySessionsView(app *App, proxy string) *SessionsView {
	v := newSessionsView(app, "all")
	v.proxy = proxy
	return v
}

func (v *SessionsView) Name() string      { return "sessions" }
func (v *SessionsView) Table() *TableView { return v.TableView }
func (v *SessionsView) Close()            {}
func (v *SessionsView) AutoRefresh() bool { return v.auto }
func (v *SessionsView) Hints() []Hint {
	h := []Hint{{Key: "enter", Desc: N("details")}, {Key: "space", Desc: N("mark")}, {Key: "ctrl-k", Desc: N("terminate")}, {Key: "v", Desc: N("all/own/foreign")}, {Key: "a", Desc: N("auto-refresh")}, {Key: "o", Desc: N("sort")}}
	if v.app.Client().Has("debug") {
		h = append(h, Hint{Key: "D", Desc: N("debug")})
	}
	return append(h, Hint{Key: "L", Desc: N("log")})
}

// Refresh reloads the list.
func (v *SessionsView) Refresh() {
	if v.busy {
		return
	}
	v.busy = true
	v.SetLoading(true)
	c := v.app.Client()
	v.app.Async(func(ctx context.Context) func() {
		sess, err := c.Sessions(ctx, v.which)
		return func() {
			v.busy = false
			v.SetLoading(false)
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			rows := make([]Row, 0, len(sess))
			for _, s := range sess {
				if v.proxy != "" && rtpengine.ProxyHost(s.Proxy) != v.proxy {
					continue
				}
				owner, tone := "own", ToneFg
				if s.Foreign {
					owner, tone = "foreign", ToneAccent
				}
				state := T("active")
				if s.Deleted {
					state, tone = T("deleting"), ToneDim
				}
				if s.Rec == "yes" {
					state += ",rec"
				}
				age, created := "", ""
				if !s.Created.IsZero() {
					age = humanAge(time.Since(s.Created))
					created = s.Created.Format("2006-01-02 15:04:05")
				}
				rows = append(rows, Row{Key: s.CallID, Data: s, Tone: tone, Cells: []string{s.CallID, age, created, s.Proxy, owner, s.Keyspace, state}})
			}
			sort.SliceStable(rows, func(i, j int) bool { return rows[i].Cells[2] > rows[j].Cells[2] })
			v.SetRows(rows)
			info := v.app.host.Name + " · " + v.which
			if v.proxy != "" {
				info += " · " + Tf("proxy %s", v.proxy)
			}
			v.SetTitleInfo(info)
		}
	})
}

func (v *SessionsView) terminate() {
	rows := v.Marked()
	if len(rows) == 0 {
		return
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.Key
	}
	text := Tf("Terminate %d call(s) on %s?", len(ids), v.app.host.Name) + "\n\n" + strings.Join(firstN(ids, 5), "\n")
	v.app.Confirm(text, T("Terminate"), func() {
		c := v.app.Client()
		v.app.Async(func(ctx context.Context) func() {
			var failed []string
			for _, id := range ids {
				if out, err := c.Terminate(ctx, id); err != nil || strings.Contains(out, "not found") {
					failed = append(failed, id)
				}
			}
			return func() {
				v.ClearMarks()
				if len(failed) > 0 {
					v.app.Flash().Errf("not terminated: %s", strings.Join(failed, ", "))
				} else {
					v.app.Flash().Infof("terminated %d call(s)", len(ids))
				}
				v.Refresh()
			}
		})
	})
}

func (v *SessionsView) debug() {
	r := v.Selected()
	if r == nil {
		return
	}
	debugCall(v.app, r.Key)
}

func debugCall(app *App, callid string) {
	c := app.Client()
	if err := c.Info().Caps.Check("debug", c.Info().Version); err != nil {
		app.Flash().Err(err)
		return
	}
	app.Modal(T("Per-call debug logging for")+"\n"+callid, []string{N("Cancel"), N("Off"), N("On")}, func(label string) {
		if label == "Cancel" || label == "" {
			return
		}
		app.Async(func(ctx context.Context) func() {
			out, err := c.Debug(ctx, callid, label == "On")
			return func() {
				if err != nil {
					app.Flash().Err(err)
					return
				}
				if label == "On" {
					app.Flash().Infof("%s · L in the call details shows its log", strings.TrimSpace(out))
					return
				}
				app.Flash().Info(strings.TrimSpace(out))
			}
		})
	})
}

func newCallView(app *App, callid string) View {
	v := newTextViewer(app, "call "+trunc(callid, 40), "call")
	c := app.Client()
	v.SetLoading()
	// parsed view by default, y shows the reply as rtpengine printed it
	var raw string
	showRaw := false
	// interfaces name the rtpengine side of streams; they rarely change,
	// so they are read once per view
	var ifOnce sync.Once
	var ifaces []rtpengine.Interface
	var created int64 // the call's start, narrows the log search
	show := func() {
		ci := rtpengine.ParseCallInfo(raw)
		if ci != nil {
			created = ci.Created
		}
		if ci != nil && !showRaw {
			v.SetContent(callText(ci, ifaces), "markup")
			return
		}
		v.SetContent(strings.TrimLeft(raw, "\n"), "call")
	}
	v.Loader = func() {
		app.Async(func(ctx context.Context) func() {
			out, err := c.SessionInfo(ctx, callid)
			var loaded []rtpengine.Interface
			ifOnce.Do(func() {
				if c.Has("list interfaces") {
					if list, err := c.Do(ctx, "list interfaces"); err == nil {
						loaded = rtpengine.ParseInterfaces(list)
					}
				}
			})
			return func() {
				if loaded != nil {
					ifaces = loaded
				}
				if err != nil {
					v.SetError(err)
					return
				}
				raw = out
				show()
			}
		})
	}
	v.Keys['y'] = func() {
		showRaw = !showRaw
		show()
		v.Text().ScrollToBeginning() // the other layout has other lines
	}
	v.CtrlKeys[tcell.KeyCtrlK] = func() {
		app.Confirm(Tf("Terminate call %s on %s?", callid, app.host.Name), T("Terminate"), func() {
			app.Async(func(ctx context.Context) func() {
				out, err := c.Terminate(ctx, callid)
				return func() {
					if err != nil {
						app.Flash().Err(err)
						return
					}
					app.Flash().Info(strings.TrimSpace(out))
					app.Pop()
					if t := app.Top(); t != nil {
						t.Refresh()
					}
				}
			})
		})
	}
	v.Keys['D'] = func() { debugCall(app, callid) }
	v.Keys['L'] = func() { app.Push(newCallLogView(app, callid, created)) }
	v.Extra = []Hint{{Key: "y", Desc: N("raw/parsed")}, {Key: "ctrl-k", Desc: N("terminate")}, {Key: "D", Desc: N("debug")}, {Key: "L", Desc: N("log")}}
	v.Loader()
	return &autoText{v}
}

// autoText is a TextViewer refreshed by the ticker.
type autoText struct{ *TextViewer }

func (a *autoText) AutoRefresh() bool { return true }

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string(nil), s[:n]...), Tf("… and %d more", len(s)-n))
}
