package ui

import (
	"fmt"
	"strings"

	"github.com/napadailo/voip-tui/tui/theme"
)

// flowOpts is how call views draw a call: which kinds of messages they
// show and whether hosts of one alias share a lane. It lasts while
// homer-tui runs, so the next call opens the same way.
type flowOpts struct {
	byAlias  bool
	fold     bool            // no lanes again for a call that comes back
	plain    bool            // SIP messages in plain words instead of as they are
	qosChart bool            // QoS as graphs instead of a table
	logWrap  bool            // the logs whole and wrapped instead of a table
	hide     map[string]bool // msgKind values
}

func (o *flowOpts) hidden(kind string) bool { return o.hide[kind] }

func (o *flowOpts) toggle(kind string) {
	if o.hide == nil {
		o.hide = map[string]bool{}
	}
	o.hide[kind] = !o.hide[kind]
}

// msgKind names a message by its HEP payload type: SIP, RTCP, LOG, or
// HEP and the number. Without the type the arrow's method tells.
func msgKind(m map[string]any, method string) string {
	if pt, ok := m["payloadType"].(float64); ok {
		switch pt {
		case 1:
			return "SIP"
		case 5:
			return "RTCP"
		case 100:
			return "LOG"
		}
		return fmt.Sprintf("HEP %d", int(pt))
	}
	switch method {
	case "LOG", "RTCP":
		return method
	}
	return "SIP"
}

// FlowFilterView switches the kinds of messages of a call on and off and
// the hosts by alias; the call view redraws on each change.
type FlowFilterView struct {
	*TableView
	cv *CallView
}

const (
	aliasRow  = "\x00alias"
	unfoldRow = "\x00unfold"
)

func newFlowFilterView(cv *CallView) *FlowFilterView {
	v := &FlowFilterView{TableView: NewTableView(cv.app, N("flow filter"), []string{N("SHOW"), N("WHAT"), N("COUNT")}), cv: cv}
	toggle := func() {
		r := v.Selected()
		if r == nil {
			return
		}
		switch r.Key {
		case aliasRow:
			cv.app.flow.byAlias = !cv.app.flow.byAlias
		case unfoldRow:
			cv.app.flow.fold = !cv.app.flow.fold
		default:
			cv.app.flow.toggle(r.Key)
		}
		cv.applyFlow()
		v.Refresh()
	}
	v.Bind(' ', toggle)
	v.OnEnter(func(*Row) { toggle() })
	v.Refresh()
	return v
}

func (v *FlowFilterView) Name() string      { return "filter" }
func (v *FlowFilterView) Table() *TableView { return v.TableView }
func (v *FlowFilterView) Close()            {}
func (v *FlowFilterView) Hints() []Hint {
	return []Hint{{Key: "space/enter", Desc: N("on/off")}, {Key: "esc", Desc: N("back to the call")}}
}

func (v *FlowFilterView) Refresh() {
	opts := &v.cv.app.flow
	mark := func(on bool) string {
		if on {
			return "✓"
		}
		return "·"
	}
	var rows []Row
	for _, k := range v.cv.kinds() {
		color := theme.Fg
		if opts.hidden(k.name) || k.n == 0 {
			color = theme.Dim
		}
		rows = append(rows, Row{Cells: []string{mark(!opts.hidden(k.name)), k.name, fmt.Sprint(k.n)}, Key: k.name, Color: color})
	}
	setting := func(on bool, what, key string, off, onN int) {
		color := theme.Dim
		if on {
			color = theme.Fg
		}
		rows = append(rows, Row{Cells: []string{mark(on), what, Tf("%d hosts → %d", off, onN)}, Key: key, Color: color})
	}
	unfold := !opts.fold
	setting(opts.byAlias, T("hosts by alias"), aliasRow, v.cv.laneCount(false, unfold), v.cv.laneCount(true, unfold))
	setting(unfold, T("unfold calls that come back"), unfoldRow, v.cv.laneCount(opts.byAlias, false), v.cv.laneCount(opts.byAlias, true))
	v.SetRows(rows)
}

// flowNote says in the flow title what the filter hides of this call.
func (v *CallView) flowNote() string {
	var hidden []string
	for _, k := range v.kinds() {
		if k.n > 0 && v.app.flow.hidden(k.name) {
			hidden = append(hidden, k.name)
		}
	}
	var parts []string
	if len(hidden) > 0 {
		parts = append(parts, Tf("hidden: %s", strings.Join(hidden, ", ")))
	}
	if v.app.flow.byAlias {
		parts = append(parts, T("by alias"))
	}
	if !v.app.flow.fold && v.laneCount(v.app.flow.byAlias, true) > v.laneCount(v.app.flow.byAlias, false) {
		parts = append(parts, T("unfolded"))
	}
	return strings.Join(parts, " · ")
}
