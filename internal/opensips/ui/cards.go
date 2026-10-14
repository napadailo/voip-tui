package ui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// cards lays out a row of a resource as a card; the other resources show
// the row as JSON.
var cards = map[string]func(data map[string]any) *tui.Card{
	"dialogs":    dialogCard,
	"contacts":   contactCard,
	"dispatcher": destinationCard,
	"gateways":   gatewayCard,
}

// fields fills a card from an MI object: each key it shows is remembered,
// and rest lists the keys no section took, under their OpenSIPS names, so
// that nothing of the answer is hidden.
type fields struct {
	*tui.Card
	m     map[string]any
	shown map[string]bool
}

func newFields(title string, m map[string]any) *fields {
	return &fields{Card: tui.NewCard(title), m: m, shown: map[string]bool{}}
}

// val is the value of the first of keys that is set, "" if none.
func (f *fields) val(keys ...string) string {
	for _, k := range keys {
		f.shown[k] = true
	}
	for _, k := range keys {
		if s := strings.TrimSpace(opensips.Str(f.m[k])); s != "" {
			return s
		}
	}
	return ""
}

// kv adds a translated label with the value of keys, raw adds an OpenSIPS
// or SIP name as it is; both skip a value that is not set.
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

// sdp adds an SDP body, one line of it per card line.
func (f *fields) sdp(label, key string) {
	body := f.val(key)
	if body == "" {
		return
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return
	}
	f.KV(label, tview.Escape(lines[0]))
	for _, l := range lines[1:] {
		f.Line(tview.Escape(l))
	}
}

// attrs adds attributes ("carrier=a;cps=50") as a section of key and value
// lines; a string that is not such pairs is shown as it is.
func (f *fields) attrs(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	f.Section(N("Attributes"))
	pairs, ok := splitAttrs(s)
	if !ok {
		f.Line(tview.Escape(s))
		return
	}
	for _, p := range pairs {
		f.KVRaw(p[0], tview.Escape(p[1]))
	}
}

// splitAttrs reads "k=v;k2=v2" (or with commas when there is no ";") as
// pairs; it reports false when a part is not k=v.
func splitAttrs(s string) ([][2]string, bool) {
	sep := ";"
	if !strings.Contains(s, ";") && strings.Contains(s, ",") {
		sep = ","
	}
	var out [][2]string
	for _, part := range strings.Split(s, sep) {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, false
		}
		out = append(out, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
	}
	return out, len(out) > 0
}

// restHere adds the scalar keys of f.m no line showed to the current
// section, under their OpenSIPS names: the fields of one callee.
func (f *fields) restHere() {
	var keys []string
	for k, v := range f.m {
		switch v.(type) {
		case map[string]any, []any:
			continue
		}
		if !f.shown[k] && strings.TrimSpace(opensips.Str(v)) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		f.shown[k] = true
		f.KVRaw(k, tview.Escape(opensips.Str(f.m[k])))
	}
}

// rest adds the scalar keys no section showed.
func (f *fields) rest() {
	var keys []string
	for k, v := range f.m {
		switch v.(type) {
		case map[string]any, []any:
			continue
		}
		if !f.shown[k] && strings.TrimSpace(opensips.Str(v)) != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	f.Section(N("Other"), T("y: the row as JSON"))
	for _, k := range keys {
		f.KVRaw(k, tview.Escape(opensips.Str(f.m[k])))
	}
}

// cellMarkup is a table cell as card markup, in the color of its level.
func cellMarkup(c opensips.Cell) string {
	if col := levelColor(c.Level); col != 0 {
		return tui.Paint(col, c.Text)
	}
	return tview.Escape(c.Text)
}

// at is a unix time as the clock time and how far it is from now.
func at(sec int64, now time.Time) string {
	t := time.Unix(sec, 0)
	d := now.Unix() - sec
	rel := Tf("%s ago", opensips.Duration(d))
	if d < 0 {
		rel = Tf("in %s", opensips.Duration(-d))
	}
	return tview.Escape(t.Format("2006-01-02 15:04:05")) + tui.Faint("  "+rel)
}

func dialogCard(d map[string]any) *tui.Card {
	now := time.Now()
	f := newFields(opensips.Str(d["callid"]), d)
	f.shown["callid"] = true
	state := opensips.DialogState(opensips.Num(d["state"]))
	sub := []string{"ID " + opensips.Str(d["ID"])}
	if start := opensips.Num(d["timestart"]); start > 0 {
		sub = append(sub, opensips.Duration(now.Unix()-start))
	}
	f.Sub(sub...)

	f.Section(N("Dialog"))
	f.KV(N("state"), cellMarkup(state)+tui.Faint("  "+opensips.Str(d["state"])))
	f.shown["state"] = true
	if start := opensips.Num(f.val("timestart")); start > 0 {
		f.KV(N("started"), at(start, now))
	} else {
		f.KV(N("started"), tui.Faint(T("not answered yet")))
	}
	if to := opensips.Num(f.val("timeout")); to > 0 {
		f.KV(N("expires"), at(to, now))
	}
	f.shown["ID"] = true // in the line under the title
	if e, id := f.val("h_entry"), f.val("h_id"); e != "" {
		f.KVRaw("h_entry:h_id", e+":"+id)
	}
	f.raw("user_flags", "user_flags")

	f.Section(N("Caller"))
	f.raw("URI", "from_uri")
	f.raw("tag", "caller_tag")
	f.raw("Contact", "caller_contact")
	f.raw("CSeq", "caller_cseq")
	f.raw("Route", "caller_route_set")
	f.kv(N("socket"), "caller_bind_addr")
	f.shown["caller_sdp"], f.shown["caller_sent_sdp"] = true, true // in Caller SDP below

	callees := opensips.AsList(d["CALLEES"])
	f.shown["CALLEES"] = true
	if callees == nil && f.val("callee_tag", "callee_contact") != "" {
		callees = []any{d} // before 3.0 the callee is flat in the dialog
	}
	if len(callees) == 0 {
		f.Section(N("Callee"))
		f.raw("URI", "to_uri")
	}
	for i, x := range callees {
		c := &fields{Card: f.Card, m: opensips.AsMap(x), shown: f.shown}
		if len(callees) > 1 {
			c.Section(N("Callee"), strconv.Itoa(i+1))
		} else {
			c.Section(N("Callee"))
		}
		if i == 0 {
			f.raw("URI", "to_uri")
		}
		c.raw("tag", "callee_tag")
		c.raw("Contact", "callee_contact")
		c.raw("CSeq", "callee_cseq")
		c.raw("Route", "callee_route_set")
		c.kv(N("socket"), "callee_bind_addr")
		c.shown["callee_sdp"], c.shown["callee_sent_sdp"] = true, true // in Callee SDP below
		if opensips.AsMap(x)["callid"] == nil {                        // a callee of its own, not the flat 2.x dialog
			c.restHere()
		}
	}

	// the SDP bodies after the parties: what OpenSIPS received from a side
	// and what it sent to it (after rtpengine, say)
	sdps := func(title, note, recv, sent string, m map[string]any) {
		if opensips.Str(m[recv]) == "" && opensips.Str(m[sent]) == "" {
			return
		}
		s := &fields{Card: f.Card, m: m, shown: f.shown}
		s.Section(title, note)
		s.sdp(N("received SDP"), recv)
		s.sdp(N("sent SDP"), sent)
	}
	sdps(N("Caller SDP"), "", "caller_sdp", "caller_sent_sdp", d)
	for i, x := range callees {
		note := ""
		if len(callees) > 1 {
			note = strconv.Itoa(i + 1)
		}
		sdps(N("Callee SDP"), note, "callee_sdp", "callee_sent_sdp", opensips.AsMap(x))
	}
	f.rest()
	return f.Card
}

func contactCard(ct map[string]any) *tui.Card {
	f := newFields(opensips.Str(ct["Contact"]), ct)
	f.shown["Contact"] = true
	f.Sub("AOR "+f.val("AOR"), Tf("table %s", f.val("table")))

	f.Section(N("Registration"))
	if e, err := strconv.ParseInt(f.val("Expires"), 10, 64); err == nil {
		v := tview.Escape(opensips.Duration(e))
		if e < 60 {
			v = tui.Paint(theme.Warn, opensips.Duration(e))
		}
		f.KV(N("expires in"), v)
	}
	f.raw("Call-ID", "Callid")
	f.raw("CSeq", "Cseq")
	f.raw("User-Agent", "User-agent")
	f.raw("Q", "Q")
	contactState(f)
	contactMethods(f)
	contactFlags(f)
	f.raw("cflags", "Cflags")
	f.kv(N("attributes"), "Attr")
	f.raw("ContactID", "ContactID")

	f.Section(N("Network"))
	f.kv(N("received from"), "Received")
	f.kv(N("socket"), "Socket")
	f.raw("Path", "Path")
	f.raw("+sip.instance", "SIP_instance")
	f.raw("reg-id", "Reg-ID")
	f.rest()
	return f.Card
}

func destinationCard(d map[string]any) *tui.Card {
	f := newFields(opensips.Str(d["URI"]), d)
	f.shown["URI"] = true
	f.Sub(Tf("set %s", f.val("set")), Tf("partition %s", f.val("partition")))
	f.Section(N("Destination"))
	if s := f.val("state"); s != "" {
		f.KV(N("state"), cellMarkup(opensips.StateCell(s)))
	}
	f.kv(N("weight"), "weight")
	f.kv(N("priority"), "priority")
	f.kv(N("description"), "description")
	f.kv(N("socket"), "socket")
	f.raw("flags", "flags")
	f.attrs(f.val("attributes", "attrs"))
	f.rest()
	return f.Card
}

func gatewayCard(g map[string]any) *tui.Card {
	f := newFields(opensips.Str(g["ID"]), g)
	f.shown["ID"] = true
	part := f.val("partition")
	if part == "" {
		part = T("default")
	}
	f.Sub(f.val("IP"), Tf("partition %s", part))
	f.Section(N("Gateway"))
	f.raw("IP", "IP")
	if s := f.val("State"); s != "" {
		f.KV(N("state"), cellMarkup(opensips.StateCell(s)))
	}
	f.attrs(f.val("ATTRS"))
	f.rest()
	return f.Card
}

// newCardView shows a card; y switches it to data as JSON and back. build
// runs again on a refresh, so that :lang applies to an open card.
func newCardView(app *App, name string, build func() *tui.Card, data any) *TextViewer {
	tv := newTextViewer(app, name, "markup")
	showRaw := false
	tv.Loader = func() {
		if showRaw {
			tv.SetContent(toJSON(data), "json")
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

// contactState explains State: how the contact stands with the usrloc
// database (cstate_t in modules/usrloc/ucontact.h).
func contactState(f *fields) {
	st := f.val("State")
	if st == "" {
		return
	}
	meaning := map[string]string{
		"CS_SYNC":  T("written to the database"),
		"CS_NEW":   T("new, not written to the database yet"),
		"CS_DIRTY": T("changed, not written to the database yet"),
	}[st]
	if meaning == "" {
		meaning = T("unknown")
	}
	f.KV(N("state"), tview.Escape(st)+tui.Faint("  "+meaning))
}

// contactMethods lists the methods the contact accepts: its Contact
// methods parameter or the Allow header of the REGISTER.
func contactMethods(f *fields) {
	raw := f.val("Methods")
	if raw == "" {
		return
	}
	names, all := opensips.Methods(opensips.Num(raw))
	switch {
	case all:
		f.KV(N("methods"), T("all: the REGISTER had no Allow")+tui.Faint("  "+raw))
	case len(names) == 0:
		f.KV(N("methods"), tui.Faint("-  "+raw))
	default:
		f.KV(N("methods"), tview.Escape(strings.Join(names, ", "))+tui.Faint("  "+raw))
	}
}

// contactFlags names the usrloc flags of the contact (ucontact.h).
func contactFlags(f *fields) {
	raw := f.val("Flags")
	if raw == "" {
		return
	}
	flags, rest := opensips.ContactFlags(opensips.Num(raw))
	if len(flags) == 0 && rest == 0 {
		f.KV(N("flags"), tui.Faint(T("none")))
		return
	}
	meaning := map[string]string{
		"FL_MEM":       T("in memory only, not written to the database"),
		"FL_EXTRA_HOP": T("reached through another node of the cluster"),
		"FL_PN_ON":     T("push notifications (RFC 8599)"),
	}
	var lines []string
	for _, fl := range flags {
		lines = append(lines, tview.Escape(fl.Name)+tui.Faint("  "+meaning[fl.Name]))
	}
	if rest != 0 {
		lines = append(lines, tview.Escape(strconv.FormatInt(rest, 10))+tui.Faint("  "+T("unknown bits")))
	}
	f.KV(N("flags"), lines[0])
	for _, l := range lines[1:] {
		f.Line(l)
	}
}
