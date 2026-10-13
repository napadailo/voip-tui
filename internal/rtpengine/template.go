package rtpengine

import (
	"regexp"
	"strings"

	"github.com/napadailo/voip-tui/i18n"
)

// Signalling templates (mr13.2+): each key of the [templates] section of
// rtpengine.conf is a string of NG flags in the rtpp_flags syntax, parsed by
// daemon/control_ng_flags_parser.c the same way as rtpengine_offer("...")
// arguments from Kamailio or OpenSIPS.

type ngKind int

const (
	ngKey     ngKind = iota // key=value
	ngFlag                  // bare flag
	ngReplace               // replace-<what>, replace=[...]
	ngCodec                 // codec-<op>-<codec>, codec=[<op>=[...]]
)

type ngSpec struct {
	Doc      []string // paragraphs of docs/ng_control_protocol.md
	Alias    string   // documented spelling of an undocumented alias
	Branches uint32   // bit set over ngBranches
}

// TemplateItem is one token of a template, explained.
type TemplateItem struct {
	Token   string // as written
	Kind    string // key, flag, replace, codec, transport, direction
	Name    string // key/flag name as documented
	Value   string // key value, list items joined by spaces
	Meaning string // first paragraph of the docs
	Values  []TemplateValue
	Status  Status // StatusOK, StatusUnknown, StatusNotInVersion
	Note    string
}

// TemplateValue explains one value of a key or one list element.
type TemplateValue struct {
	Value   string
	Meaning string
	Status  Status
	Note    string
}

// Template is a parsed [templates] entry.
type Template struct {
	Name  string
	Items []TemplateItem
	// Transport is the transport-protocol the shortcut tokens (RTP, SRTP,
	// AVPF, DTLS…) add up to, "" without shortcuts.
	Transport string
}

// ngCommands are the NG commands; a template named after one is applied to
// that command automatically.
var ngCommands = []string{"ping", "offer", "answer", "delete", "query", "list", "start recording", "stop recording",
	"pause recording", "block DTMF", "unblock DTMF", "block media", "unblock media", "silence media", "unsilence media",
	"start forwarding", "stop forwarding", "play media", "stop media", "play DTMF", "statistics", "publish",
	"subscribe request", "subscribe answer", "unsubscribe", "inject start", "inject stop", "connect", "create",
	"create answer", "mesh"}

// TemplateNamed reports whether a template is applied only where the proxy
// asks for it with template=<name>; the others are applied automatically.
func TemplateNamed(name string) bool {
	if name == "default" {
		return false
	}
	for _, c := range ngCommands {
		if c == name {
			return false
		}
	}
	return true
}

// TemplateUse says how rtpengine applies a template with this name.
func TemplateUse(name string) string {
	switch {
	case name == "default":
		return i18n.T("applied to every signalling message")
	case !TemplateNamed(name):
		return i18n.Tf("applied to every %s message", name)
	}
	return i18n.Tf("used where the proxy passes template=%s", name)
}

// templateTokens splits on spaces, keeping [...] groups together.
func templateTokens(s string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '[':
			depth++
		case r == ']' && depth > 0:
			depth--
		case (r == ' ' || r == '\t') && depth == 0:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// listItems returns the elements of "[a b c]", or the value itself.
func listItems(v string) []string {
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		return templateTokens(v[1 : len(v)-1])
	}
	return []string{v}
}

var transportBits = map[string]int{"RTP/AVP": 0x00, "RTP/SAVP": 0x01, "RTP/AVPF": 0x02, "RTP/SAVPF": 0x03, "UDP/TLS/RTP/SAVP": 0x04, "UDP/TLS/RTP/SAVPF": 0x06}

// codec dictionary keys: as key=value (with or without the codec- prefix
// in the daemon's spelling) and as codec-<op>-<codec> flags
var (
	codecKeys     = map[string]string{"transcode": "transcode", "codec-transcode": "transcode", "codec-strip": "strip", "codec-offer": "offer", "codec-mask": "mask", "codec-set": "set", "codec-accept": "accept", "codec-except": "except"}
	codecPrefixes = []struct{ prefix, op string }{
		{"codec-transcode-", "transcode"}, {"transcode-", "transcode"}, {"codec-strip-", "strip"}, {"codec-offer-", "offer"},
		{"codec-mask-", "mask"}, {"codec-set-", "set"}, {"codec-accept-", "accept"}, {"codec-except-", "except"},
		{"codec-consume-", "consume"}, {"codec-ignore-", "ignore"},
	}
	// flag prefixes that stand for a key with one value
	keyPrefixes = []struct{ prefix, key string }{
		{"endpoint-learning-", "endpoint-learning"}, {"from-tags-", "from-tags"}, {"OSRTP-", "OSRTP"},
		{"rtcp-mux-", "rtcp-mux"}, {"SDES-", "SDES"}, {"T38-", "T.38"}, {"T.38-", "T.38"},
	}
)

// ParseTemplate explains a template value and checks it against the NG
// flags of the host's release.
func ParseTemplate(name, value string, v Version) Template {
	t := Template{Name: name}
	transport, direction := 0, 0
	for _, tok := range templateTokens(value) {
		key, val, hasVal := strings.Cut(tok, "=")
		it := TemplateItem{Token: tok}
		switch {
		case hasVal && codecKeys[key] != "":
			it = codecItem(tok, codecKeys[key], listItems(val), v)
		case !hasVal && transportShortcut(key, &transport):
			it.Kind, it.Name = i18n.N("transport"), key
			it.Meaning = i18n.T("transport shortcut, combined into transport-protocol")
		case key == "internal" || key == "external" || key == "direction" && hasVal && !strings.HasPrefix(val, "["):
			iface := key
			if key == "direction" {
				iface = val
			}
			dir := []string{"from-interface", "to-interface"}
			it.Kind, it.Name, it.Value = i18n.N("direction"), key, iface
			if direction < len(dir) {
				it.Meaning = i18n.Tf("sets %s=%s (the n-th direction flag fills from-interface, then to-interface)", dir[direction], iface)
			} else {
				it.Status, it.Note = StatusUnknown, i18n.T("more than two direction flags, rtpengine ignores this one")
			}
			direction++
		case !hasVal:
			it = flagItem(tok, v)
		default:
			it = keyItem(tok, key, val, v)
		}
		t.Items = append(t.Items, it)
	}
	if transport != 0 {
		for name, bits := range transportBits {
			if bits == transport&0x007 {
				t.Transport = name
			}
		}
	}
	return t
}

// transportShortcut mirrors parse_rtpp_flags: RTP, AVP, SRTP, AVPF, DTLS and
// full protocol names set bits of one transport-protocol.
func transportShortcut(tok string, t *int) bool {
	switch tok {
	case "RTP":
		*t = (*t | 0x100) &^ 0x001
	case "AVP":
		*t = (*t | 0x100) &^ 0x002
	case "SRTP":
		*t |= 0x101
	case "AVPF":
		*t |= 0x102
	case "DTLS":
		*t |= 0x104
	default:
		bits, ok := transportBits[tok]
		if !ok {
			return false
		}
		*t = 0x100 | bits
	}
	return true
}

func flagItem(tok string, v Version) TemplateItem {
	it := TemplateItem{Token: tok, Kind: i18n.N("flag"), Name: tok}
	if spec, name, ok := lookupNG(ngFlag, tok); ok {
		it.Name = name
		it.Meaning = firstPara(spec)
		it.Status, it.Note = checkNG(spec, v)
		return it
	}
	if strings.HasPrefix(tok, "replace-") {
		return replaceItem(tok, []string{strings.TrimPrefix(tok, "replace-")}, v)
	}
	for _, p := range codecPrefixes {
		if strings.HasPrefix(tok, p.prefix) {
			return codecItem(tok, p.op, []string{strings.TrimPrefix(tok, p.prefix)}, v)
		}
	}
	for _, p := range keyPrefixes {
		if strings.HasPrefix(tok, p.prefix) {
			it := keyItem(tok, p.key, strings.TrimPrefix(tok, p.prefix), v)
			it.Kind = i18n.N("flag")
			return it
		}
	}
	for _, p := range []string{"sdp-attr-add-", "sdp-attr-remove-", "sdp-attr-substitute-"} {
		if strings.HasPrefix(tok, p) {
			it.Name = strings.TrimSuffix(p, "-")
			it.Value = strings.TrimPrefix(tok, p)
			it.Meaning = i18n.Tf("SDP attribute manipulation: %s %s", strings.TrimPrefix(it.Name, "sdp-attr-"), it.Value)
			return it
		}
	}
	it.Status, it.Note = unknownNG(ngFlag, tok, i18n.N("flag"))
	return it
}

func keyItem(tok, key, val string, v Version) TemplateItem {
	switch key {
	case "replace":
		return replaceItem(tok, listItems(val), v)
	case "codec":
		vals := listItems(val)
		it := TemplateItem{Token: tok, Kind: i18n.N("codec"), Name: "codec", Value: strings.Join(vals, " "), Meaning: i18n.T("codec manipulations")}
		for _, e := range vals {
			op, codecs, _ := strings.Cut(e, "=")
			sub := codecItem(e, op, listItems(codecs), v)
			it.Values = append(it.Values, TemplateValue{Value: e, Meaning: sub.Meaning, Status: sub.Status, Note: sub.Note})
		}
		return it
	case "flags":
		it := TemplateItem{Token: tok, Kind: i18n.N("key"), Name: "flags", Value: strings.Join(listItems(val), " "), Meaning: i18n.T("list of flags")}
		for _, f := range listItems(val) {
			sub := flagItem(f, v)
			it.Values = append(it.Values, TemplateValue{Value: f, Meaning: sub.Meaning, Status: sub.Status, Note: sub.Note})
		}
		return it
	}
	it := TemplateItem{Token: tok, Kind: i18n.N("key"), Name: key, Value: strings.Join(listItems(val), " ")}
	spec, name, ok := lookupNG(ngKey, key)
	if !ok {
		it.Status, it.Note = unknownNG(ngKey, key, i18n.N("key"))
		return it
	}
	it.Name = name
	it.Meaning = firstPara(spec)
	it.Status, it.Note = checkNG(spec, v)
	for _, e := range listItems(val) {
		if m := valuePara(spec, e); m != "" {
			it.Values = append(it.Values, TemplateValue{Value: e, Meaning: m})
		}
	}
	return it
}

func replaceItem(tok string, what []string, v Version) TemplateItem {
	it := TemplateItem{Token: tok, Kind: i18n.N("replace"), Name: "replace", Value: strings.Join(what, " "), Meaning: i18n.T("rewrite parts of the SDP")}
	for _, w := range what {
		tv := TemplateValue{Value: w}
		if spec, _, ok := lookupNG(ngReplace, w); ok {
			tv.Meaning = firstPara(spec)
			tv.Status, tv.Note = checkNG(spec, v)
		} else {
			tv.Status, tv.Note = unknownNG(ngReplace, w, i18n.N("replace flag"))
		}
		it.Values = append(it.Values, tv)
	}
	if len(what) == 1 {
		it.Meaning, it.Status, it.Note = it.Values[0].Meaning, it.Values[0].Status, it.Values[0].Note
		it.Values = nil
	}
	return it
}

func codecItem(tok, op string, codecs []string, v Version) TemplateItem {
	it := TemplateItem{Token: tok, Kind: i18n.N("codec"), Name: "codec " + op, Value: strings.Join(codecs, " ")}
	spec, _, ok := lookupNG(ngCodec, op)
	if !ok {
		it.Status, it.Note = unknownNG(ngCodec, op, i18n.N("codec manipulation"))
		return it
	}
	it.Meaning = firstPara(spec)
	it.Status, it.Note = checkNG(spec, v)
	return it
}

// lookupNG finds a name, following undocumented aliases to the documented
// entry. rtpengine matches names case-sensitively.
func lookupNG(k ngKind, name string) (ngSpec, string, bool) {
	spec, ok := ngSpecs[k][name]
	if !ok {
		return ngSpec{}, "", false
	}
	if spec.Alias != "" {
		if doc, ok := ngSpecs[k][spec.Alias]; ok {
			spec.Doc = doc.Doc
			return spec, spec.Alias, true
		}
	}
	return spec, name, true
}

func checkNG(spec ngSpec, v Version) (Status, string) {
	if ok, note := availableIn(ngBranches, uint64(spec.Branches), v); !ok {
		return StatusNotInVersion, note
	}
	return StatusOK, ""
}

// unknownNG explains a name rtpengine does not know: it logs a warning and
// ignores it. A spelling that differs only in case or by a typo is pointed out.
func unknownNG(k ngKind, name, what string) (Status, string) {
	best, bestDist := "", 3 // suggest names at most 2 edits away
	for n := range ngSpecs[k] {
		if strings.EqualFold(n, name) {
			return StatusUnknown, i18n.Tf("unknown %s, names are case-sensitive: did you mean %s?", i18n.T(what), n)
		}
		if d := editDistance(strings.ToLower(n), strings.ToLower(name)); d < bestDist || d == bestDist && n < best {
			best, bestDist = n, d
		}
	}
	if best != "" {
		if spec := ngSpecs[k][best]; spec.Alias != "" {
			best = strings.ReplaceAll(spec.Alias, " ", "-")
		}
		return StatusUnknown, i18n.Tf("unknown %s, rtpengine ignores it: did you mean %s?", i18n.T(what), best)
	}
	return StatusUnknown, i18n.Tf("unknown %s, rtpengine logs a warning and ignores it", i18n.T(what))
}

// editDistance is the Levenshtein distance of two short strings.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

var (
	mdRe     = regexp.MustCompile("`|\\*\\*|__|\\\\")
	mdItalic = regexp.MustCompile(`\*([^*\s][^*]*)\*`)
)

func plain(s string) string {
	s = mdRe.ReplaceAllString(s, "")
	return strings.TrimSpace(mdItalic.ReplaceAllString(s, "$1"))
}

func firstPara(spec ngSpec) string {
	if len(spec.Doc) == 0 {
		return ""
	}
	return plain(spec.Doc[0])
}

// valuePara finds the paragraph of a key's docs describing one value:
// "With `force`, …", or a list item naming the values ("- `off` or `no`")
// followed by the paragraph that describes them.
func valuePara(spec ngSpec, val string) string {
	mark := "`" + val + "`"
	for i, p := range spec.Doc {
		if i == 0 || !strings.Contains(p, mark) {
			continue
		}
		if valueListItem.MatchString(p) && i+1 < len(spec.Doc) {
			return plain(spec.Doc[i+1])
		}
		return plain(p)
	}
	return ""
}

// valueListItem matches "- `off` or `no` or `disable`", "* `offer`".
var valueListItem = regexp.MustCompile("^[-*] `[^`]+`(?:(?:,| or| and) `[^`]+`)*$")
