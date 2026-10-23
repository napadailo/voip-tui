package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

const (
	tabFlow = iota
	tabMessages
	tabQoS
	tabLogs
)

var tabNames = []string{N("Flow"), N("Messages"), N("QoS"), N("Logs")}

// CallView shows one or more correlated sessions.
type CallView struct {
	*tview.Flex
	app  *App
	ref  homer.CallRef
	tx   *homer.Transaction
	tab  int
	body *tview.Flex

	tabs     *tview.TextView
	status   *tview.TextView // what became of the call, under the tabs, on every one
	pages    *tview.Pages
	flow     *tview.Table
	messages *TableView
	qos      *TableView
	qosChart *tview.TextView // the reports as graphs, v on the QoS tab
	qosPages *tview.Pages
	qosData  *homer.QoS
	qosW     int             // the width the graphs were drawn for
	qosHops  string          // the hops with their addresses, for the message pane
	logText  *tview.TextView // the logs whole, wrapped, w on the Logs tab
	logPages *tview.Pages
	logItems []map[string]any
	logs     *TableView
	detail   *tview.TextView
	showDet  bool
	inDetail bool // the message pane has the focus
	follow   bool // F: reload the call on every tick until it ends

	qosLoaded, logsLoaded bool
	first                 time.Time
	vis                   []int // the messages the filter shows, indexes in tx

	// the ladder by host, so that h and l can scroll it: a table scrolls
	// whole columns, and the ladder is one column
	laneHead [2][]string // host names, addresses
	laneRows []laneArrow // the arrow of each message
	laneW    int         // the width of a host
	fixedW   int         // TIME and DELTA with their separators
	hoff     int         // the first host shown
}

func newCallView(app *App, ref homer.CallRef) *CallView {
	v := &CallView{app: app, ref: ref, showDet: true}
	v.tabs = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.status = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.detail = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	v.detail.SetBorder(true).SetTitle(" "+T("message")+" ").SetBorderPadding(0, 0, 1, 1)
	v.detail.SetInputCapture(v.detailKeys)

	v.flow = tview.NewTable().SetSelectable(true, false).SetFixed(2, 3)
	v.flow.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	v.flow.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Bold(true))
	// a list fills the message pane only while its tab is shown: a reload,
	// after :lang or r, selects rows on the other tabs too
	v.flow.SetSelectionChangedFunc(func(row, _ int) {
		if v.tab == tabFlow {
			v.showMessage(v.flowIndex(row))
		}
	})
	v.flow.SetSelectedFunc(func(row, _ int) { v.openMessage(v.flowIndex(row)) })
	v.flow.SetInputCapture(v.captureList(func(d int) {
		row, _ := v.flow.GetSelection()
		v.flow.Select(clamp(row+d, 2, v.flow.GetRowCount()-1), 0)
	}))

	v.messages = NewTableView(app, N("Messages"), []string{"#", N("TIME"), N("DELTA"), N("METHOD"), N("SOURCE"), N("DESTINATION"), N("PROTO"), N("SIZE"), N("SESSION ID"), N("NODE")})
	v.messages.OnSelect(func(r *Row) {
		if v.tab == tabMessages {
			v.showMessage(r.Data.(int))
		}
	})
	v.messages.OnEnter(func(r *Row) { v.openMessage(r.Data.(int)) })
	v.messages.SetInputCapture(v.chain(v.messages.HandleKey))

	v.qos = NewTableView(app, N("QoS (RTCP / RTP)"), []string{N("TIME"), N("TYPE"), N("SOURCE"), N("DESTINATION"), "SSRC", N("PACKETS"), N("LOST"), N("FRACTION"), N("JITTER"), "MOS"})
	v.qos.OnSelect(func(r *Row) {
		if v.tab == tabQoS && !v.app.flow.qosChart {
			v.showRaw(r.Data.(map[string]any))
		}
	})
	v.qos.OnEnter(func(r *Row) { v.openRaw(r.Data.(map[string]any), "json") })
	v.qos.SetInputCapture(v.chain(v.qos.HandleKey))
	v.qosChart = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.qosChart.SetBorder(true).SetTitle(" "+T("QoS graphs")+" ").SetBorderPadding(0, 0, 1, 1)
	v.qosChart.SetInputCapture(v.textKeys(v.qosChart))
	// the graphs fit the width they get, drawn again when it changes
	v.qosChart.SetDrawFunc(func(_ tcell.Screen, x, y, w, h int) (int, int, int, int) {
		if iw := w - 4; iw != v.qosW && v.qosData != nil {
			v.qosW = iw
			go v.app.QueueUpdateDraw(v.showQoS)
		}
		return x + 2, y + 1, w - 4, h - 2
	})
	v.qosPages = tview.NewPages().AddPage("table", v.qos, true, true).AddPage("chart", v.qosChart, true, false)

	v.logs = NewTableView(app, N("Logs"), []string{N("TIME"), N("SOURCE"), N("DESTINATION"), N("MESSAGE")})
	v.logs.OnSelect(func(r *Row) {
		if v.tab == tabLogs {
			v.showRaw(r.Data.(map[string]any))
		}
	})
	v.logs.OnEnter(func(r *Row) { v.openRaw(r.Data.(map[string]any), "plain") })
	v.logs.SetInputCapture(v.chain(v.logs.HandleKey))
	v.logText = tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true)
	v.logText.SetBorder(true).SetTitle(" "+T("Logs")+" ").SetBorderPadding(0, 0, 1, 1)
	v.logText.SetInputCapture(v.textKeys(v.logText))
	v.logPages = tview.NewPages().AddPage("table", v.logs, true, true).AddPage("text", v.logText, true, false)

	v.pages = tview.NewPages().
		AddPage("0", v.flow, true, true).
		AddPage("1", v.messages, true, false).
		AddPage("2", v.qosPages, true, false).
		AddPage("3", v.logPages, true, false)
	v.body = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.pages, 0, 3, true).
		AddItem(v.detail, 0, 2, false)
	v.Flex = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.tabs, 1, 0, false).
		AddItem(rule(), 1, 0, false).
		AddItem(v.status, 1, 0, false).
		AddItem(v.body, 0, 1, true)
	v.renderTabs()
	v.flow.SetTitle(" " + T("flow") + " " + theme.Tag(theme.Warn) + "⟳ ")
	v.load()
	return v
}

// rule is a thin line across, between the tabs and the status of a call.
func rule() *tview.Box {
	return tview.NewBox().SetDrawFunc(func(screen tcell.Screen, x, y, w, h int) (int, int, int, int) {
		style := tcell.StyleDefault.Background(tview.Styles.PrimitiveBackgroundColor).Foreground(theme.Dim)
		for i := 0; i < w; i++ {
			screen.SetContent(x+i, y, '─', nil, style)
		}
		return x, y, w, h
	})
}

func (v *CallView) Name() string {
	if len(v.ref.CallIDs) == 1 {
		return v.ref.CallIDs[0]
	}
	return Tf("%d sessions", len(v.ref.CallIDs))
}

// Hints show the state of f, a and u, as v does in search results.
func (v *CallView) Hints() []Hint {
	if v.inDetail {
		msg := N("message: as is")
		if v.app.flow.plain {
			msg = N("message: readable")
		}
		return []Hint{{Key: "j/k g/G", Desc: N("scroll")}, {Key: "ctrl-d/u", Desc: N("half page")}, {Key: "v", Desc: msg},
			{Key: "m/esc", Desc: N("back to the list")}}
	}
	hosts, unfold, msg, follow := N("hosts: by address"), N("unfold: on"), N("message: as is"), N("follow: off")
	if v.follow {
		follow = N("follow: on")
	}
	if v.app.flow.plain {
		msg = N("message: readable")
	}
	if v.tab == tabQoS {
		msg = N("QoS: table")
		if v.app.flow.qosChart {
			msg = N("QoS: graphs")
		}
	}
	if v.tab == tabLogs {
		wrap := N("wrap: off")
		if v.app.flow.logWrap {
			wrap = N("wrap: on")
		}
		return []Hint{{Key: "1-4/tab", Desc: N("flow·msgs·qos·logs")}, {Key: "w", Desc: wrap}, {Key: "enter", Desc: N("full message")},
			{Key: "z", Desc: N("toggle detail")}, {Key: "m", Desc: N("into the message")}, {Key: "F", Desc: follow}, {Key: "p", Desc: "pcap"}, {Key: "t", Desc: N("text export")},
			{Key: "y", Desc: "JSON"}, {Key: "r", Desc: N("reload")}}
	}
	if v.app.flow.byAlias {
		hosts = N("hosts: by alias")
	}
	if v.app.flow.fold {
		unfold = N("unfold: off")
	}
	return []Hint{
		{Key: "1-4/tab", Desc: N("flow·msgs·qos·logs")}, {Key: "enter", Desc: N("full message")}, {Key: "z", Desc: N("toggle detail")},
		{Key: "h/l", Desc: N("scroll hosts")}, {Key: "f", Desc: v.shownHint()}, {Key: "a", Desc: hosts}, {Key: "u", Desc: unfold}, {Key: "v", Desc: msg}, {Key: "m", Desc: N("into the message")}, {Key: "F", Desc: follow},
		{Key: "p", Desc: "pcap"}, {Key: "t", Desc: N("text export")}, {Key: "y", Desc: "JSON"}, {Key: "r", Desc: N("reload")},
	}
}

// shownHint names the kinds of messages the filter shows.
func (v *CallView) shownHint() string {
	kinds := []string{"SIP", "RTCP", "LOG"}
	var other []string
	for k, hide := range v.app.flow.hide {
		if hide && k != "SIP" && k != "RTCP" && k != "LOG" {
			other = append(other, k)
		}
	}
	sort.Strings(other)
	var shown []string
	for _, k := range append(kinds, other...) {
		if !v.app.flow.hidden(k) {
			shown = append(shown, k)
		}
	}
	switch {
	case len(shown) == len(kinds) && len(other) == 0:
		return T("shown: all")
	case len(shown) == 0:
		return T("shown: nothing")
	}
	return Tf("shown: %s", strings.Join(shown, ", "))
}

func (v *CallView) Table() *TableView {
	switch v.tab {
	case tabMessages:
		return v.messages
	case tabQoS:
		if v.app.flow.qosChart {
			return nil
		}
		return v.qos
	case tabLogs:
		if v.app.flow.logWrap {
			return nil
		}
		return v.logs
	}
	return nil
}

func (v *CallView) Refresh() {
	v.qosLoaded, v.logsLoaded = false, false
	v.load()
	if v.tab == tabQoS {
		v.loadQoS()
	}
	if v.tab == tabLogs {
		v.loadLogs()
	}
}

func (v *CallView) Close() {}

// Focus sends focus to the active list.
func (v *CallView) Focus(delegate func(p tview.Primitive)) { delegate(v.activeList()) }

func (v *CallView) activeList() tview.Primitive {
	switch v.tab {
	case tabMessages:
		return v.messages
	case tabQoS:
		if v.app.flow.qosChart {
			return v.qosChart
		}
		return v.qos
	case tabLogs:
		if v.app.flow.logWrap {
			return v.logText
		}
		return v.logs
	}
	return v.flow
}

// chain wraps a TableView capture with call-view keys.
func (v *CallView) chain(inner func(*tcell.EventKey) *tcell.EventKey) func(*tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		if ev = v.keys(ev); ev == nil {
			return nil
		}
		return inner(ev)
	}
}

func (v *CallView) captureList(move func(int)) func(*tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		if ev = v.keys(ev); ev == nil {
			return nil
		}
		_, _, _, h := v.flow.GetInnerRect()
		switch ev.Key() {
		case tcell.KeyCtrlD:
			move(h / 2)
			return nil
		case tcell.KeyCtrlU:
			move(-h / 2)
			return nil
		case tcell.KeyLeft:
			v.scrollLanes(-1)
			return nil
		case tcell.KeyRight:
			v.scrollLanes(1)
			return nil
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'h':
				v.scrollLanes(-1)
				return nil
			case 'l':
				v.scrollLanes(1)
				return nil
			case '0':
				v.scrollLanes(-len(v.laneHead[0]))
				return nil
			case '$':
				v.scrollLanes(len(v.laneHead[0]))
				return nil
			}
		}
		return ev
	}
}

func (v *CallView) keys(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyTab:
		v.setTab((v.tab + 1) % len(tabNames))
		return nil
	case tcell.KeyBacktab:
		v.setTab((v.tab + len(tabNames) - 1) % len(tabNames))
		return nil
	case tcell.KeyRune:
		switch r := ev.Rune(); r {
		case '1', '2', '3', '4':
			v.setTab(int(r - '1'))
			return nil
		case 'z':
			v.showDet = !v.showDet
			if v.showDet {
				v.body.ResizeItem(v.detail, 0, 2)
			} else {
				v.body.ResizeItem(v.detail, 0, 0)
			}
			return nil
		case 'p':
			exportRows(v.app, v.exportRef(), "pcap")
			return nil
		case 't':
			exportRows(v.app, v.exportRef(), "text")
			return nil
		case 'y', 'd': // d as before; y as in the other views
			v.describe()
			return nil
		case 'r':
			v.Refresh()
			return nil
		case 'f':
			if v.tx != nil {
				v.app.Push(newFlowFilterView(v))
			}
			return nil
		case 'a':
			v.app.flow.byAlias = !v.app.flow.byAlias
			v.applyFlow()
			if v.app.flow.byAlias {
				v.app.Flash().Info(T("hosts by alias"))
			} else {
				v.app.Flash().Info(T("hosts by address"))
			}
			v.app.RefreshHints()
			return nil
		case 'v':
			if v.tab == tabQoS {
				v.app.flow.qosChart = !v.app.flow.qosChart
				v.showQoS()
				v.redrawDetail()
				v.app.SetFocus(v.activeList())
			} else {
				v.app.flow.plain = !v.app.flow.plain
				v.redrawDetail()
			}
			v.app.RefreshHints()
			return nil
		case 'm':
			v.focusDetail(true)
			return nil
		case 'F':
			v.follow = !v.follow
			if v.follow {
				v.Refresh()
				v.app.Flash().Info(T("following the call: reloaded on every tick until it ends"))
			} else {
				v.app.Flash().Info(T("following stopped"))
			}
			if v.tx != nil {
				v.drawLanes()
			}
			v.app.RefreshHints()
			return nil
		case 'w':
			if v.tab == tabLogs {
				v.app.flow.logWrap = !v.app.flow.logWrap
				v.showLogs()
				v.redrawDetail()
				v.app.SetFocus(v.activeList())
				v.app.RefreshHints()
				return nil
			}
		case 'u':
			v.app.flow.fold = !v.app.flow.fold
			v.applyFlow()
			if v.app.flow.fold {
				v.app.Flash().Info(T("each host once"))
			} else {
				v.app.Flash().Info(T("unfold calls that come back"))
			}
			v.app.RefreshHints()
			return nil
		}
	}
	return ev
}

func (v *CallView) setTab(t int) {
	v.tab = t
	v.pages.SwitchToPage(fmt.Sprint(t))
	switch t {
	case tabQoS:
		v.showQoS()
	case tabLogs:
		v.showLogs()
	}
	v.renderTabs()
	v.app.SetFocus(v.activeList())
	defer v.app.RefreshHints()
	switch t {
	case tabQoS:
		if !v.qosLoaded {
			v.loadQoS()
		}
	case tabLogs:
		if !v.logsLoaded {
			v.loadLogs()
		}
	}
	v.redrawDetail()
}

func (v *CallView) renderTabs() {
	var b strings.Builder
	b.WriteString(" ")
	for i, n := range tabNames {
		if i == v.tab {
			fmt.Fprintf(&b, "[black:orange:b] %d %s [-:-:-] ", i+1, T(n))
		} else {
			fmt.Fprintf(&b, "[%s] %d %s [-] ", theme.KeyDesc.String(), i+1, T(n))
		}
	}
	fmt.Fprintf(&b, " [%s]%s[-]", theme.Dim.String(), tview.Escape(strings.Join(v.ref.CallIDs, ", ")))
	v.tabs.SetText(b.String())
}

// exportRef includes every correlated session found in the transaction.
func (v *CallView) exportRef() *homer.CallRef {
	ref := v.ref
	seen := map[string]bool{}
	ref.CallIDs = nil
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			ref.CallIDs = append(ref.CallIDs, s)
		}
	}
	for _, s := range v.ref.CallIDs {
		add(s)
	}
	if v.tx != nil {
		for _, m := range v.tx.Data.Messages {
			add(str(m, "sid"))
		}
	}
	return &ref
}

func (v *CallView) load() {
	if v.follow { // the window grows with the call
		v.ref.To = time.Now()
	}
	ref := v.ref
	v.app.Async(func(ctx context.Context) func() {
		tx, err := v.app.client.Transaction(ctx, ref)
		return func() {
			if err != nil {
				v.flow.SetTitle(" " + T("flow") + " ")
				if brokenOff(err) {
					v.app.Flash().Errf("homer-app broke off its answer: it failed assembling the call of %s, its log says why (%v)", ref.Key, err)
					return
				}
				v.app.Flash().Err(err)
				return
			}
			// a reload stays on the message selected, on the last one when
			// that was the last: new messages then come into view
			keep, first := 0, v.tx == nil
			if !first {
				row, _ := v.flow.GetSelection()
				keep = max(v.flowIndex(row), 0)
				if row-2 >= len(v.vis)-1 {
					keep = len(tx.Data.Calldata) - 1
				}
			}
			v.tx = tx
			o := callOutcomeOf(tx, v.msgTime)
			v.status.SetText(o.line(v.follow && !v.callEnded()))
			v.filter()
			v.renderFlow(keep)
			v.renderMessages()
			if v.tab != tabQoS && v.tab != tabLogs && !v.inDetail { // they show their own; the pane is being read
				v.redrawDetail()
			}
			switch {
			case v.follow && v.callEnded():
				v.follow = false
				v.drawLanes()
				v.app.RefreshHints()
				v.app.Flash().Infof("the call ended, following stopped: %d messages", len(tx.Data.Calldata))
			case first: // reloads are quiet, not to wipe what the status line says
				v.app.Flash().Infof("%d messages, %d hosts", len(tx.Data.Calldata), v.laneCount(v.app.flow.byAlias, !v.app.flow.fold))
			}
		}
	})
}

// brokenOff tells an answer homer-app broke off, as it does when it fails
// inside a request: the stream or connection closed without one.
func brokenOff(err error) bool {
	s := err.Error()
	return strings.Contains(s, "INTERNAL_ERROR") || strings.Contains(s, "unexpected EOF") || strings.HasSuffix(s, ": EOF") || strings.Contains(s, "connection reset")
}

// AutoRefresh reloads the call on every tick while F follows it.
func (v *CallView) AutoRefresh() bool { return v.follow }

// callEnded tells a call that is over: a BYE answered 2xx, or an INVITE
// answered with a failure, past a 401 or 407, and none answered 2xx.
func (v *CallView) callEnded() bool {
	var byeOK, failed, answered bool
	for i, c := range v.tx.Data.Calldata {
		_, of := cseqOf(v.message(i))
		code, err := strconv.Atoi(c.Method)
		if err != nil {
			continue
		}
		switch {
		case of == "BYE" && code/100 == 2:
			byeOK = true
		case of == "INVITE" && code/100 == 2:
			answered = true
		case of == "INVITE" && code >= 300 && code != 401 && code != 407:
			failed = true
		}
	}
	return byeOK || failed && !answered
}

// flowHost is a lane of the flow: one address, or with byAlias all the
// addresses of an alias.
type flowHost struct {
	key, label, sub string
}

func laneKey(id, alias string, byAlias bool) string {
	if byAlias && alias != "" {
		return "alias:" + alias
	}
	return id
}

// layout puts the messages the filter shows on lanes and returns the
// lanes and the lanes of each message. A lane is an address, or with
// byAlias the addresses of an alias. A leg is the messages of one session
// between two hosts; its first request goes left to right. With unfold a
// leg that starts towards a host left of the sender draws that host again
// on the right, so a call that comes back reads on to the right too; a
// sender whose alias is already drawn further right on another address is
// drawn again there as well, as one device with two interfaces. A second
// call the leftmost host starts, as a PBX calling two parties to bridge
// them, is drawn to its left, so the PBX stands between its calls.
func (v *CallView) layout(byAlias, unfold bool) ([]flowHost, [][2]int) {
	var hosts []flowHost
	var ids []map[string]bool
	var sip []bool               // lanes with SIP, not only media or logs
	var pos []int                // where a lane stands, the lanes sorted by it at the end
	var leftSide []bool          // a lane of a call drawn to the left
	originated := map[int]bool{} // lanes that started a leg
	minPos, maxPos := 0, -1
	right := map[string]int{} // the latest lane of a key
	newLane := func(key, alias, id string, dir int) int {
		label := alias
		if label == "" {
			label = id
		}
		p := maxPos + 1
		if dir < 0 {
			p = minPos - 1
		}
		minPos, maxPos = min(minPos, p), max(maxPos, p)
		hosts = append(hosts, flowHost{key: key, label: label})
		ids = append(ids, map[string]bool{})
		sip = append(sip, false)
		pos = append(pos, p)
		leftSide = append(leftSide, dir < 0)
		right[key] = len(hosts) - 1
		return len(hosts) - 1
	}
	// lane is the latest lane of key; a new one goes on the side of its
	// device: an address of a host drawn to the left, a second interface
	// say, stands to the left too
	lane := func(key, alias, id string) int {
		if l, ok := right[key]; ok {
			return l
		}
		dir := 1
		for k := range hosts {
			if alias != "" && hosts[k].label == alias && leftSide[k] {
				dir = -1
			}
		}
		return newLane(key, alias, id, dir)
	}
	legOf := func(c homer.CallElement) (leg, sk, dk string) {
		sk, dk = laneKey(c.SrcID, c.AliasSrc, byAlias), laneKey(c.DstID, c.AliasDst, byAlias)
		return c.Sid + "\x00" + min(sk, dk) + "\x00" + max(sk, dk), sk, dk
	}
	// the first request of each leg sets its direction, even when an
	// answer was captured a little earlier by another agent
	firstReq := map[string]int{}
	for _, i := range v.vis {
		if leg, _, _ := legOf(v.tx.Data.Calldata[i]); v.isRequest(i) {
			if _, ok := firstReq[leg]; !ok {
				firstReq[leg] = i
			}
		}
	}
	// beyond finds the nearest lane of key past lane l in direction dir
	beyond := func(l, dir int, ok func(k int) bool) int {
		best := -1
		for k := range hosts {
			if (pos[k]-pos[l])*dir > 0 && ok(k) && (best < 0 || (pos[k]-pos[best])*dir < 0) {
				best = k
			}
		}
		return best
	}
	// place gives a leg its lanes from its first request r: the sender on
	// its latest lane, unfolded the receiver past it in the way the call
	// goes, to the right; a call the leftmost host starts after one it
	// started already, as a PBX that calls two parties and bridges them,
	// goes to the left, and so does all that follows it
	place := func(r homer.CallElement, unfold bool) [2]int {
		_, sk, dk := legOf(r)
		l := [2]int{lane(sk, r.AliasSrc, r.SrcID), -1}
		if !unfold || sk == dk {
			l[1] = lane(dk, r.AliasDst, r.DstID)
			return l
		}
		dir := 1
		if leftSide[l[0]] {
			dir = -1
		}
		isKey := func(k int) bool { return hosts[k].key == dk }
		if dir > 0 && originated[l[0]] && pos[l[0]] == minPos && beyond(l[0], 1, isKey) < 0 {
			dir = -1
		}
		// the sender's device came back on another of its signalling
		// addresses: the sender is drawn again past it too
		dev := r.AliasSrc
		if dev == "" {
			dev = r.SrcID
		}
		if beyond(l[0], dir, func(k int) bool { return hosts[k].label == dev && sip[k] }) >= 0 {
			l[0] = newLane(sk, r.AliasSrc, r.SrcID, dir)
		}
		originated[l[0]] = true
		if l[1] = beyond(l[0], dir, isKey); l[1] < 0 {
			l[1] = newLane(dk, r.AliasDst, r.DstID, dir)
		}
		return l
	}
	legs := map[string][2]int{} // the lanes of the sender and the receiver
	arrows := make([][2]int, len(v.vis))
	for j, i := range v.vis {
		c := v.tx.Data.Calldata[i]
		leg, sk, _ := legOf(c)
		l, ok := legs[leg]
		if !ok {
			r, req := firstReq[leg]
			if !req {
				r = i
			}
			l = place(v.tx.Data.Calldata[r], unfold && req)
			legs[leg] = l
		}
		if hosts[l[0]].key != sk { // a message the other way
			l = [2]int{l[1], l[0]}
		}
		ids[l[0]][c.SrcID] = true
		ids[l[1]][c.DstID] = true
		if msgKind(v.message(i), c.Method) == "SIP" {
			sip[l[0]], sip[l[1]] = true, true
		}
		arrows[j] = l
	}
	for i := range hosts {
		h := &hosts[i]
		if n := len(ids[i]); n > 1 {
			h.sub = Tf("%d addresses", n)
			continue
		}
		for id := range ids[i] {
			if id != h.label {
				h.sub = id
			}
		}
	}
	// the lanes in the order they stand
	order := make([]int, len(hosts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return pos[order[a]] < pos[order[b]] })
	rank := make([]int, len(hosts))
	sorted := make([]flowHost, len(hosts))
	for r, i := range order {
		rank[i], sorted[r] = r, hosts[i]
	}
	for j := range arrows {
		arrows[j] = [2]int{rank[arrows[j][0]], rank[arrows[j][1]]}
	}
	return sorted, arrows
}

// isRequest tells a SIP request from a response, whose method is its code.
func (v *CallView) isRequest(i int) bool {
	c := v.tx.Data.Calldata[i]
	if msgKind(v.message(i), c.Method) != "SIP" || c.Method == "" {
		return false
	}
	_, err := strconv.Atoi(c.Method)
	return err != nil
}

func (v *CallView) laneCount(byAlias, unfold bool) int {
	hosts, _ := v.layout(byAlias, unfold)
	return len(hosts)
}

type kindCount struct {
	name string
	n    int
}

// kinds counts the messages of the call by kind: SIP, RTCP and LOG
// always, so the filter looks the same for every call, then the kinds the
// call has or the filter hides.
func (v *CallView) kinds() []kindCount {
	if v.tx == nil {
		return nil
	}
	n := map[string]int{}
	for k, hide := range v.app.flow.hide {
		if hide {
			n[k] += 0
		}
	}
	for i, c := range v.tx.Data.Calldata {
		n[msgKind(v.message(i), c.Method)]++
	}
	var out []kindCount
	for _, k := range []string{"SIP", "RTCP", "LOG"} {
		out = append(out, kindCount{k, n[k]})
		delete(n, k)
	}
	var rest []string
	for k := range n {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	for _, k := range rest {
		out = append(out, kindCount{k, n[k]})
	}
	return out
}

// filter picks the messages of the kinds that are on.
func (v *CallView) filter() {
	v.vis = v.vis[:0]
	for i, c := range v.tx.Data.Calldata {
		if !v.app.flow.hidden(msgKind(v.message(i), c.Method)) {
			v.vis = append(v.vis, i)
		}
	}
}

// flowIndex is the message of a row of the flow, -1 for the header.
func (v *CallView) flowIndex(row int) int {
	if row-2 < 0 || row-2 >= len(v.vis) {
		return -1
	}
	return v.vis[row-2]
}

// applyFlow draws the call again after the filter changed, on the same
// message or the one shown before it.
func (v *CallView) applyFlow() {
	if v.tx == nil {
		return
	}
	row, _ := v.flow.GetSelection()
	keep := max(v.flowIndex(row), 0)
	v.filter()
	v.renderFlow(keep)
	v.renderMessages()
}

// msgTime is the time of a message to the microsecond: from its
// timeSeconds and timeUseconds, else from the arrow, which homer-app gives
// in milliseconds.
func (v *CallView) msgTime(i int) time.Time {
	if i < len(v.tx.Data.Messages) {
		if t := itemTime(v.tx.Data.Messages[i]); !t.IsZero() {
			return t
		}
	}
	c := v.tx.Data.Calldata[i]
	if c.MicroTs > 0 {
		return epochTime(c.MicroTs)
	}
	return epochTime(c.CreateDate)
}

// arrowLabel returns "INVITE" or "200 OK".
func (v *CallView) arrowLabel(i int) string {
	c := v.tx.Data.Calldata[i]
	label := c.Method
	if i < len(v.tx.Data.Messages) {
		raw := str(v.tx.Data.Messages[i], "raw")
		if first, _, _ := strings.Cut(raw, "\n"); strings.HasPrefix(first, "SIP/2.0 ") {
			label = strings.TrimSpace(strings.TrimPrefix(first, "SIP/2.0 "))
		}
	}
	return label
}

// renderFlow draws the ladder of the messages shown and selects keep, or
// the last message shown before it.
func (v *CallView) renderFlow(keep int) {
	v.flow.Clear()
	hosts, arrows := v.layout(v.app.flow.byAlias, !v.app.flow.fold)
	width := 18
	for _, h := range hosts {
		for _, s := range []string{h.label, h.sub} {
			if l := utf8.RuneCountInString(s) + 3; l > width {
				width = l
			}
		}
	}
	if width > 32 {
		width = 32
	}
	n := len(v.vis)
	v.flow.SetCell(0, 0, tview.NewTableCell(T("TIME")).SetTextColor(theme.Header).SetSelectable(false))
	v.flow.SetCell(0, 1, tview.NewTableCell(T("DELTA")).SetTextColor(theme.Header).SetSelectable(false))
	v.flow.SetCell(1, 0, tview.NewTableCell("").SetSelectable(false))
	v.flow.SetCell(1, 1, tview.NewTableCell("").SetSelectable(false))
	v.flow.SetCell(0, 2, tview.NewTableCell("TX").SetTextColor(theme.Header).SetSelectable(false))
	v.flow.SetCell(1, 2, tview.NewTableCell("").SetSelectable(false))
	v.laneW, v.laneRows = width, nil
	v.laneHead = [2][]string{make([]string, len(hosts)), make([]string, len(hosts))}
	for i, h := range hosts {
		v.laneHead[0][i], v.laneHead[1][i] = center(h.label, width), center(h.sub, width) // escaped once cut to the screen
	}
	if n == 0 {
		v.drawLanes()
		empty := T("no messages in the time window")
		if len(v.tx.Data.Calldata) > 0 {
			empty = T("the filter hides all messages, f changes it")
		}
		v.flow.SetCell(2, 0, tview.NewTableCell(empty).SetTextColor(theme.Dim))
		v.detail.SetText("")
		return
	}
	v.first = v.msgTime(v.vis[0])
	var prev time.Time
	timeW, deltaW := utf8.RuneCountInString(T("TIME")), utf8.RuneCountInString(T("DELTA"))
	txCells, txW := txGraph(v.txKeys())
	sel := 0
	for j, i := range v.vis {
		if i <= keep {
			sel = j
		}
		t := v.msgTime(i)
		delta := "+0.000"
		if !prev.IsZero() {
			delta = fmt.Sprintf("+%.3f", t.Sub(prev).Seconds())
		}
		prev = t
		color := methodColor(v.arrowLabel(i))
		ts := t.In(v.app.loc).Format("15:04:05.000")
		timeW, deltaW = max(timeW, len(ts)), max(deltaW, len(delta))
		v.flow.SetCell(j+2, 0, tview.NewTableCell(ts).SetTextColor(theme.Fg))
		v.flow.SetCell(j+2, 1, tview.NewTableCell(delta).SetTextColor(theme.Dim))
		v.flow.SetCell(j+2, 2, tview.NewTableCell(txCells[j]))
		v.laneRows = append(v.laneRows, laneArrow{arrows[j][0], arrows[j][1], v.arrowLabel(i), color})
	}
	v.fixedW = timeW + 1 + deltaW + 1 + max(txW, 2) + 1
	v.drawLanes()
	v.flow.ScrollToBeginning()
	v.flow.Select(sel+2, 0)
	if v.tab == tabFlow {
		v.showMessage(v.vis[sel])
	}
}

// laneSpace is the width beside TIME and DELTA; 0 before the first draw.
func (v *CallView) laneSpace() int {
	_, _, w, _ := v.flow.GetInnerRect()
	if v.laneW == 0 {
		return 0
	}
	return max(0, w-v.fixedW)
}

func (v *CallView) scrollLanes(d int) {
	if v.tx == nil {
		return
	}
	v.hoff += d
	v.drawLanes()
}

// drawLanes puts the ladder from host hoff on and says in the title which
// hosts are shown when not all fit.
func (v *CallView) drawLanes() {
	n := len(v.laneHead[0])
	space := v.laneSpace()
	shown, last := n, n-1 // whole hosts on screen, the last first one
	if space > 0 {
		shown = max(1, space/v.laneW)
		last = n - shown
	}
	v.hoff = clamp(v.hoff, 0, last)
	from, to := v.hoff*v.laneW, n*v.laneW // the columns of the ladder on screen
	if space > 0 {
		to = min(to, from+space)
	}
	join := func(cells []string) string { return strings.Join(cells[v.hoff:], "") }
	// the hosts on screen only, so that what is right of the ladder stays in view
	head := func(cells []string) string {
		s := []rune(join(cells))
		return tview.Escape(string(s[:min(len(s), to-from)]))
	}
	v.flow.SetCell(0, 3, tview.NewTableCell(head(v.laneHead[0])).SetTextColor(theme.InfoKey).
		SetAttributes(tcell.AttrBold).SetSelectable(false))
	v.flow.SetCell(1, 3, tview.NewTableCell(head(v.laneHead[1])).SetTextColor(theme.Dim).SetSelectable(false))
	// the hosts on screen only, as the header; between the hosts of the
	// arrow in its colour
	row := func(cells []string, lo, hi int, color tcell.Color) string {
		var b strings.Builder
		left := to - from
		for l := v.hoff; l < n && left > 0; l++ {
			col := theme.Dim
			if l >= lo && l <= hi {
				col = color
			}
			text := []rune(cells[l])
			text = text[:min(len(text), left)]
			left -= len(text)
			b.WriteString(theme.Tag(col) + tview.Escape(string(text)))
		}
		return b.String()
	}
	for i, a := range v.laneRows {
		lo, hi := min(a.src, a.dst), max(a.src, a.dst)
		v.flow.SetCell(i+2, 3, tview.NewTableCell(row(laneCells(n, a.src, a.dst, v.laneW, a.label, from, to), lo, hi, a.color)))
	}
	count := fmt.Sprint(len(v.laneRows))
	if v.tx != nil && len(v.laneRows) < len(v.tx.Data.Calldata) {
		count += "/" + fmt.Sprint(len(v.tx.Data.Calldata))
	}
	title := fmt.Sprintf(" [::b]%s[::-][white][%s[][-] ", T("flow"), count)
	if note := v.flowNote(); note != "" {
		title += theme.Tag(theme.Warn) + tview.Escape(note) + "[-] "
	}
	if v.follow {
		title += theme.Tag(theme.OK) + "● " + T("live") + "[-] "
	}
	if shown < n {
		left, right := " ", " "
		if v.hoff > 0 {
			left = "◀"
		}
		if v.hoff+shown < n {
			right = "▶"
		}
		title += theme.Tag(theme.Dim) + left + " " + Tf("hosts %d-%d of %d", v.hoff+1, min(v.hoff+shown, n), n) + " " + right + "[-] "
	}
	v.flow.SetTitle(title)
}

// laneArrow is one message on the ladder: from host src to host dst.
type laneArrow struct {
	src, dst int
	label    string
	color    tcell.Color
}

// laneCells draws one ladder row split into per-host cells of the given
// width. The label and the head go in the part of the arrow on screen, the
// columns from..to-1 of the row.
func laneCells(n, src, dst, width int, label string, from, to int) []string {
	c := width / 2
	line := []rune(strings.Repeat(" ", n*width))
	for i := 0; i < n; i++ {
		line[i*width+c] = '│'
	}
	if src == dst {
		start := src*width + c + 1
		text := []rune(" ⟲ " + label)
		for k := 0; k < len(text) && start+k < len(line); k++ {
			line[start+k] = text[k]
		}
	} else {
		lo, hi := src, dst
		if lo > hi {
			lo, hi = hi, lo
		}
		a, b := lo*width+c, hi*width+c
		for x := a + 1; x < b; x++ {
			line[x] = '─'
		}
		if src < dst {
			line[b-1] = '▶'
		} else {
			line[a+1] = '◀'
		}
		// a head on a host off screen goes to the edge
		if src > dst && a+1 < from {
			line[from] = '◀'
		}
		if src < dst && b-1 >= to {
			line[to-1] = '▶'
		}
		la, lb := max(a, from-1), min(b, to)
		span := lb - la - 4
		lr := []rune(label)
		if span > 0 {
			if len(lr) > span {
				lr = append(lr[:span-1], '…')
			}
			start := la + 2 + (span-len(lr))/2
			for k, r := range lr {
				line[start+k] = r
			}
		}
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = string(line[i*width : (i+1)*width])
	}
	return out
}

func center(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(append(r[:width-2], '…', ' '))
	}
	left := (width - len(r)) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", width-len(r)-left)
}

func (v *CallView) renderMessages() {
	rows := make([]Row, 0, len(v.vis))
	var prev time.Time
	for _, i := range v.vis {
		c := v.tx.Data.Calldata[i]
		t := v.msgTime(i)
		delta := "+0.000"
		if !prev.IsZero() {
			delta = fmt.Sprintf("+%.3f", t.Sub(prev).Seconds())
		}
		prev = t
		m := map[string]any{}
		if i < len(v.tx.Data.Messages) {
			m = v.tx.Data.Messages[i]
		}
		label := v.arrowLabel(i)
		src, dst := c.SrcID, c.DstID
		if c.AliasSrc != "" && c.AliasSrc != c.SrcID {
			src = c.AliasSrc + " (" + c.SrcID + ")"
		}
		if c.AliasDst != "" && c.AliasDst != c.DstID {
			dst = c.AliasDst + " (" + c.DstID + ")"
		}
		rows = append(rows, Row{
			Cells: []string{fmt.Sprint(i + 1), t.In(v.app.loc).Format("15:04:05.000000"), delta, label, src, dst,
				protoName(m), fmt.Sprint(len(str(m, "raw"))), c.Sid, str(m, "dbnode")},
			Color: methodColor(label),
			Data:  i,
			Key:   fmt.Sprint(i),
		})
	}
	v.messages.SetRows(rows)
}

func (v *CallView) message(i int) map[string]any {
	if v.tx == nil || i < 0 || i >= len(v.tx.Data.Messages) {
		return nil
	}
	return v.tx.Data.Messages[i]
}

func (v *CallView) showMessage(i int) {
	m := v.message(i)
	if m == nil {
		return
	}
	c := v.tx.Data.Calldata[i]
	head := fmt.Sprintf("[%s]#%d  %s  %s → %s  %s  %s[-]\n\n", theme.Dim.String(), i+1,
		v.msgTime(i).In(v.app.loc).Format("2006-01-02 15:04:05.000000"), tview.Escape(c.SrcID), tview.Escape(c.DstID), tview.Escape(protoName(m)), tview.Escape(str(m, "sid")))
	if v.app.flow.plain && msgKind(m, c.Method) == "SIP" {
		v.detail.SetText(head + sipSummary(str(m, "raw"), v.aliasOf).String())
	} else {
		v.detail.SetText(head + colorizeSIP(str(m, "raw")))
	}
	v.detail.ScrollToBeginning()
}

// focusDetail moves the focus into the message pane to scroll it, or back
// to the list.
func (v *CallView) focusDetail(in bool) {
	if in && !v.showDet {
		v.showDet = true
		v.body.ResizeItem(v.detail, 0, 2)
	}
	v.inDetail = in
	if in {
		v.detail.SetBorderColor(theme.Accent).SetTitle(" " + T("message") + " · " + T("m or esc: back to the list") + " ")
		v.app.SetFocus(v.detail)
	} else {
		v.detail.SetBorderColor(theme.Border).SetTitle(" " + T("message") + " ")
		v.app.SetFocus(v.activeList())
	}
	v.app.RefreshHints()
}

// Back leaves the message pane before esc or q close the call.
func (v *CallView) Back() bool {
	if !v.inDetail {
		return false
	}
	v.focusDetail(false)
	return true
}

// halfPage scrolls a text view by half its height on ctrl-d and ctrl-u,
// as the tables and the pager do; it reports whether the key was one.
func halfPage(tv *tview.TextView, ev *tcell.EventKey) bool {
	_, _, _, h := tv.GetInnerRect()
	row, _ := tv.GetScrollOffset()
	switch ev.Key() {
	case tcell.KeyCtrlD:
		tv.ScrollTo(row+max(h/2, 1), 0)
	case tcell.KeyCtrlU:
		tv.ScrollTo(max(row-max(h/2, 1), 0), 0)
	default:
		return false
	}
	return true
}

// textKeys are the keys of a text view on a tab: half pages, then the
// keys of the call.
func (v *CallView) textKeys(tv *tview.TextView) func(*tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		if halfPage(tv, ev) {
			return nil
		}
		return v.keys(ev)
	}
}

// detailKeys scroll the message pane; the other keys of the call do not
// reach the list from here.
func (v *CallView) detailKeys(ev *tcell.EventKey) *tcell.EventKey {
	if halfPage(v.detail, ev) {
		return nil
	}
	switch ev.Key() {
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'm':
			v.focusDetail(false)
			return nil
		case 'v':
			v.app.flow.plain = !v.app.flow.plain
			v.redrawDetail()
			v.app.RefreshHints()
			return nil
		case 'j', 'k', 'g', 'G', 'h', 'l':
			return ev
		}
		return nil
	}
	return ev
}

// redrawDetail shows in the message pane what is selected on the tab, or
// clears it: a tab does not keep the pane of the one before.
func (v *CallView) redrawDetail() {
	v.detail.SetText("")
	switch v.tab {
	case tabFlow:
		row, _ := v.flow.GetSelection()
		v.showMessage(v.flowIndex(row))
	case tabMessages:
		if r := v.messages.Selected(); r != nil {
			v.showMessage(r.Data.(int))
		}
	case tabQoS:
		if v.app.flow.qosChart {
			v.detail.SetText(v.qosHops + "\n" + qosLegend())
		} else if r := v.qos.Selected(); r != nil {
			v.showRaw(r.Data.(map[string]any))
		}
	case tabLogs:
		if v.app.flow.logWrap {
			v.detail.SetText(tui.Faint(T("w: the logs as a table again, one per line with the message cut")))
		} else if r := v.logs.Selected(); r != nil {
			v.showRaw(r.Data.(map[string]any))
		}
	}
}

// aliasOf names an address of the call with its alias in Homer, as the
// flow does, and the address, which tells the interfaces of a device apart.
func (v *CallView) aliasOf(hostport string) string {
	if v.tx != nil {
		for _, c := range v.tx.Data.Calldata {
			for _, e := range [][2]string{{c.SrcID, c.AliasSrc}, {c.DstID, c.AliasDst}} {
				if e[1] != "" && e[1] != e[0] && (e[0] == hostport || e[0] == hostport+":5060") {
					return e[1] + " (" + hostport + ")"
				}
			}
		}
	}
	return hostport
}

func (v *CallView) showRaw(m map[string]any) {
	raw := str(m, "raw")
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		v.detail.SetText(colorizeJSON(prettyJSON([]byte(raw))))
	} else {
		v.detail.SetText(tview.Escape(raw))
	}
	v.detail.ScrollToBeginning()
}

func (v *CallView) openMessage(i int) {
	m := v.message(i)
	if m == nil {
		return
	}
	tv := newTextViewer(v.app, fmt.Sprintf("message #%d %s", i+1, v.arrowLabel(i)), "sip")
	sip := msgKind(m, v.tx.Data.Calldata[i].Method) == "SIP"
	tv.Loader = func() {
		if v.app.flow.plain && sip {
			tv.SetContent(sipSummary(str(m, "raw"), v.aliasOf).String(), "markup")
		} else {
			tv.SetContent(str(m, "raw"), "sip")
		}
	}
	if sip { // v here as in the call, for this view and the call's pane
		tv.Keys['v'] = func() {
			v.app.flow.plain = !v.app.flow.plain
			tv.Loader()
			tv.Text().ScrollToBeginning()
			v.redrawDetail()
		}
		tv.Extra = []Hint{{Key: "v", Desc: N("readable/as is")}}
	}
	tv.Loader()
	v.app.Push(tv)
}

func (v *CallView) openRaw(m map[string]any, syntax string) {
	raw := str(m, "raw")
	if syntax == "json" {
		raw = prettyJSON([]byte(raw))
	}
	tv := newTextViewer(v.app, "raw "+str(m, "sid"), syntax)
	tv.SetContent(raw, syntax)
	v.app.Push(tv)
}

func (v *CallView) describe() {
	var obj any
	switch v.tab {
	case tabFlow:
		row, _ := v.flow.GetSelection()
		obj = v.message(v.flowIndex(row))
	case tabMessages:
		if r := v.messages.Selected(); r != nil {
			obj = v.message(r.Data.(int))
		}
	case tabQoS:
		if r := v.qos.Selected(); r != nil {
			obj = r.Data
		}
	case tabLogs:
		if r := v.logs.Selected(); r != nil {
			obj = r.Data
		}
	}
	if obj == nil {
		return
	}
	pushJSON(v.app, "json", obj)
}

func (v *CallView) loadQoS() {
	v.qosLoaded = true
	v.qos.SetLoading(true)
	ref := *v.exportRef()
	v.app.Async(func(ctx context.Context) func() {
		q, err := v.app.client.QoS(ctx, ref)
		return func() {
			v.qos.SetLoading(false)
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			var rows []Row
			for i, m := range q.RTCP.Data {
				rows = append(rows, qosRow(m, "RTCP", i, v.app.loc))
			}
			for i, m := range q.RTP.Data {
				rows = append(rows, qosRow(m, "RTP", i, v.app.loc))
			}
			v.qos.SetRows(rows)
			v.qosData = q
			v.showQoS()
			if v.tab == tabQoS {
				v.redrawDetail()
			}
			if len(rows) == 0 {
				v.app.Flash().Info(T("no RTCP/RTP reports for this call"))
			}
		}
	})
}

// showLogs shows the logs as a table, or whole and wrapped, as w chose.
func (v *CallView) showLogs() {
	if !v.app.flow.logWrap {
		v.logPages.SwitchToPage("table")
		return
	}
	v.logPages.SwitchToPage("text")
	var b strings.Builder
	for _, m := range v.logItems {
		fmt.Fprintf(&b, "%s%s  %s → %s[-]\n%s\n\n", theme.Tag(theme.Dim), fmtTime(itemTime(m), v.app.loc),
			tview.Escape(endpoint(m, "", "srcIp", "srcPort")), tview.Escape(endpoint(m, "", "dstIp", "dstPort")),
			tview.Escape(strings.TrimSpace(str(m, "raw"))))
	}
	if len(v.logItems) == 0 {
		b.WriteString(tui.Faint(T("no HEP logs for this call")))
	}
	v.logText.SetText(b.String())
}

// showQoS shows the reports as a table or as graphs, as v chose.
func (v *CallView) showQoS() {
	if !v.app.flow.qosChart {
		v.qosPages.SwitchToPage("table")
		return
	}
	v.qosPages.SwitchToPage("chart")
	if v.qosData == nil {
		v.qosChart.SetText(tui.Faint(T("loading…")))
		return
	}
	data := append([]map[string]any(nil), v.qosData.RTCP.Data...)
	addr := func(ip string, port any) string { return fmt.Sprintf("%s:%v", ip, port) }
	plan := v.mediaPlan()
	legs := qosLegs(qosStreams(data, addr), plan)
	v.qosHops = qosHops(legs, plan.owners)
	v.qosChart.SetText(qosPath(legs, plan, v.qosW, v.app.loc))
}

// qosLegend says how the graphs are read.
func qosLegend() string {
	return T("The scheme: the hosts of the media in the order of the call, the caller on the left, named by the host that announced their addresses in SDP; a relay with two addresses is one column. The upper row is the sound the callee hears, the lower the sound the caller hears; each arrow says what the receiver of that hop reported in RTCP: MOS and the worst loss, or no data when it sent no reports (a relay such as rtpengine usually does not).") + "\n\n" +
		T("Trouble names, for each way, the first hop on the way of the sound where it goes bad: what breaks there carries on to the hops after it.") + "\n" +
		T("MOS ≈: an estimate by the simplified E-model from loss, jitter and RTT; fair under 3.6, poor under 3.1") + "\n" +
		T("Over time: a column is the worst report in it, · no report; loss in %, jitter average/worst in ms, RTT the worst in ms")
}

func qosRow(m map[string]any, kind string, i int, loc *time.Location) Row {
	var rep map[string]any
	_ = json.Unmarshal([]byte(str(m, "raw")), &rep)
	get := func(path ...string) string {
		for _, p := range path {
			if v := homer.Lookup(rep, p); v != nil {
				return homer.Stringify(v)
			}
		}
		return ""
	}
	var block map[string]any
	if rb, ok := rep["report_blocks"].([]any); ok && len(rb) > 0 {
		block, _ = rb[0].(map[string]any)
	}
	fromBlock := func(k string) string {
		if block == nil {
			return ""
		}
		return homer.Stringify(block[k])
	}
	lost := fromBlock("packets_lost")
	if lost == "" {
		lost = get("PACKET_LOSS", "packets_lost", "lost")
	}
	fraction := fromBlock("fraction_lost")
	jitter := fromBlock("ia_jitter")
	if jitter == "" {
		jitter = get("JITTER", "jitter", "MAX_JITTER")
	}
	cells := []string{
		fmtTime(itemTime(m), loc), kind,
		endpoint(m, "", "srcIp", "srcPort"), endpoint(m, "", "dstIp", "dstPort"),
		get("ssrc", "SSRC"), get("sender_information.packets", "TOTAL_PK", "packets"),
		lost, fraction, jitter, get("MOS", "mos", "CODEC_MOS"),
	}
	color := theme.Fg
	if fraction != "" && fraction != "0" || lost != "" && lost != "0" {
		color = theme.Warn
	}
	return Row{Cells: cells, Color: color, Data: m, Key: fmt.Sprintf("%s%d", kind, i)}
}

func (v *CallView) loadLogs() {
	v.logsLoaded = true
	v.logs.SetLoading(true)
	ref := *v.exportRef()
	v.app.Async(func(ctx context.Context) func() {
		items, err := v.app.client.Logs(ctx, ref)
		return func() {
			v.logs.SetLoading(false)
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			rows := make([]Row, 0, len(items))
			for i, m := range items {
				msg := strings.Join(strings.Fields(str(m, "raw")), " ")
				rows = append(rows, Row{
					Cells: []string{fmtTime(itemTime(m), v.app.loc), endpoint(m, "", "srcIp", "srcPort"),
						endpoint(m, "", "dstIp", "dstPort"), msg},
					Data: m, Key: fmt.Sprint(i),
				})
			}
			v.logs.SetRows(rows)
			v.logItems = items
			v.showLogs()
			if v.tab == tabLogs {
				v.redrawDetail()
			}
			if len(rows) == 0 {
				v.app.Flash().Info(T("no HEP logs for this call"))
			}
		}
	})
}
