package homer_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/napadailo/voip-tui/internal/homer"
)

func mapping(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// What homer-app skips, misreads or crashes on in a mapping.
func TestMappingIssues(t *testing.T) {
	good := mapping(t, `{"hepid":1,"profile":"call","partid":10,"fields_mapping":[{"id":"data_header.from_user"},{"id":"data_header.callid"},{"id":"sid"}],
		"correlation_mapping":[{"source_field":"data_header.callid","lookup_id":100,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]}]}`)
	if got := homer.MappingIssues(good, []map[string]any{good}); len(got) != 0 {
		t.Fatalf("a good mapping has issues: %v", got)
	}
	bad := mapping(t, `{"hepid":100,"profile":"my_logs","partid":11,
		"fields_mapping":[{"id":"data_header.x-cid"},{"id":"data_header.a.b"},{"id":"other.x"}],
		"correlation_mapping":[
			{"source_field":"sid","lookup_id":1,"lookup_profile":"call","lookup_field":"data_header.callid","lookup_range":[-300,200]},
			{"lookup_id":1,"lookup_profile":"call","lookup_field":"sid","lookup_range":[0,0]},
			{"source_field":"sid","lookup_profile":"call","lookup_field":"sid","lookup_range":[0,0]},
			{"source_field":"sid","lookup_id":1,"lookup_profile":"call","lookup_field":"sid","lookup_range":[0]},
			{"source_field":"sid","lookup_id":0,"lookup_profile":"pcap","lookup_field":"sid","lookup_range":[0,0]},
			{"source_field":"sid","lookup_id":1,"lookup_profile":"call","lookup_field":"data_header.callid","lookup_range":[-300,200]}]}`)
	got := strings.Join(homer.MappingIssues(bad, []map[string]any{bad, bad}), "\n")
	for _, want := range []string{
		"partid is 11, not 10",
		"a _ in the profile breaks the call assembly",
		"2 mappings share the key 100_my_logs",
		"field data_header.x-cid is refused", "field data_header.a.b is refused", "field other.x is refused",
		"rule 1: lookup_field data_header.callid is not SQL", "write data_header->>'callid'",
		"rule 2 has no source_field", "rule 3 has no lookup_id", "rule 4 has no lookup_range of two numbers",
		"rule 5 has lookup_id 0", "rule 6 repeats rule 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	if bad := homer.MappingIssues(mapping(t, `{"hepid":1,"profile":"my-calls","partid":10}`), nil); len(bad) != 1 || !strings.Contains(bad[0], "characters homer-app does not accept") {
		t.Errorf("a - in the profile: %v", bad)
	}
}

func TestMappingWords(t *testing.T) {
	m := mapping(t, `{"hepid":1,"profile":"call","partid":10,"fields_mapping":[{"id":"data_header.callid"},{"id":"protocol_header.correlation_id"}],"correlation_mapping":[
		{"source_field":"data_header.callid","lookup_id":100,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
		{"source_field":"data_header.callid","lookup_id":5,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
		{"source_field":"protocol_header.correlation_id","lookup_id":1,"lookup_profile":"call","lookup_field":"sid","lookup_range":[-300,200]}]}`)
	d := homer.DeriveMapping(m, []map[string]any{m})
	if d["_key"] != "1_call" || d["_what"] != "SIP · call" || d["_fields"] != 2 || d["_joins"] != "LOG, RTCP, LEG" || d["_search"] != "yes" || d["_issues"] != "" {
		t.Fatalf("%v", d)
	}
	if got := homer.HumanColumn("data_header->>'callid'"); got != "data_header.callid" {
		t.Errorf("%q", got)
	}
	if homer.FieldPlace("data_header.from_user") != "parsed SIP field" || homer.FieldPlace("data_header.x-cid") != "" || homer.FieldTerm("protocol_header.srcIp") != "srcIp" {
		t.Error("field places")
	}
	if rules, err := homer.CorrRules(map[string]any{}); err != nil || rules != nil {
		t.Errorf("{} is no rules: %v %v", rules, err)
	}
}

// A rule after an append_sid one may repeat an earlier rule: it looks up
// the session ids found in between as well.
func TestRepeatAfterAppendSID(t *testing.T) {
	m := mapping(t, `{"hepid":1,"profile":"call","partid":10,"correlation_mapping":[
		{"source_field":"data_header.callid","lookup_id":100,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
		{"source_field":"protocol_header.correlation_id","lookup_id":1,"lookup_profile":"call","lookup_field":"protocol_header->>'correlation_id'","lookup_range":[-300,200],"append_sid":true},
		{"source_field":"data_header.callid","lookup_id":100,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
		{"source_field":"data_header.callid","lookup_id":100,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]}]}`)
	got := homer.MappingIssues(m, nil)
	if len(got) != 1 || !strings.Contains(got[0], "rule 4 repeats rule 3") {
		t.Fatalf("%v", got)
	}
}

// A rule written back leaves its empty optional keys out: homer-app runs an
// input_function_js that is there at all, and an empty one crashes it. A
// stored rule with one empty is a problem; so is a source_field the
// profile does not have.
func TestEmptyScriptsAndSourceFields(t *testing.T) {
	one := 1
	data, _ := json.Marshal(homer.CorrRule{SourceField: "sid", LookupID: &one, LookupProfile: "call", LookupField: "sid", LookupRange: []int{-300, 200}})
	for _, k := range []string{"input_function_js", "input_script", "output_script", "like_search", "append_sid"} {
		if strings.Contains(string(data), k) {
			t.Errorf("%s written empty: %s", k, data)
		}
	}
	logs := mapping(t, `{"hepid":100,"profile":"default","partid":10,"fields_mapping":[{"id":"sid"},{"id":"protocol_header.correlation_id"}],
		"correlation_mapping":[{"source_field":"data_header.correlation_id","lookup_id":1,"lookup_profile":"call","lookup_field":"sid","lookup_range":[-300,200],
		"input_function_js":"","input_script":"","like_search":false,"append_sid":false,"output_script":""}]}`)
	got := strings.Join(homer.MappingIssues(logs, nil), "\n")
	for _, want := range []string{"rule 1 has an empty input_function_js: homer-app runs it and crashes",
		"rule 1: source_field data_header.correlation_id is not in this profile, the rule finds nothing; its table has it as protocol_header.correlation_id"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	// read and written back, the rule is clean
	rules, _ := homer.CorrRules(logs["correlation_mapping"])
	out, _ := json.Marshal(homer.CompileBlocks(homer.ParseBlocks(rules, 100), 100))
	if strings.Contains(string(out), "input_function_js") {
		t.Errorf("written back with the empty script: %s", out)
	}
}

func TestWindowText(t *testing.T) {
	for r, want := range map[[2]int]string{{-300, 200}: "5m before, 3m20s after", {-3600, 0}: "1h before, 0s after", {10, -20}: "10s into it, 20s short of its end"} {
		if got := homer.WindowText([]int{r[0], r[1]}); got != want {
			t.Errorf("%v: %q, want %q", r, got, want)
		}
	}
}

// append_sid shows with the lookup of the block or rule that has it.
func TestBlockAppendSID(t *testing.T) {
	if _, lookup := homer.BlockMatch(homer.NewBlock(homer.BlockLegs)); lookup != "correlation_id · append_sid" {
		t.Errorf("legs: %q", lookup)
	}
	one := 1
	b := homer.Block{Kind: homer.BlockCustom, Rule: homer.CorrRule{SourceField: "data_header.callid", LookupID: &one, LookupProfile: "call", LookupField: "sid", AppendSID: true}}
	if match, lookup := homer.BlockMatch(b); match != "Call-ID" || lookup != "sid · append_sid" {
		t.Errorf("custom: %q %q", match, lookup)
	}
	if _, lookup := homer.BlockMatch(homer.NewBlock(homer.BlockXCID)); lookup != "Call-ID" {
		t.Errorf("xcid: %q", lookup)
	}
}
