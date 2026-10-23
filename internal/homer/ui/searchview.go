package ui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

type colSpec struct {
	title string
	get   func(m map[string]any) string
}

// SearchView lists search results of one profile.
type SearchView struct {
	*TableView
	app      *App
	spec     homer.SearchSpec
	from, to time.Time
	auto     bool
	stop     chan struct{}
	cols     []colSpec
	busy     bool
	again    bool // the search changed while one ran: run it once that ends
	grouped  bool
	items    []map[string]any
	// the window is read from its end back, a part at a time, as the
	// table scrolls down: homer-app gives the messages in no order, so a
	// part is the latest stretch before those read that a limit holds, of
	// whole sessions, each in the part of its latest activity
	next    pageStart // where the next part ends
	more    bool      // the window goes on further back
	gen     int       // the search a part belongs to
	filling bool      // a fill is queued after a draw
}

type pageStart struct {
	to    time.Time       // the page ends here; zero is the end of the window
	span  time.Duration   // the stretch the page before took, where this one begins to look
	shown map[string]bool // the sessions of the pages before
	seen  map[string]bool // dbnode/id of the rows with no session at to the page before had
}

func newSearchView(app *App, spec homer.SearchSpec) *SearchView {
	v := &SearchView{app: app, spec: spec, grouped: strings.HasPrefix(spec.Key, "1_")}
	v.cols = columnsFor(spec.Key, app)
	v.TableView = NewTableView(app, v.title(), nil)
	v.applyHeaders()
	v.SetSort(0, false) // START and DATE: the latest first
	v.OnEnter(func(*Row) { v.openCall() })
	v.Bind('d', v.describe)
	v.Bind('y', v.json)
	v.Bind('p', func() { exportRows(app, v.refs(), "pcap") })
	v.Bind('t', func() { exportRows(app, v.refs(), "text") })
	v.Bind('s', v.openForm)
	v.Bind('e', v.editQuery)
	v.Bind('r', v.Refresh)
	v.Bind('a', v.toggleAuto)
	v.Bind('v', v.toggleGrouped)
	v.Bind('[', func() { v.shift(-1) })
	v.Bind(']', func() { v.shift(1) })
	v.OnSelect(func(*Row) { // near the end, the next part
		if v.SelectedIndex() >= len(v.Rows())-3 {
			v.loadMore(false)
		}
	})
	v.Refresh()
	return v
}

func (v *SearchView) title() string {
	if m := v.app.Mapping(v.spec.Key); m != nil {
		return fmt.Sprintf("%s(%s)", m.Title(), v.spec.Key)
	}
	return v.spec.Key
}

func (v *SearchView) Name() string      { return v.spec.Key }
func (v *SearchView) Table() *TableView { return v.TableView }

func (v *SearchView) Hints() []Hint {
	view := N("view: messages")
	if v.grouped {
		view = N("view: sessions")
	}
	auto := N("auto-refresh off")
	if v.auto {
		auto = N("auto-refresh on")
	}
	h := []Hint{
		{Key: "enter", Desc: N("open call")}, {Key: "space", Desc: N("mark")}, {Key: "v", Desc: view}, {Key: "d", Desc: N("describe")}, {Key: "y", Desc: "JSON"}, {Key: "p", Desc: "pcap"}, {Key: "t", Desc: N("text export")},
		{Key: "s", Desc: N("search form")}, {Key: "e", Desc: N("edit query")}, {Key: "r", Desc: N("refresh")}, {Key: "a", Desc: auto},
		{Key: "[/]", Desc: N("shift window")}, {Key: "o/O", Desc: N("sort/order")},
	}
	return h
}

func (v *SearchView) Close() {
	if v.stop != nil {
		close(v.stop)
		v.stop = nil
	}
}

// Refresh re-runs the query (relative ranges move to now).
func (v *SearchView) Refresh() {
	if v.busy {
		v.again = true
		return
	}
	v.busy = true
	v.gen++
	v.SetLoading(true)
	spec := v.spec
	m := v.app.Mapping(spec.Key)
	nodes := v.app.NodeValues()
	v.app.Async(func(ctx context.Context) func() {
		bq, unknown := homer.BuildQuery(spec, m, nodes, time.Now())
		items, next, more, err := loadPage(ctx, v.app.client, pageQuery{Query: bq})
		return func() {
			if v.ended() {
				return
			}
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			v.from, v.to = bq.From, bq.To
			v.items, v.next, v.more = items, next, more
			v.renderRows()
			v.showInfo()
			if len(unknown) > 0 {
				v.app.Flash().Warnf("unknown fields ignored: %s (see :mappings)", strings.Join(unknown, ", "))
			} else {
				v.app.Flash().Infof("%d rows", len(v.Rows()))
			}
			v.fill()
		}
	})
}

// loadMore reads the next part of the window, back from where the parts
// read end, and adds its rows to the table; quiet, as it fills the screen,
// it leaves the status line to the search.
func (v *SearchView) loadMore(quiet bool) {
	if v.busy || !v.more {
		return
	}
	v.busy = true
	v.SetLoading(true)
	gen, start := v.gen, v.next
	bq, _ := homer.BuildQuery(v.spec, v.app.Mapping(v.spec.Key), v.app.NodeValues(), time.Now())
	bq.From, bq.To = v.from, v.to // the window of the first part, a relative one included
	v.app.Async(func(ctx context.Context) func() {
		items, next, more, err := loadPage(ctx, v.app.client, pageQuery{Query: bq, pageStart: start})
		return func() {
			if v.ended() || gen != v.gen {
				return
			}
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			before := len(v.Rows())
			v.items, v.next, v.more = mergeRows(v.items, items), next, more
			v.renderRows()
			v.showInfo()
			if !quiet {
				v.app.Flash().Infof("%d rows, %d more", len(v.Rows()), len(v.Rows())-before)
			}
			v.fill()
		}
	})
}

// ended ends a load: it runs the search again when it changed meanwhile,
// and tells whether it did.
func (v *SearchView) ended() bool {
	v.busy = false
	if v.again { // what came is of the search before
		v.again = false
		v.Refresh()
		return true
	}
	v.SetLoading(false)
	return false
}

// fill reads on while the rows do not fill the table, that there is
// something to scroll.
func (v *SearchView) fill() {
	if _, _, _, h := v.GetInnerRect(); v.more && len(v.Rows()) < h {
		v.loadMore(true)
	}
}

// Draw draws the table, and reads on when the rows do not fill it: the
// table may grow after the rows came, at first or as the terminal does.
func (v *SearchView) Draw(screen tcell.Screen) {
	v.TableView.Draw(screen)
	if _, _, _, h := v.GetInnerRect(); v.more && !v.busy && !v.filling && len(v.Rows()) < h {
		v.filling = true
		go v.app.QueueUpdateDraw(func() {
			v.filling = false
			v.fill()
		})
	}
}

// showInfo says the window and the search in the title, and how far back
// the rows go while there is more.
func (v *SearchView) showInfo() {
	info := v.spec.RangeLabel(v.app.loc)
	if len(v.spec.Filters) > 0 || v.spec.Smart != "" {
		info += " · " + v.spec.String()
	}
	if v.more {
		info += " · " + Tf("back to %s, more below", v.next.to.In(v.app.loc).Format("15:04:05"))
	}
	v.SetTitleInfo(info)
}

// loadPage reads a page: the latest stretch of the window before q.to
// whose messages a limit holds, and the sessions with messages there that
// no later page has, each with all its messages in the window; and where
// the next, earlier page ends. So a session is on the page of its latest
// activity. Its messages outside the stretch come by its session id;
// homer-app gives them only when they match the search too. A row with no
// session id is a session of its own. A page of no sessions of its own is
// skipped.
func loadPage(ctx context.Context, c *homer.Client, q pageQuery) ([]map[string]any, pageStart, bool, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	shown := map[string]bool{}
	for sid := range q.shown {
		shown[sid] = true
	}
	end, span, seen := q.to, q.span, q.seen
	if end.IsZero() {
		end = q.To
	}
	if span <= 0 {
		span = end.Sub(q.From)
	}
	for {
		rows, from, took, err := lastStretch(ctx, c, q.Query, end, span, limit)
		if err != nil {
			return nil, pageStart{}, false, err
		}
		next := pageStart{to: from, span: took, shown: shown, seen: map[string]bool{}}
		var items []map[string]any
		var sids []string
		bySid := map[string][]map[string]any{}
		for _, m := range rows {
			sid := str(m, "sid")
			if sid == "" {
				key := str(m, "dbnode") + "/" + str(m, "id")
				if !seen[key] {
					items = append(items, m)
				}
				if itemTime(m).Truncate(time.Millisecond).Equal(from) {
					next.seen[key] = true
				}
				continue
			}
			if shown[sid] {
				continue
			}
			if _, ok := bySid[sid]; !ok {
				sids = append(sids, sid)
			}
			bySid[sid] = append(bySid[sid], m)
		}
		if len(sids) > 0 {
			rest, err := sessionRest(ctx, c, q.Query, sids, limit)
			if err != nil {
				return nil, pageStart{}, false, err
			}
			whole := map[string][]map[string]any{}
			for _, m := range rest {
				whole[str(m, "sid")] = append(whole[str(m, "sid")], m)
			}
			for _, sid := range sids {
				msgs := bySid[sid]
				if w := whole[sid]; len(w) > 0 {
					msgs = mergeRows(msgs, w)
				}
				shown[sid] = true
				items = append(items, msgs...)
			}
		}
		sort.SliceStable(items, func(i, j int) bool { return itemTime(items[i]).Before(itemTime(items[j])) })
		more := from.After(q.From)
		if len(items) > 0 || !more {
			return items, next, more, nil
		}
		end, span, seen = from, took, next.seen
	}
}

// lastStretch finds the latest stretch of the window from q.From to end
// that a limit holds, and at least half fills it where the window has as
// much: it looks for the length by halves between one the limit holds and
// one it does not, from span, the length of the page before. It gives the
// messages, where the stretch begins, and its length.
func lastStretch(ctx context.Context, c *homer.Client, q homer.Query, end time.Time, span time.Duration, limit int) ([]map[string]any, time.Time, time.Duration, error) {
	var best, last []map[string]any
	var bestFrom, lastFrom time.Time
	var fits, over time.Duration // the longest span the limit holds, the shortest it does not
	for tries := 0; tries < 16; tries++ {
		from := end.Add(-span).Truncate(time.Millisecond)
		if from.Before(q.From) {
			from = q.From
		}
		w := q
		w.From, w.To = from, end
		res, err := c.Search(ctx, w)
		if err != nil {
			return nil, time.Time{}, 0, err
		}
		last, lastFrom = res.Data, from
		if len(res.Data) >= limit {
			over = span
		} else {
			fits, best, bestFrom = span, res.Data, from
			if len(best) >= limit/2 || !from.After(q.From) {
				break
			}
		}
		switch {
		case over == 0:
			span *= 2
		case fits == 0 && span > time.Millisecond:
			span /= 2
		case fits == 0 || over-fits <= time.Millisecond:
			tries = 16 // as close as a millisecond: so be it
		default:
			span = fits + (over-fits)/2
		}
	}
	if best == nil { // a limit in one millisecond: as it is
		return last, lastFrom, span, nil
	}
	return best, bestFrom, fits, nil
}

// pageQuery is the query of the window and where the page ends.
type pageQuery struct {
	homer.Query
	pageStart
}

// sessionRest reads the messages of the sessions in the window, a limit at
// a time. With OR between the conditions a session
// id is one more alternative, so then it goes alone.
func sessionRest(ctx context.Context, c *homer.Client, q homer.Query, sids []string, limit int) ([]map[string]any, error) {
	cond := homer.Condition{Name: "sid", Value: strings.Join(sids, ";"), Type: "string", HepID: q.HepID}
	if q.OrLogic {
		q.Conditions, q.Smart, q.OrLogic = nil, "", false
	}
	q.Conditions = append(slices.Clip(q.Conditions), cond)
	var out []map[string]any
	for {
		res, err := c.Search(ctx, q)
		if err != nil {
			return nil, err
		}
		out = mergeRows(out, res.Data)
		if len(res.Data) < limit {
			return out, nil
		}
		last := q.From
		for _, m := range res.Data {
			if t := itemTime(m).Truncate(time.Millisecond); t.After(last) {
				last = t
			}
		}
		if !last.After(q.From) {
			last = q.From.Add(time.Millisecond)
		}
		q.From = last
	}
}

// mergeRows adds the rows of more that rows lacks, by node and id.
func mergeRows(rows, more []map[string]any) []map[string]any {
	have := map[string]bool{}
	for _, m := range rows {
		have[str(m, "dbnode")+"/"+str(m, "id")] = true
	}
	for _, m := range more {
		if k := str(m, "dbnode") + "/" + str(m, "id"); !have[k] {
			have[k] = true
			rows = append(rows, m)
		}
	}
	return rows
}

func (v *SearchView) applyHeaders() {
	var headers []string
	if v.grouped {
		headers = []string{N("START"), N("STATUS"), N("FROM"), N("TO"), "RURI", N("DURATION"), N("MSGS"), N("SOURCE"), N("DESTINATION"), "USER AGENT", N("SESSION ID"), N("NODE")}
	} else {
		for _, c := range v.cols {
			headers = append(headers, c.title)
		}
	}
	v.SetHeaders(headers)
}

func (v *SearchView) toggleGrouped() {
	if !strings.HasPrefix(v.spec.Key, "1_") {
		v.app.Flash().Info(T("session view is available for SIP profiles"))
		return
	}
	v.grouped = !v.grouped
	v.ClearMarks()
	v.applyHeaders()
	v.renderRows()
	v.app.RefreshHints()
}

func (v *SearchView) renderRows() {
	var rows []Row
	if v.grouped {
		for _, g := range groupSessions(v.items) {
			rows = append(rows, g.row(v.app.loc))
		}
	} else {
		rows = make([]Row, 0, len(v.items))
		for _, item := range v.items {
			rows = append(rows, rowFromItem(item, v.cols))
		}
	}
	v.SetRows(rows)
}

// sessionGroup is all found messages of one Call-ID.
type sessionGroup struct {
	sid   string
	items []map[string]any
}

func groupSessions(items []map[string]any) []*sessionGroup {
	var order []*sessionGroup
	idx := map[string]*sessionGroup{}
	for _, it := range items {
		sid := str(it, "sid")
		g, ok := idx[sid]
		if !ok {
			g = &sessionGroup{sid: sid}
			idx[sid] = g
			order = append(order, g)
		}
		g.items = append(g.items, it)
	}
	for _, g := range order {
		sort.SliceStable(g.items, func(i, j int) bool { return itemTime(g.items[i]).Before(itemTime(g.items[j])) })
	}
	return order
}

var reasons = map[string]string{
	"200": "OK", "202": "Accepted", "301": "Moved", "302": "Moved", "401": "Unauthorized", "403": "Forbidden",
	"404": "Not Found", "407": "Proxy Auth", "408": "Timeout", "480": "Unavailable", "481": "No Dialog",
	"484": "Incomplete", "486": "Busy", "487": "Cancelled", "488": "Not Acceptable", "500": "Server Error",
	"502": "Bad Gateway", "503": "Unavailable", "504": "Timeout", "603": "Decline",
}

func isCode(m string) bool { return len(m) == 3 && m[0] >= '1' && m[0] <= '6' }

func (g *sessionGroup) row(loc *time.Location) Row {
	first := g.items[0]
	req := first
	for _, it := range g.items {
		if m := str(it, "method"); m == "INVITE" || m == "REGISTER" || (!isCode(m) && m != "") {
			req = it
			break
		}
	}
	// the outcome is the final answer to the request of the session, the
	// first for a call, the last for a registration that renews itself;
	// a 401 or 407 asks for credentials and the request comes again, so it
	// is the outcome only when nothing else answered. Answers to other
	// requests, BYE or PRACK, do not count.
	method := str(req, "method")
	register := method == "REGISTER"
	status, challenge, provisional := "", "", ""
	var answered, bye time.Time
	for _, it := range g.items {
		m := str(it, "method")
		if m == "BYE" && bye.IsZero() && !answered.IsZero() {
			bye = itemTime(it)
		}
		if !isCode(m) {
			continue
		}
		if _, of := cseqOf(it); of != "" && of != method {
			continue
		}
		switch {
		case m[0] == '1':
			provisional = m
		case m == "401" || m == "407":
			challenge = m
		case status == "" || register:
			status = m
			if m[0] == '2' {
				answered = itemTime(it)
			}
		}
	}
	if status == "" {
		status = challenge
	}
	color := theme.Fg
	label := status
	switch {
	case status == "" && provisional != "":
		label, color = Tf("%s (no final)", provisional), tcell.ColorAqua
	case status == "":
		label, color = T("no answer"), tcell.ColorAqua
	case status == "487":
		color = theme.Warn
	default:
		color = methodColor(status)
	}
	if r, ok := reasons[status]; ok {
		label += " " + r
	}
	dur := ""
	switch {
	case !answered.IsZero() && !bye.IsZero():
		dur = bye.Sub(answered).Round(time.Second).String()
	case !answered.IsZero() && !register:
		dur = T("active?")
	}
	return Row{
		Cells: []string{fmtTime(itemTime(first), loc), label, str(req, "from_user"), str(req, "to_user"), str(req, "ruri_user"),
			dur, fmt.Sprint(len(g.items)), endpoint(req, "aliasSrc", "srcIp", "srcPort"), endpoint(req, "aliasDst", "dstIp", "dstPort"),
			str(req, "user_agent"), g.sid, str(req, "dbnode")},
		Color: color,
		Data:  g,
		Key:   g.sid,
	}
}

func rowFromItem(item map[string]any, cols []colSpec) Row {
	cells := make([]string, len(cols))
	for i, c := range cols {
		cells[i] = c.get(item)
	}
	method := str(item, "method")
	return Row{
		Cells: cells,
		Color: methodColor(method),
		Data:  item,
		Key:   str(item, "dbnode") + "/" + str(item, "id"),
	}
}

func (v *SearchView) toggleAuto() {
	v.auto = !v.auto
	if v.auto {
		if !v.spec.Relative() {
			v.app.Flash().Warn(T("auto-refresh needs a relative range (since=…)"))
			v.auto = false
			return
		}
		v.stop = make(chan struct{})
		stop := v.stop
		every := time.Duration(v.app.cfg.RefreshSeconds) * time.Second
		go func() {
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					v.app.QueueUpdateDraw(v.Refresh)
				}
			}
		}()
		v.app.Flash().Infof("auto-refresh every %s", every)
	} else if v.stop != nil {
		close(v.stop)
		v.stop = nil
		v.app.Flash().Info(T("auto-refresh off"))
	}
	v.app.RefreshHints()
}

// shift moves the time window by its own length.
func (v *SearchView) shift(dir int) {
	from, to := v.from, v.to
	if from.IsZero() {
		from, to = v.spec.Range(time.Now())
	}
	d := to.Sub(from)
	from, to = from.Add(time.Duration(dir)*d), to.Add(time.Duration(dir)*d)
	if to.After(time.Now()) {
		to = time.Now()
		from = to.Add(-d)
	}
	v.spec.From, v.spec.To = from, to
	if v.auto {
		v.toggleAuto()
	}
	v.Refresh()
}

// describe opens the card of the row: a message, or a session with its
// messages.
func (v *SearchView) describe() {
	r := v.Selected()
	if r == nil {
		return
	}
	switch d := r.Data.(type) {
	case map[string]any:
		v.app.Push(newCardView(v.app, "describe "+str(d, "sid"), func() *tui.Card { return messageCard(d, v.app.loc) }, func() any { return d }))
	case *sessionGroup:
		v.app.Push(newCardView(v.app, "describe "+d.sid, func() *tui.Card { return sessionCard(d, v.app.loc) }, func() any { return d.items }))
	}
}

// json opens the row as Homer returned it; y again closes it.
func (v *SearchView) json() {
	r := v.Selected()
	if r == nil {
		return
	}
	switch d := r.Data.(type) {
	case map[string]any:
		pushJSON(v.app, "json "+str(d, "sid"), d)
	case *sessionGroup:
		pushJSON(v.app, "json "+d.sid, d.items)
	}
}

func (v *SearchView) refs() *homer.CallRef {
	rows := v.Marked()
	if len(rows) == 0 {
		return nil
	}
	return callRefFromRows(v.spec.Key, rows, v.from, v.to, v.app.NodeValues())
}

func callRefFromRows(key string, rows []*Row, from, to time.Time, nodes []string) *homer.CallRef {
	ref := &homer.CallRef{Key: key, Nodes: nodes}
	seen := map[string]bool{}
	var minT, maxT time.Time
	var items []map[string]any
	for _, r := range rows {
		switch d := r.Data.(type) {
		case map[string]any:
			items = append(items, d)
		case *sessionGroup:
			items = append(items, d.items...)
		}
	}
	for _, item := range items {
		if sid := str(item, "sid"); sid != "" && !seen[sid] {
			seen[sid] = true
			ref.CallIDs = append(ref.CallIDs, sid)
		}
		if id, ok := item["id"].(float64); ok {
			ref.IDs = append(ref.IDs, int64(id))
		}
		if t := itemTime(item); !t.IsZero() {
			if minT.IsZero() || t.Before(minT) {
				minT = t
			}
			if t.After(maxT) {
				maxT = t
			}
		}
	}
	ref.From, ref.To = transactionWindow(from, to, minT, maxT)
	return ref
}

// transactionWindow widens the search window so the whole dialog is captured.
func transactionWindow(from, to, minT, maxT time.Time) (time.Time, time.Time) {
	if from.IsZero() || (!minT.IsZero() && minT.Before(from)) {
		from = minT
	}
	if to.IsZero() || maxT.After(to) {
		to = maxT
	}
	from = from.Add(-5 * time.Minute)
	to = to.Add(time.Hour)
	if now := time.Now(); to.After(now) {
		to = now
	}
	return from, to
}

func (v *SearchView) openCall() {
	ref := v.refs()
	if ref == nil {
		return
	}
	v.app.Push(newCallView(v.app, *ref)) // homer-app assembles it by the profile's mapping
}

func (v *SearchView) openForm() {
	v.app.Push(newSearchForm(v.app, v.spec, func(spec homer.SearchSpec) {
		v.app.Pop()
		if spec.Key != v.spec.Key {
			v.app.Replace(newSearchView(v.app, spec))
			return
		}
		v.spec = spec
		v.Refresh()
	}))
}

func (v *SearchView) editQuery() {
	cmd := "search " + v.spec.Key
	switch v.spec.Key {
	case "1_call":
		cmd = "calls"
	case "1_registration":
		cmd = "reg"
	case "1_default":
		cmd = "sip"
	case "100_default":
		cmd = "logs"
	}
	v.app.Prompt(cmd + " " + v.spec.String())
}

func columnsFor(key string, app *App) []colSpec {
	date := colSpec{N("DATE"), func(m map[string]any) string { return fmtTime(itemTime(m), app.loc) }}
	src := colSpec{N("SOURCE"), func(m map[string]any) string { return endpoint(m, "aliasSrc", "srcIp", "srcPort") }}
	dst := colSpec{N("DESTINATION"), func(m map[string]any) string { return endpoint(m, "aliasDst", "dstIp", "dstPort") }}
	sid := colSpec{N("SESSION ID"), func(m map[string]any) string { return str(m, "sid") }}
	node := colSpec{N("NODE"), func(m map[string]any) string { return str(m, "dbnode") }}
	field := func(title, k string) colSpec {
		return colSpec{title, func(m map[string]any) string { return str(m, k) }}
	}
	hepid := key
	if i := strings.Index(key, "_"); i > 0 {
		hepid = key[:i]
	}
	switch hepid {
	case "1":
		return []colSpec{date, field(N("METHOD"), "method"), field(N("FROM"), "from_user"), field(N("TO"), "to_user"),
			field("RURI", "ruri_user"), src, dst, sid, field("USER AGENT", "user_agent"),
			colSpec{N("PROTO"), func(m map[string]any) string { return protoName(m) }}, node}
	case "100":
		return []colSpec{date, src, dst, sid, colSpec{N("MESSAGE"), func(m map[string]any) string {
			s := strings.Join(strings.Fields(str(m, "raw")), " ")
			if len(s) > 160 {
				s = s[:160] + "…"
			}
			return s
		}}, node}
	}
	// generic: well-known columns plus a few mapping fields
	cols := []colSpec{date, src, dst, sid}
	if m := app.Mapping(key); m != nil {
		n := 0
		for _, f := range m.Fields {
			k := f.ID
			if i := strings.LastIndex(k, "."); i >= 0 {
				k = k[i+1:]
			}
			if k == "sid" || k == "callid" || n >= 5 {
				continue
			}
			cols = append(cols, field(strings.ToUpper(f.Name), k))
			n++
		}
	}
	return append(cols, node)
}

func itemTime(m map[string]any) time.Time {
	if ts, ok := m["timeSeconds"].(float64); ok && ts > 0 {
		us, _ := m["timeUseconds"].(float64)
		return time.Unix(int64(ts), int64(us)*1000)
	}
	switch v := m["create_date"].(type) {
	case float64:
		return time.UnixMilli(int64(v))
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
	}
	if v, ok := m["micro_ts"].(float64); ok {
		return epochTime(int64(v))
	}
	return time.Time{}
}

// epochTime reads a Unix time in milliseconds or microseconds: homer-app
// names a field micro_ts and puts milliseconds in it.
func epochTime(v int64) time.Time {
	if v > 1e14 {
		return time.UnixMicro(v)
	}
	return time.UnixMilli(v)
}

func fmtTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format("2006-01-02 15:04:05.000")
}

func endpoint(m map[string]any, aliasKey, ipKey, portKey string) string {
	ip := str(m, ipKey)
	port := str(m, portKey)
	addr := ip
	if port != "" && port != "0" {
		if strings.Contains(ip, ":") {
			addr = "[" + ip + "]:" + port
		} else {
			addr = ip + ":" + port
		}
	}
	if alias := str(m, aliasKey); alias != "" && alias != addr {
		return alias
	}
	return addr
}

func protoName(m map[string]any) string {
	switch str(m, "protocol") {
	case "17":
		return "UDP"
	case "6":
		return "TCP"
	case "132":
		return "SCTP"
	}
	return str(m, "protocol")
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	return homer.Stringify(m[k])
}
