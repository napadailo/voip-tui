package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/internal/opensips/config"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// statsPrefix starts the name of the statistics panel, so that switching
// hosts keeps it open.
const statsPrefix = "stats "

// autoText is a text view refreshed with the ticker.
type autoText struct{ *TextViewer }

func (a *autoText) AutoRefresh() bool { return true }

// newStatsPanel is get_statistics all by category, as the statistics of
// rtpengine-tui: what is there now, and for counters the rate between two
// refreshes and the total. y shows the JSON, s saves it, a opens every
// statistic as a table (x resets one there).
func newStatsPanel(app *App) View {
	h, cl := app.host, app.Client()
	v := newTextViewer(app, statsPrefix+h.Name, "markup")
	v.SetLoading()
	var raw any
	var cur, prev map[string]int64
	var curAt, prevAt time.Time
	showRaw := false
	show := func() {
		switch {
		case showRaw:
			v.SetContent(toJSON(raw), "json")
		case cur != nil:
			v.SetContent(statsCard(h, cl.Info(), cur, prev, curAt.Sub(prevAt)).String(), "markup")
		}
	}
	v.Loader = func() {
		if s := cl.Supports("stats.get"); !s.OK {
			v.SetError(errors.New(s.Reason))
			return
		}
		app.Async(func(ctx context.Context) func() {
			ans, err := cl.Op(ctx, "stats.get", nil)
			return func() {
				if err != nil {
					v.SetError(err)
					return
				}
				prev, prevAt = cur, curAt
				cur, curAt, raw = opensips.StatValues(ans), time.Now(), ans
				show()
			}
		})
	}
	v.Keys['y'] = func() {
		showRaw = !showRaw
		show()
		v.Text().ScrollToBeginning() // the other layout has other lines
	}
	v.Keys['s'] = func() {
		path, err := app.SaveFile("stats "+h.Name, ".json", toJSON(raw))
		if err != nil {
			app.Flash().Err(err)
			return
		}
		app.Flash().Infof("saved %s", path)
	}
	// a opens every statistic over the panel, a there comes back to it
	v.Keys['a'] = func() {
		if s := cl.Supports("stats.get"); !s.OK {
			app.Flash().Warn(s.Reason)
			return
		}
		all := newResourceView(app, opensips.FindResource("allstats"), cl)
		all.Bind('a', app.Pop)
		app.Push(all)
	}
	v.Extra = []Hint{{Key: "y", Desc: N("JSON/parsed")}, {Key: "a", Desc: N("all statistics")}}
	if cl.Info() == nil {
		app.probe(h.Name, v.Loader) // opened before the host answered
	} else {
		v.Loader()
	}
	return &autoText{v}
}

// statKind says how a statistic is shown.
type statKind int

const (
	gauge   statKind = iota // what is there now
	counter                 // grows: rate and total
	bad                     // a counter that should not grow: its rate is a warning
)

type statLine struct {
	label, key string
	kind       statKind
}

// statSections are the categories of the panel; a line shows when the host
// has its statistic, a section when it has any.
var statSections = []struct {
	title  string
	groups []string
	lines  []statLine
}{
	{N("SIP messages"), []string{"core"}, []statLine{
		{N("requests received"), "core:rcv_requests", counter}, {N("replies received"), "core:rcv_replies", counter},
		{N("requests forwarded"), "core:fwd_requests", counter}, {N("replies forwarded"), "core:fwd_replies", counter},
		{N("requests dropped"), "core:drop_requests", bad}, {N("replies dropped"), "core:drop_replies", bad},
		{N("request errors"), "core:err_requests", bad}, {N("reply errors"), "core:err_replies", bad},
		{N("bad URIs"), "core:bad_URIs_rcvd", bad}, {N("unsupported methods"), "core:unsupported_methods", bad},
		{N("bad headers"), "core:bad_msg_hdr", bad}, {N("slow messages"), "core:slow_messages", bad},
	}},
	{N("Transactions"), []string{"tm"}, []statLine{
		{N("in use"), "tm:inuse_transactions", gauge},
		{N("as server (UAS)"), "tm:UAS_transactions", counter}, {N("as client (UAC)"), "tm:UAC_transactions", counter},
		{"2xx", "tm:2xx_transactions", counter}, {"3xx", "tm:3xx_transactions", counter},
		{"4xx", "tm:4xx_transactions", counter}, {"5xx", "tm:5xx_transactions", bad}, {"6xx", "tm:6xx_transactions", counter},
		{N("replies received"), "tm:received_replies", counter}, {N("replies relayed"), "tm:relayed_replies", counter},
		{N("local replies"), "tm:local_replies", counter}, {N("no final reply in time"), "tm:timeout_finalresponse", bad},
	}},
	{N("Stateless replies"), []string{"sl"}, []statLine{
		{"1xx", "sl:1xx_replies", counter}, {"200", "sl:200_replies", counter}, {"2xx", "sl:2xx_replies", counter},
		{"3xx", "sl:3xx_replies", counter}, {"4xx", "sl:4xx_replies", counter}, {"5xx", "sl:5xx_replies", bad},
		{"6xx", "sl:6xx_replies", counter}, {N("sent"), "sl:sent_replies", counter}, {N("send errors"), "sl:sent_err_replies", bad},
	}},
	{N("Dialogs"), []string{"dialog"}, []statLine{
		{N("active"), "dialog:active_dialogs", gauge}, {N("early"), "dialog:early_dialogs", gauge},
		{N("processed"), "dialog:processed_dialogs", counter}, {N("expired"), "dialog:expired_dialogs", bad},
		{N("failed"), "dialog:failed_dialogs", bad},
	}},
	{N("Registrations"), []string{"usrloc", "registrar"}, []statLine{
		{N("registered AORs"), "usrloc:registered_users", gauge}, {N("users in location"), "usrloc:location-users", gauge},
		{N("contacts in location"), "usrloc:location-contacts", gauge}, {N("expired contacts"), "usrloc:location-expires", counter},
		{N("accepted"), "registrar:accepted_regs", counter}, {N("rejected"), "registrar:rejected_regs", bad},
	}},
	{N("Network"), []string{"net"}, []statLine{
		{N("waiting UDP"), "net:waiting_udp", gauge}, {N("waiting TCP"), "net:waiting_tcp", gauge}, {N("waiting TLS"), "net:waiting_tls", gauge},
	}},
}

// statsCard lays out the statistics by category; a group without a section
// of its own gets one with its statistics under their OpenSIPS names.
func statsCard(h *config.Host, i *opensips.HostInfo, cur, prev map[string]int64, dt time.Duration) *tui.Card {
	sub := []string{h.Name}
	if i != nil {
		sub = append(sub, i.Running.String())
		if up := i.UptimeNow(); up >= 0 {
			sub = append(sub, Tf("up %s", opensips.Duration(up)))
		}
	}
	c := tui.NewCard(T("Statistics")).Sub(sub...)
	// rate is the growth per second between two refreshes, "" when there is
	// nothing to compare with or the statistic went down (a gauge, a reset)
	rate := func(key string) (string, bool) {
		if prev == nil || dt <= 0 {
			return "", false
		}
		p, ok := prev[key]
		if !ok || cur[key] < p {
			return "", false
		}
		r := float64(cur[key]-p) / dt.Seconds()
		return strconv.FormatFloat(r, 'f', 1, 64), r > 0
	}
	counterLine := func(label string, key string, kind statKind, raw bool) {
		r, growing := rate(key)
		if r == "" {
			r = "-"
		}
		cell := fmt.Sprintf("%12s", r)
		if kind == bad && growing {
			cell = tui.Paint(theme.Warn, cell)
		}
		value := cell + fmt.Sprintf("%16s", groupDigits(cur[key]))
		if raw {
			c.KVRaw(label, value)
		} else {
			c.KV(label, value)
		}
	}
	header := func(total string) {
		c.Line(tui.Paint(theme.Header, padLeft(T("PER SECOND"), 12)+padLeft(T(total), 16)))
	}
	used := map[string]bool{} // statistics some section shows
	shown := map[string]bool{"shmem": true, "pkmem": true, "load": true}
	for _, sec := range statSections {
		for _, g := range sec.groups {
			shown[g] = true
		}
		var gauges, counters []statLine
		for _, l := range sec.lines {
			if _, ok := cur[l.key]; !ok {
				continue
			}
			used[l.key] = true
			if l.kind == gauge {
				gauges = append(gauges, l)
			} else {
				counters = append(counters, l)
			}
		}
		if len(gauges)+len(counters) == 0 {
			continue
		}
		c.Section(sec.title)
		for _, l := range gauges {
			c.KV(l.label, tui.Strong(groupDigits(cur[l.key])))
		}
		if len(counters) > 0 {
			header(N("TOTAL"))
		}
		for _, l := range counters {
			counterLine(l.label, l.key, l.kind, false)
		}
	}

	// the rest of a group: its statistics no line above shows
	rest := func(group string) []string {
		var keys []string
		for k := range cur {
			if g, _, _ := strings.Cut(k, ":"); g == group && !used[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		return keys
	}
	rawLines := func(keys []string) {
		if len(keys) == 0 {
			return
		}
		header(N("VALUE"))
		for _, k := range keys {
			_, name, _ := strings.Cut(k, ":")
			counterLine(name, k, counter, true)
		}
	}
	if memorySection(c, cur, used) {
		rawLines(rest("shmem"))
	}
	pkmemSection(c, cur)
	if loadSection(c, cur, used) {
		rawLines(rest("load"))
	}

	// groups of modules without a section of their own
	other := map[string]bool{}
	for k := range cur {
		if g, _, _ := strings.Cut(k, ":"); !shown[g] {
			other[g] = true
		}
	}
	var groups []string
	for g := range other {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		c.Section(g, T("statistics of the module"))
		rawLines(rest(g))
	}
	return c
}

// usedOf colors used of total: yellow over 70%, red over 85%.
func usedOf(n, total int64) string {
	p := float64(n) * 100 / float64(total)
	s := fmt.Sprintf("%s / %s (%.0f%%)", opensips.Size(n), opensips.Size(total), p)
	switch {
	case p > 85:
		return tui.Paint(theme.Err, s)
	case p > 70:
		return tui.Paint(theme.Warn, s)
	}
	return tview.Escape(s)
}

// memorySection is shared memory; it reports whether there was any.
func memorySection(c *tui.Card, cur map[string]int64, used map[string]bool) bool {
	total := cur["shmem:total_size"]
	if total <= 0 {
		return false
	}
	used["shmem:total_size"] = true
	c.Section(N("Shared memory"))
	for _, l := range []struct{ label, key string }{{N("used"), "shmem:real_used_size"}, {N("peak"), "shmem:max_used_size"}} {
		if n, ok := cur[l.key]; ok {
			c.KV(l.label, usedOf(n, total))
			used[l.key] = true
		}
	}
	if n, ok := cur["shmem:fragments"]; ok {
		c.KV(N("fragments"), groupDigits(n))
		used["shmem:fragments"] = true
	}
	return true
}

// pkmemSection is the private memory of every process: pkmem:<process>-<name>.
func pkmemSection(c *tui.Card, cur map[string]int64) {
	type proc struct{ used, total int64 }
	procs := map[string]*proc{}
	for k, v := range cur {
		name, ok := strings.CutPrefix(k, "pkmem:")
		if !ok {
			continue
		}
		id, stat, ok := strings.Cut(name, "-")
		if !ok {
			continue
		}
		p := procs[id]
		if p == nil {
			p = &proc{}
			procs[id] = p
		}
		switch stat {
		case "real_used_size":
			p.used = v
		case "total_size":
			p.total = v
		}
	}
	var ids []string
	for id, p := range procs {
		if p.total > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	sort.Slice(ids, func(a, b int) bool {
		x, _ := strconv.Atoi(ids[a])
		y, _ := strconv.Atoi(ids[b])
		return x < y
	})
	c.Section(N("Private memory (pkmem)"), T("per process, as in :processes"))
	for _, id := range ids {
		c.KVRaw(T("process")+" "+id, usedOf(procs[id].used, procs[id].total))
	}
}

// loadSection is the load of the worker processes; it reports whether
// there was any.
func loadSection(c *tui.Card, cur map[string]int64, used map[string]bool) bool {
	var lines [][2]string
	for _, l := range [][2]string{{N("now"), "load:load"}, {N("last minute"), "load:load1m"}, {N("last 10 minutes"), "load:load10m"}} {
		if n, ok := cur[l[1]]; ok {
			lines = append(lines, [2]string{l[0], strconv.FormatInt(n, 10) + "%"})
			used[l[1]] = true
		}
	}
	if len(lines) == 0 {
		return false
	}
	c.Section(N("Load"), T("busy share of the worker processes"))
	for _, l := range lines {
		c.KV(l[0], l[1])
	}
	return true
}

// groupDigits writes 1234567 as 1 234 567.
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(ch)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func padLeft(s string, n int) string {
	if w := tui.StrWidth(s); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}
