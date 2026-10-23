package homer

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/napadailo/voip-tui/i18n"
)

// What homer-app does with a mapping, read from its sources
// (sipcapture/homer-app, data/service/search.go, search_query_security.go,
// mapping.go, userSettings.go): the key hepid_profile names the table
// hep_proto_<key>; partid 10 makes it searchable; fields_mapping lists the
// fields a search may use, with their type; correlation_mapping says what a
// call found here is assembled from. retention, partition_step, version,
// create_table and create_index are only stored.

// PayloadName names a HEP payload type the way homer-app does.
func PayloadName(hepid int) string {
	switch hepid {
	case 1:
		return "SIP"
	case 5:
		return "RTCP"
	case 34, 35:
		return i18n.T("RTP reports")
	case 54:
		return "ISUP"
	case 81:
		return "CDR"
	case 100:
		return i18n.T("logs")
	case 200:
		return i18n.T("imported PCAP")
	case 2000:
		return "Loki"
	}
	return fmt.Sprintf("HEP %d", hepid)
}

// ShortPayload names a HEP payload type in a word, as the lists show it
// and the flow filter does: SIP, RTCP, RTP, LOG.
func ShortPayload(hepid int) string {
	switch hepid {
	case 34, 35:
		return "RTP"
	case 100:
		return "LOG"
	case 200:
		return "PCAP"
	}
	return PayloadName(hepid)
}

// ProfileMeaning says what the messages of a profile are, as the writer
// sorts them into tables: homer-app itself only reads the tables.
func ProfileMeaning(profile string) string {
	switch profile {
	case "call":
		return i18n.T("calls")
	case "registration":
		return i18n.T("registrations")
	case "default":
		return i18n.T("the rest")
	}
	return profile
}

// MappingTable is the table the messages of a key live in.
func MappingTable(key string) string { return "hep_proto_" + key }

// CorrRule is one entry of correlation_mapping.
type CorrRule struct {
	SourceField   string `json:"source_field"`
	LookupID      *int   `json:"lookup_id,omitempty"`
	LookupProfile string `json:"lookup_profile"`
	LookupField   string `json:"lookup_field"`
	LookupRange   []int  `json:"lookup_range"`
	// the optional keys are left out when empty: homer-app runs an
	// input_function_js that is there at all, and an empty one crashes it
	InputJS      string `json:"input_function_js,omitempty"`
	InputScript  string `json:"input_script,omitempty"`
	LikeSearch   bool   `json:"like_search,omitempty"`
	AppendSID    bool   `json:"append_sid,omitempty"`
	OutputScript string `json:"output_script,omitempty"`
}

// Target is the key of the table the rule looks in.
func (r CorrRule) Target() string {
	id := 0
	if r.LookupID != nil {
		id = *r.LookupID
	}
	return fmt.Sprintf("%d_%s", id, r.LookupProfile)
}

// CorrRules reads correlation_mapping, an array of rules or {} for none.
func CorrRules(v any) ([]CorrRule, error) {
	if v == nil {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if t := strings.TrimSpace(string(data)); t == "{}" || t == "null" || t == "[]" {
		return nil, nil
	}
	var rules []CorrRule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// MappingField is one entry of fields_mapping, the keys homer-app reads.
type MappingField struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	FormType string `json:"form_type"`
	Skip     bool   `json:"skip"`
	Hide     *bool  `json:"hide"`
}

// MappingFields reads fields_mapping.
func MappingFields(v any) ([]MappingField, error) {
	if v == nil {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var fields []MappingField
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

var (
	keyRe     = regexp.MustCompile(`^\d+_[a-zA-Z0-9_]+$`)
	subnameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// builtinFields are searchable in every profile, mapped or not.
var builtinFields = map[string]bool{"limit": true, "smartinput": true, "raw": true, "id": true, "sid": true, "create_date": true}

// FieldPlace says where homer-app looks for a field: in the SIP fields
// the writer parsed (data_header), in the HEP header (protocol_header), a
// column of the table, the whole text; "" for a field it refuses.
func FieldPlace(id string) string {
	if a, b, ok := strings.Cut(id, "."); ok {
		if strings.Contains(b, ".") || !subnameRe.MatchString(b) {
			return ""
		}
		switch a {
		case "data_header":
			return i18n.T("parsed SIP field")
		case "protocol_header":
			return i18n.T("HEP header: network, time, capture")
		}
		return ""
	}
	switch id {
	case "profile":
		return i18n.T("not a column: Homer's search form picks the profile with it")
	case "node":
		return i18n.T("not a column: the database nodes, node= in homer-tui")
	case "raw":
		return i18n.T("the message text, ILIKE")
	case "smartinput":
		return i18n.T("an expression, alone")
	case "sid":
		return i18n.T("column: session id, the Call-ID")
	case "id", "create_date":
		return i18n.T("column")
	}
	return i18n.T("a column of that name, if the table has one")
}

// FieldTerm is how a field is typed in a homer-tui search: the name after
// the dot, as in from_user=100.
func FieldTerm(id string) string {
	if _, b, ok := strings.Cut(id, "."); ok {
		return b
	}
	return id
}

// HumanColumn says a source_field or lookup_field in words.
func HumanColumn(f string) string {
	switch {
	case f == "sid":
		return i18n.T("session id (sid)")
	case strings.Contains(f, "->>"):
		col, key, _ := strings.Cut(f, "->>")
		return strings.TrimSpace(col) + "." + strings.Trim(strings.TrimSpace(key), "'\"")
	}
	return f
}

// MappingIssues finds what makes homer-app skip, misread or crash on a
// mapping; all are the mappings beside it, for the keys they share.
func MappingIssues(item map[string]any, all []map[string]any) []string {
	var out []string
	hepid := fmt.Sprint(item["hepid"])
	profile := fmt.Sprint(item["profile"])
	key := hepid + "_" + profile
	if p := fmt.Sprint(item["partid"]); p != "10" {
		out = append(out, i18n.Tf("partid is %s, not 10: homer-app does not search this profile and leaves it out of the list", p))
	}
	if !keyRe.MatchString(key) {
		out = append(out, i18n.Tf("the key %s has characters homer-app does not accept: not searchable", key))
	} else if strings.Contains(profile, "_") {
		out = append(out, i18n.T("a _ in the profile breaks the call assembly: homer-app cuts the key at the first _ to find the mapping"))
	}
	same := 0
	for _, o := range all {
		if fmt.Sprint(o["hepid"]) == hepid && fmt.Sprint(o["profile"]) == profile {
			same++
		}
	}
	if same > 1 {
		out = append(out, i18n.Tf("%d mappings share the key %s: homer-app takes any one of them", same, key))
	}
	fields, err := MappingFields(item["fields_mapping"])
	if err != nil {
		out = append(out, i18n.Tf("fields_mapping is not a list of fields: %v", err))
	}
	for _, f := range fields {
		if f.ID != "" && !builtinFields[f.ID] && strings.Contains(f.ID, ".") && FieldPlace(f.ID) == "" {
			out = append(out, i18n.Tf("field %s is refused in a search: homer-app takes data_header.name or protocol_header.name", f.ID))
		}
	}
	rules, err := CorrRules(item["correlation_mapping"])
	if err != nil {
		out = append(out, i18n.Tf("correlation_mapping is not a list of rules: %v", err))
	}
	for _, ri := range append(EmptyScriptIssues(item["correlation_mapping"]), RuleIssues(rules, fields)...) {
		out = append(out, ri.Text)
	}
	return out
}

// EmptyScriptIssues finds the rules that keep input_function_js empty:
// homer-app runs one that is there, gets no list back and crashes.
func EmptyScriptIssues(v any) []RuleIssue {
	list, _ := v.([]any)
	var out []RuleIssue
	for i, x := range list {
		if r, ok := x.(map[string]any); ok {
			if js, there := r["input_function_js"]; there && strings.TrimSpace(fmt.Sprint(js)) == "" {
				out = append(out, RuleIssue{i + 1, i18n.Tf("rule %d has an empty input_function_js: homer-app runs it and crashes on a call of this profile; leave the key out", i+1), i18n.T("crashes homer-app")})
			}
		}
	}
	return out
}

// RuleIssue is what homer-app skips or crashes on in one rule, numbered
// from 1.
type RuleIssue struct {
	Rule  int
	Text  string
	Short string // a few words for the scheme of the assembly
}

// RuleIssues finds what homer-app skips or crashes on in the rules of a
// call assembly; fields, the profile's own, tell a source_field its table
// does not have.
func RuleIssues(rules []CorrRule, fields []MappingField) []RuleIssue {
	var issues []RuleIssue
	for i, r := range rules {
		n := i + 1
		add := func(text, short string) { issues = append(issues, RuleIssue{n, text, short}) }
		if f := r.SourceField; strings.Contains(f, ".") && len(fields) > 0 && !hasField(fields, f) {
			if other := fieldNamed(fields, FieldTerm(f)); other != "" {
				add(i18n.Tf("rule %d: source_field %s is not in this profile, the rule finds nothing; its table has it as %s", n, f, other), i18n.Tf("finds nothing: %s here", other))
			} else {
				add(i18n.Tf("rule %d: source_field %s is not in this profile, the rule finds nothing", n, f), i18n.T("finds nothing: no such field"))
			}
		}
		switch {
		case r.SourceField == "":
			add(i18n.Tf("rule %d has no source_field: homer-app crashes on a call of this profile", n), i18n.T("crashes homer-app"))
		case r.LookupID == nil:
			add(i18n.Tf("rule %d has no lookup_id: homer-app crashes on a call of this profile", n), i18n.T("crashes homer-app"))
		case len(r.LookupRange) < 2:
			add(i18n.Tf("rule %d has no lookup_range of two numbers: homer-app crashes on a call of this profile", n), i18n.T("crashes homer-app"))
		case *r.LookupID == 0:
			add(i18n.Tf("rule %d has lookup_id 0: homer-app skips it", n), i18n.T("skipped"))
		}
		if f := r.LookupField; strings.Contains(f, ".") && !strings.Contains(f, "->") {
			col, name, _ := strings.Cut(f, ".")
			add(i18n.Tf("rule %d: lookup_field %s is not SQL, the lookup finds nothing; write %s->>'%s'", n, f, col, name), i18n.T("finds nothing: not SQL"))
		}
		// a repeat after an append_sid rule looks up the session ids that
		// rule found as well: on purpose, not a repeat
		for j := i - 1; j >= 0 && !rules[j].AppendSID; j-- {
			if sameRule(rules[j], r) {
				add(i18n.Tf("rule %d repeats rule %d: homer-app runs the same lookup twice", n, j+1), i18n.Tf("repeats rule %d", j+1))
				break
			}
		}
	}
	return issues
}

func hasField(fields []MappingField, id string) bool {
	for _, f := range fields {
		if f.ID == id {
			return true
		}
	}
	return false
}

// fieldNamed is the field of a profile with that short name, "" for none.
func fieldNamed(fields []MappingField, term string) string {
	for _, f := range fields {
		if FieldTerm(f.ID) == term {
			return f.ID
		}
	}
	return ""
}

func sameRule(a, b CorrRule) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// MappingJoins names, short, what a call of this mapping is assembled
// with: LOG, RTCP, RTP, LEG for the other legs.
func MappingJoins(item map[string]any) string {
	rules, err := CorrRules(item["correlation_mapping"])
	if err != nil {
		return "?"
	}
	var out []string
	add := func(s string) {
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	self := fmt.Sprint(item["hepid"])
	for _, r := range rules {
		if r.LookupID == nil {
			continue
		}
		switch id := *r.LookupID; {
		case fmt.Sprint(id) == self:
			add("LEG")
		default:
			add(ShortPayload(id))
		}
	}
	return strings.Join(out, ", ")
}

// DeriveMapping makes the columns of the mappings list for an item: what
// it is, its fields, what it joins, whether it is searchable, its issues.
// The item itself stays as homer-app gave it.
func DeriveMapping(item map[string]any, all []map[string]any) map[string]any {
	hepid := 0
	fmt.Sscan(fmt.Sprint(item["hepid"]), &hepid)
	profile := fmt.Sprint(item["profile"])
	key := fmt.Sprintf("%d_%s", hepid, profile)
	d := map[string]any{"_key": key, "_what": ShortPayload(hepid) + " · " + profile, "_joins": MappingJoins(item),
		"_search": i18n.T("yes"), "_issues": ""}
	if fields, err := MappingFields(item["fields_mapping"]); err == nil {
		d["_fields"] = len(fields)
	}
	if fmt.Sprint(item["partid"]) != "10" || !keyRe.MatchString(key) {
		d["_search"] = i18n.T("no")
	}
	if n := len(MappingIssues(item, all)); n > 0 {
		d["_issues"] = n
	}
	return d
}
