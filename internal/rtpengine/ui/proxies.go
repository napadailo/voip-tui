package ui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/napadailo/voip-tui/internal/rtpengine"
)

// ProxiesView groups the sessions of the host by signalling proxy (the
// address NG commands came from) and, from jsonstats (mr8.5+), adds what
// each proxy asked over NG since the start; enter lists the calls of one
// proxy.
type ProxiesView struct {
	*TableView
	app  *App
	busy bool
}

func newProxiesView(app *App) *ProxiesView {
	v := &ProxiesView{TableView: NewTableView(app, N("Proxies"), []string{N("PROXY"), N("CALLS"), N("SHARE"), N("OWN"), N("FOREIGN"), N("DELETING"), N("SOCKETS"), N("LAST CALL"), N("OLDEST CALL"),
		N("NG"), N("OFFER"), N("ANSWER"), N("DELETE"), N("NG ERR"), N("OFFER AVG")}), app: app}
	v.OnEnter(func(r *Row) { app.Push(newProxySessionsView(app, r.Key)) })
	app.ensureProbed(v.Refresh)
	return v
}

func (v *ProxiesView) Name() string      { return "proxies" }
func (v *ProxiesView) Table() *TableView { return v.TableView }
func (v *ProxiesView) Close()            {}
func (v *ProxiesView) AutoRefresh() bool { return true }
func (v *ProxiesView) Hints() []Hint {
	return []Hint{{Key: "enter", Desc: N("calls of the proxy")}, {Key: "o", Desc: N("sort")}}
}

// Refresh reloads the sessions and the NG statistics and regroups them.
func (v *ProxiesView) Refresh() {
	if v.busy {
		return
	}
	v.busy = true
	v.SetLoading(true)
	c := v.app.Client()
	v.app.Async(func(ctx context.Context) func() {
		sess, err := c.Sessions(ctx, "all")
		var ng map[string]*rtpengine.NGStat
		var ngErr error
		if err == nil && c.Has("list jsonstats") {
			var st rtpengine.Stats
			if st, _, ngErr = c.JSONStats(ctx); ngErr == nil {
				ng = st.NGByProxy()
			}
		}
		return func() {
			v.busy = false
			v.SetLoading(false)
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			if ngErr != nil {
				v.app.Flash().Err(ngErr)
			}
			stats := rtpengine.GroupByProxy(sess)
			for host := range ng { // proxies that talk NG but have no calls now
				found := false
				for _, p := range stats {
					found = found || p.Proxy == host
				}
				if !found {
					stats = append(stats, rtpengine.ProxyStat{Proxy: host})
				}
			}
			rows := make([]Row, 0, len(stats))
			for _, p := range stats {
				name := p.Proxy
				if name == "" {
					name = T("(unknown)")
				}
				last, oldest, share := "", "", ""
				if !p.Newest.IsZero() {
					last = Tf("%s ago", humanAge(time.Since(p.Newest)))
					oldest = humanAge(time.Since(p.Oldest))
				}
				if len(sess) > 0 {
					share = fmt.Sprintf("%.1f%%", 100*float64(p.Calls)/float64(len(sess)))
				}
				r := Row{Key: p.Proxy, Data: p, Cells: []string{name, strconv.Itoa(p.Calls), share,
					strconv.Itoa(p.Own), strconv.Itoa(p.Foreign), strconv.Itoa(p.Deleting), strconv.Itoa(p.Ports), last, oldest,
					"", "", "", "", "", ""}}
				if n := ng[p.Proxy]; n != nil {
					avg := ""
					if d, ok := n.Avg("offer"); ok {
						avg = fmt.Sprintf("%.2f ms", float64(d)/float64(time.Millisecond))
					}
					copy(r.Cells[9:], []string{groupDigits(strconv.FormatUint(n.Total(), 10)), groupDigits(strconv.FormatUint(n.Counts["offer"], 10)),
						groupDigits(strconv.FormatUint(n.Counts["answer"], 10)), groupDigits(strconv.FormatUint(n.Counts["delete"], 10)),
						strconv.FormatUint(n.Errors, 10), avg})
					if n.Errors > 0 {
						r.CellTones = map[int]Tone{13: ToneWarn}
					}
				}
				if p.Own == 0 && p.Calls > 0 {
					r.Tone = ToneAccent // all its calls are owned by another node
				}
				rows = append(rows, r)
			}
			v.SetRows(rows)
			info := v.app.host.Name + " · " + Tf("%d calls from %d proxies", len(sess), len(stats))
			if ng != nil {
				info += " · " + T("NG: commands since the start")
			}
			v.SetTitleInfo(info)
		}
	})
}
