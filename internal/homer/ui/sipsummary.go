package ui

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// sipHeader is one header line of a message, its compact name expanded.
type sipHeader struct{ name, value string }

// sipParsed is a SIP message split into its start line, headers in their
// order and body.
type sipParsed struct {
	start   string
	headers []sipHeader
	body    string
}

// compactNames are the one-letter header names of RFC 3261 and its
// extensions.
var compactNames = map[string]string{
	"i": "Call-ID", "f": "From", "t": "To", "v": "Via", "m": "Contact", "l": "Content-Length",
	"c": "Content-Type", "k": "Supported", "s": "Subject", "e": "Content-Encoding", "o": "Event",
	"r": "Refer-To", "b": "Referred-By", "u": "Allow-Events", "x": "Session-Expires", "y": "Identity",
}

func parseSIP(raw string) sipParsed {
	var p sipParsed
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i, l := range lines {
		switch {
		case i == 0:
			p.start = strings.TrimSpace(l)
		case strings.TrimSpace(l) == "":
			p.body = strings.Join(lines[i+1:], "\n")
			return p
		case (l[0] == ' ' || l[0] == '\t') && len(p.headers) > 0: // folded
			p.headers[len(p.headers)-1].value += " " + strings.TrimSpace(l)
		default:
			name, val, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			name = strings.TrimSpace(name)
			if full, ok := compactNames[strings.ToLower(name)]; ok {
				name = full
			}
			p.headers = append(p.headers, sipHeader{name, strings.TrimSpace(val)})
		}
	}
	return p
}

// get returns the values of a header, each comma-separated value on its
// own when split is set.
func (p sipParsed) get(name string, split bool) []string {
	var out []string
	for _, h := range p.headers {
		if !strings.EqualFold(h.name, name) {
			continue
		}
		if split {
			out = append(out, splitList(h.value)...)
		} else {
			out = append(out, h.value)
		}
	}
	return out
}

func (p sipParsed) first(name string) string {
	if v := p.get(name, false); len(v) > 0 {
		return v[0]
	}
	return ""
}

// splitList splits a header value on the commas outside quotes and <>.
func splitList(s string) []string {
	var out []string
	depth, quoted, from := 0, false, 0
	for i, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '<':
			depth++
		case r == '>':
			depth--
		case r == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[from:i]))
			from = i + 1
		}
	}
	return append(out, strings.TrimSpace(s[from:]))
}

// uriShort is user@host of a SIP or tel URI, without scheme and params.
func uriShort(uri string) string {
	uri = strings.Trim(strings.TrimSpace(uri), "<>")
	for _, s := range []string{"sips:", "sip:", "tel:"} {
		if len(uri) >= len(s) && strings.EqualFold(uri[:len(s)], s) {
			uri = uri[len(s):]
			break
		}
	}
	uri, _, _ = strings.Cut(uri, ";")
	uri, _, _ = strings.Cut(uri, "?")
	return uri
}

// party is a name-addr as "Display" user@host and the params after it.
func party(v string) (who string, params map[string]string) {
	params = map[string]string{}
	display, addr := "", v
	if i := strings.Index(v, "<"); i >= 0 {
		display = strings.Trim(strings.TrimSpace(v[:i]), `"`)
		if j := strings.Index(v[i:], ">"); j >= 0 {
			addr = v[i : i+j+1]
			v = v[i+j+1:]
		}
	} else {
		addr, v, _ = strings.Cut(v, ";")
		v = ";" + v
	}
	for _, kv := range strings.Split(v, ";") {
		if k, val, ok := strings.Cut(strings.TrimSpace(kv), "="); ok {
			params[strings.ToLower(k)] = strings.Trim(val, `"`)
		}
	}
	who = uriShort(addr)
	if display != "" {
		who = `"` + display + `" ` + who
	}
	return who, params
}

var viaRe = regexp.MustCompile(`(?i)^SIP\s*/\s*2\.0\s*/\s*(\S+)\s+([^;\s]+)`)

// sipSummary is the message in plain words: who calls whom, the way it
// took, the media, the session rules and how it ended. alias names an
// address the way the flow does.
func sipSummary(raw string, alias func(hostport string) string) *tui.Card {
	p := parseSIP(raw)
	card := &summary{Card: tui.NewCard(p.start)}
	request := !strings.HasPrefix(strings.ToUpper(p.start), "SIP/")

	card.open(N("Parties"))
	if request {
		if f := strings.Fields(p.start); len(f) >= 2 {
			card.kv(N("request URI"), tview.Escape(uriShort(f[1])))
		}
	}
	for _, name := range []string{"From", "To"} {
		if who, _ := party(p.first(name)); who != "" {
			card.raw(name, tview.Escape(who))
		}
	}
	for _, name := range []string{"P-Asserted-Identity", "P-Preferred-Identity", "Remote-Party-ID", "Diversion", "History-Info", "Referred-By", "Refer-To"} {
		var vals []string
		for _, v := range p.get(name, true) {
			who, params := party(v)
			if r := params["reason"]; r != "" && strings.EqualFold(name, "Diversion") {
				who += " (" + r + ")"
			}
			vals = append(vals, who)
		}
		if len(vals) > 0 {
			card.raw(name, tview.Escape(strings.Join(vals, ", ")))
		}
	}
	for _, name := range []string{"User-Agent", "Server"} {
		if v := p.first(name); v != "" {
			card.raw(name, tview.Escape(v))
		}
	}

	card.open(N("Route"), T("first hop first"))
	if vias := p.get("Via", true); len(vias) > 0 {
		var hops []string
		transport := ""
		for i := len(vias) - 1; i >= 0; i-- { // the bottom Via is the first hop
			m := viaRe.FindStringSubmatch(vias[i])
			if m == nil {
				continue
			}
			transport = strings.ToUpper(m[1])
			hops = append(hops, alias(m[2]))
		}
		card.raw("Via", tview.Escape(transport+": "+strings.Join(hops, " → ")))
	}
	for _, name := range []string{"Record-Route", "Route"} {
		var hops []string
		for _, v := range p.get(name, true) {
			hops = append(hops, alias(uriShort(v)))
		}
		if name == "Record-Route" { // each proxy puts itself on top
			slices.Reverse(hops)
		}
		if len(hops) > 0 {
			card.raw(name, tview.Escape(strings.Join(hops, " → ")))
		}
	}
	if who, _ := party(p.first("Contact")); who != "" {
		card.raw("Contact", tview.Escape(who))
	}
	if v := p.first("Max-Forwards"); v != "" {
		card.raw("Max-Forwards", tview.Escape(v))
	}

	if strings.Contains(p.body, "m=") {
		card.open(N("Media"), T("SDP"))
		for _, m := range sdpMedia(p.body) {
			card.raw(m.kind, m.line)
		}
	}

	var rules []sipHeader
	for _, name := range []string{"Session-Expires", "Min-SE", "Require", "Supported", "Allow", "Expires", "Event", "Subscription-State"} {
		if v := p.get(name, false); len(v) > 0 {
			rules = append(rules, sipHeader{name, strings.Join(v, ", ")})
		}
	}
	if len(rules) > 0 {
		card.open(N("Session"))
		for _, h := range rules {
			card.raw(h.name, tview.Escape(h.value))
		}
	}

	reasons, warnings := p.get("Reason", true), p.get("Warning", true)
	if !request || len(reasons) > 0 || len(warnings) > 0 {
		card.open(N("Result"))
		if !request {
			if f := strings.Fields(p.start); len(f) >= 2 {
				code, _ := strconv.Atoi(f[1])
				line := tview.Escape(strings.Join(f[1:], " "))
				if m := codeMeaning(code); m != "" {
					line += tui.Faint(" - " + m)
				}
				card.kv(N("answer"), theme.Tag(methodColor(f[1]))+line+"[-]")
			}
		}
		for _, r := range reasons {
			card.raw("Reason", tview.Escape(reasonText(r)))
		}
		for _, w := range warnings {
			card.raw("Warning", tview.Escape(w))
		}
	}
	return card.Card
}

// summary is a card whose sections show up only once they have a line.
type summary struct {
	*tui.Card
	section, note string
}

func (s *summary) open(name string, note ...string) {
	s.section, s.note = name, strings.Join(note, " ")
}

func (s *summary) flush() {
	if s.section != "" {
		s.Card.Section(s.section, s.note)
		s.section = ""
	}
}

func (s *summary) kv(label, value string)  { s.flush(); s.Card.KV(label, value) }
func (s *summary) raw(label, value string) { s.flush(); s.Card.KVRaw(label, value) }

type sdpLine struct{ kind, line string }

// staticPT names the static RTP payload types of RFC 3551.
var staticPT = map[string]string{"0": "PCMU", "3": "GSM", "4": "G723", "8": "PCMA", "9": "G722", "13": "CN", "18": "G729"}

// sdpStream is one m= of an SDP body: its address, port, codecs by name,
// direction and ptime, the session's address and direction where it has
// none of its own.
type sdpStream struct {
	kind, addr, port, dir, ptime string
	codecs                       []string
}

// sdpStreams reads the m= lines of an SDP body.
func sdpStreams(body string) []sdpStream {
	sessionAddr, sessionDir := "", "sendrecv"
	type media struct {
		kind, port, addr, dir, ptime string
		pts                          []string
		names                        map[string]string
	}
	var cur *media
	var all []*media
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		switch k {
		case "c":
			if f := strings.Fields(v); len(f) >= 3 {
				if cur != nil {
					cur.addr = f[2]
				} else {
					sessionAddr = f[2]
				}
			}
		case "m":
			f := strings.Fields(v)
			if len(f) < 3 {
				continue
			}
			cur = &media{kind: f[0], port: f[1], pts: f[3:], names: map[string]string{}}
			all = append(all, cur)
		case "a":
			name, val, _ := strings.Cut(v, ":")
			switch {
			case name == "sendrecv" || name == "sendonly" || name == "recvonly" || name == "inactive":
				if cur != nil {
					cur.dir = name
				} else {
					sessionDir = name
				}
			case name == "rtpmap" && cur != nil:
				if pt, enc, ok := strings.Cut(val, " "); ok {
					enc, _, _ = strings.Cut(enc, "/")
					cur.names[pt] = enc
				}
			case name == "ptime" && cur != nil:
				cur.ptime = val
			}
		}
	}
	var out []sdpStream
	for _, m := range all {
		st := sdpStream{kind: m.kind, addr: m.addr, port: m.port, dir: m.dir, ptime: m.ptime}
		if st.addr == "" {
			st.addr = sessionAddr
		}
		if st.dir == "" {
			st.dir = sessionDir
		}
		for _, pt := range m.pts {
			switch {
			case m.names[pt] != "":
				st.codecs = append(st.codecs, m.names[pt])
			case staticPT[pt] != "":
				st.codecs = append(st.codecs, staticPT[pt])
			default:
				st.codecs = append(st.codecs, pt)
			}
		}
		out = append(out, st)
	}
	return out
}

// held tells a stream on hold.
func (st sdpStream) held() bool {
	return st.dir == "sendonly" || st.dir == "inactive" || st.addr == "0.0.0.0"
}

// sdpMedia sums up each m= of an SDP body: address, codecs by name,
// direction and ptime; a held stream is marked.
func sdpMedia(body string) []sdpLine {
	var out []sdpLine
	for _, st := range sdpStreams(body) {
		parts := []string{st.addr + ":" + st.port, strings.Join(st.codecs, " "), st.dir}
		if st.ptime != "" {
			parts = append(parts, "ptime "+st.ptime)
		}
		line := tview.Escape(strings.Join(parts, " · "))
		switch {
		case st.port == "0":
			line += " · " + theme.Tag(theme.Warn) + T("rejected") + "[-]"
		case st.held():
			line += " · " + theme.Tag(theme.Warn) + T("on hold") + "[-]"
		}
		out = append(out, sdpLine{st.kind, line})
	}
	return out
}

// q850 names the ISDN causes that carry over to SIP in Reason headers.
var q850 = map[int]string{
	1: "Unallocated number", 2: "No route to specified transit network", 3: "No route to destination",
	16: "Normal call clearing", 17: "User busy", 18: "No user responding", 19: "No answer from user",
	20: "Subscriber absent", 21: "Call rejected", 22: "Number changed", 27: "Destination out of order",
	28: "Invalid number format", 29: "Facility rejected", 31: "Normal, unspecified", 34: "No circuit/channel available",
	38: "Network out of order", 41: "Temporary failure", 42: "Switching equipment congestion",
	44: "Requested circuit/channel not available", 47: "Resource unavailable, unspecified",
	57: "Bearer capability not authorized", 58: "Bearer capability not presently available",
	65: "Bearer capability not implemented", 79: "Service or option not implemented",
	88: "Incompatible destination", 102: "Recovery on timer expiry", 111: "Protocol error, unspecified",
	127: "Interworking, unspecified",
}

// reasonText reads a Reason value: the protocol, the cause with its
// Q.850 name, and the text.
func reasonText(v string) string {
	proto, rest, _ := strings.Cut(v, ";")
	_, params := party("<x>;" + rest)
	proto = strings.TrimSpace(proto)
	s := proto
	if c := params["cause"]; c != "" {
		s += " " + c
		if n, err := strconv.Atoi(c); err == nil && strings.EqualFold(proto, "Q.850") && q850[n] != "" {
			s += ": " + q850[n]
		}
	}
	if t := params["text"]; t != "" && !strings.HasSuffix(strings.ToLower(s), strings.ToLower(t)) {
		s += fmt.Sprintf(" (%s)", t)
	}
	return s
}

// codeMeaning says what an answer usually means in practice.
func codeMeaning(code int) string {
	switch code {
	case 401, 407:
		return T("authentication asked, usual before the request is sent again with credentials")
	case 403:
		return T("refused: no rights, a wrong password or a blocked number")
	case 404:
		return T("no such user or number")
	case 408:
		return T("no answer in time")
	case 480:
		return T("unavailable: not registered, do not disturb or no answer")
	case 486, 600:
		return T("busy")
	case 487:
		return T("cancelled before the answer")
	case 488, 606:
		return T("the offer is not acceptable: codecs or SDP")
	case 500:
		return T("server error")
	case 502, 503, 504:
		return T("the next hop is down, overloaded or has no route")
	case 603:
		return T("declined")
	}
	return ""
}
