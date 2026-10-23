package ui

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui"
)

// newCardView shows a card; y switches it to data as JSON and back. build
// runs again on a refresh, so that :lang applies to an open card.
func newCardView(app *App, name string, build func() *tui.Card, data func() any) *TextViewer {
	tv := newTextViewer(app, name, "markup")
	showRaw := false
	tv.Loader = func() {
		if showRaw {
			tv.SetContent(toJSON(data()), "json")
			return
		}
		tv.SetContent(build().String(), "markup")
	}
	tv.Keys['y'] = func() {
		showRaw = !showRaw
		tv.Loader()
		tv.Text().ScrollToBeginning() // the other layout has other lines
	}
	tv.Extra = []Hint{{Key: "y", Desc: N("JSON/parsed")}}
	tv.Loader()
	return tv
}

// pushJSON opens data as JSON over the current view; y there closes it,
// as in the tables of rtpengine-tui and opensips-tui.
func pushJSON(app *App, name string, data any) {
	tv := newTextViewer(app, name, "json")
	tv.SetContent(toJSON(data), "json")
	tv.Keys['y'] = app.Pop
	tv.Extra = []Hint{{Key: "y", Desc: N("back")}}
	app.Push(tv)
}

// fields fills a card from an item: each key a line shows is remembered,
// rest lists the others under their own names, so nothing is hidden.
type fields struct {
	*tui.Card
	m     map[string]any
	shown map[string]bool
}

func newFields(title string, m map[string]any) *fields {
	return &fields{Card: tui.NewCard(title), m: m, shown: map[string]bool{}}
}

func (f *fields) val(keys ...string) string {
	for _, k := range keys {
		f.shown[k] = true
	}
	for _, k := range keys {
		if s := strings.TrimSpace(homer.Stringify(f.m[k])); s != "" {
			return s
		}
	}
	return ""
}

// kv adds a translated label, raw a protocol or Homer name as it is; both
// skip a value that is not set.
func (f *fields) kv(label string, keys ...string) {
	if s := f.val(keys...); s != "" {
		f.KV(label, tview.Escape(s))
	}
}

func (f *fields) raw(label string, keys ...string) {
	if s := f.val(keys...); s != "" {
		f.KVRaw(label, tview.Escape(s))
	}
}

// rest adds the keys no line showed: values as they are, objects and
// lists as compact JSON.
func (f *fields) rest() {
	var keys []string
	for k, v := range f.m {
		if !f.shown[k] && strings.TrimSpace(homer.Stringify(v)) != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	f.Section(N("Other"), T("y: as JSON"))
	for _, k := range keys {
		v := f.m[k]
		s := homer.Stringify(v)
		switch v.(type) {
		case map[string]any, []any:
			b, _ := json.Marshal(v)
			s = string(b)
		}
		f.KVRaw(k, tview.Escape(tui.Trunc(s, 200)))
	}
}

// messageCard is one captured message of a search: when, what, between
// whom, and its fields.
func messageCard(m map[string]any, loc *time.Location) *tui.Card {
	title := str(m, "method")
	if title == "" {
		title = str(m, "sid")
	}
	f := newFields(title, m)
	f.Sub(fmtTime(itemTime(m), loc), str(m, "sid"))
	for _, k := range []string{"timeSeconds", "timeUseconds", "create_date", "srcIp", "srcPort", "dstIp", "dstPort",
		"aliasSrc", "aliasDst", "protocol", "raw"} {
		f.shown[k] = true
	}
	f.Section(N("Message"))
	f.raw("method", "method")
	f.raw("From", "from_user")
	f.raw("To", "to_user")
	f.raw("RURI", "ruri_user", "ruri_domain")
	f.KV(N("source"), tview.Escape(endpoint(m, "aliasSrc", "srcIp", "srcPort")))
	f.KV(N("destination"), tview.Escape(endpoint(m, "aliasDst", "dstIp", "dstPort")))
	if p := protoName(m); p != "" {
		f.KV(N("protocol"), tview.Escape(p))
	}
	f.raw("Call-ID", "sid", "callid")
	f.raw("CSeq", "cseq")
	f.raw("User-Agent", "user_agent")
	f.kv(N("node"), "dbnode")
	if raw := str(m, "raw"); raw != "" {
		f.Section(N("Text"))
		for _, l := range strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				f.Line(tview.Escape(l))
			}
		}
	}
	f.rest()
	return f.Card
}

// sessionCard is a call or a registration grouped by its session id: the
// row's status and duration, then its messages in order.
func sessionCard(g *sessionGroup, loc *time.Location) *tui.Card {
	row := g.row(loc)
	c := row.Cells
	card := tui.NewCard(g.sid).Sub(c[0], Tf("%d messages", len(g.items)))
	card.Section(N("Session"))
	card.KV(N("status"), tview.Escape(c[1]))
	for _, l := range []struct{ label, value string }{{"From", c[2]}, {"To", c[3]}, {"RURI", c[4]}} {
		if l.value != "" {
			card.KVRaw(l.label, tview.Escape(l.value))
		}
	}
	if c[5] != "" {
		card.KV(N("duration"), tview.Escape(c[5]))
	}
	card.KV(N("source"), tview.Escape(c[7]))
	card.KV(N("destination"), tview.Escape(c[8]))
	if c[9] != "" {
		card.KVRaw("User-Agent", tview.Escape(c[9]))
	}
	if c[11] != "" {
		card.KV(N("node"), tview.Escape(c[11]))
	}
	card.Section(N("Messages"), T("enter in the list: the call with its flow"))
	for _, it := range g.items {
		line := tview.Escape(str(it, "method")) + tui.Faint("  "+endpoint(it, "aliasSrc", "srcIp", "srcPort")+" → "+endpoint(it, "aliasDst", "dstIp", "dstPort"))
		card.KVRaw(itemTime(it).In(loc).Format("15:04:05.000"), line)
	}
	return card
}

// resourceCard is an item of a settings view: the view's columns first,
// under their headers, then the other fields.
func resourceCard(res *homer.Resource, item map[string]any, key string) *tui.Card {
	title := key
	if len(res.Columns) > 0 {
		if s := homer.Stringify(homer.Lookup(item, res.Columns[0].Field)); s != "" {
			title = s
		}
	}
	f := newFields(title, item)
	f.Sub(T(res.Title), key)
	f.Section(T(res.Title))
	for _, c := range res.Columns {
		top, _, _ := strings.Cut(c.Field, ".")
		f.shown[top] = !strings.Contains(c.Field, ".") // a nested field: the object stays below too
		if s := homer.Stringify(homer.Lookup(item, c.Field)); s != "" {
			f.KVRaw(strings.ToLower(T(c.Title)), tview.Escape(s))
		}
	}
	f.rest()
	return f.Card
}
