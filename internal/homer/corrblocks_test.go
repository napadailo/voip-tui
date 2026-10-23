package homer_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/napadailo/voip-tui/internal/homer"
)

func rules(t *testing.T, s string) []homer.CorrRule {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	r, err := homer.CorrRules(v)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func kinds(bs []homer.Block) string {
	var out []string
	for _, b := range bs {
		s := b.Kind
		switch {
		case b.Kind == homer.BlockCustom:
			s += ":" + b.Rule.Target()
		case b.Profile != "" && b.Kind != homer.BlockLogs && b.Kind != homer.BlockRTCP && b.Kind != homer.BlockRTP:
			s += ":" + b.Profile
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

// The 1_call of a production Homer, 9 rules: blocks repeated after the legs
// rule are one block each, the legs' second rule is part of the block, the
// pcap rule stays as it is; written back it is 6 rules that bring the same.
func TestBlocksOfAProductionCall(t *testing.T) {
	rs := rules(t, `[
	 {"source_field":"data_header.callid","lookup_id":100,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"data_header.callid","lookup_id":5,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"protocol_header.correlation_id","lookup_id":1,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"protocol_header.correlation_id","lookup_id":1,"lookup_profile":"call","lookup_field":"protocol_header->>'correlation_id'","lookup_range":[-300,200],"append_sid":true},
	 {"source_field":"data_header.callid","lookup_id":1,"lookup_profile":"call","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"data_header.callid","lookup_id":0,"lookup_profile":"pcap","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"data_header.callid","lookup_id":100,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"data_header.callid","lookup_id":5,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"protocol_header.correlation_id","lookup_id":1,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]}]`)
	bs := homer.ParseBlocks(rs, 1)
	if got := kinds(bs); got != "legs:call logs rtcp xcid:default custom:0_pcap" { // the legs first, as written
		t.Fatalf("%s", got)
	}
	// each rule written stands for the rules read: 1-3 again as 7-9
	if _, written, from := homer.BlocksOf(rs, 1); len(written) != 6 || fmt.Sprint(from) != "[[4] [5] [1 7] [2 8] [3 9] [6]]" {
		t.Fatalf("from: %v", from)
	}
	out := homer.CompileBlocks(bs, 1)
	if len(out) != 6 || !out[0].AppendSID || out[1].LookupField != "sid" || out[1].SourceField != "data_header.callid" {
		t.Fatalf("%+v", out)
	}
	// read back, the same blocks
	if got := kinds(homer.ParseBlocks(out, 1)); got != "legs:call logs rtcp xcid:default custom:0_pcap" {
		t.Fatalf("again: %s", got)
	}
}

// homer-app's default 1_call has the B2BUA leg by a JS suffix; its
// 100_default has data_header.callid, no SQL: the blocks write it right.
func TestBlocksOfTheDefaults(t *testing.T) {
	call := rules(t, `[
	 {"source_field":"data_header.callid","lookup_id":100,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"data_header.callid","lookup_id":5,"lookup_profile":"default","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"protocol_header.correlation_id","lookup_id":1,"lookup_profile":"call","lookup_field":"sid","lookup_range":[-300,200]},
	 {"source_field":"data_header.callid","lookup_id":1,"lookup_profile":"call","lookup_field":"data_header->>'callid'","lookup_range":[-300,200],
	  "input_function_js":"var returnData=[]; for (var i = 0; i < data.length; i++) { returnData.push(data[i]+'_b2b-1'); }; returnData;"}]`)
	bs := homer.ParseBlocks(call, 1)
	if got := kinds(bs); got != "logs rtcp xcid:call suffix:call" || bs[3].Suffix != "_b2b-1" {
		t.Fatalf("%s %+v", got, bs)
	}
	if out := homer.CompileBlocks(bs, 1); out[3].InputJS != call[3].InputJS {
		t.Fatalf("the suffix JS changed: %q", out[3].InputJS)
	}
	logs := rules(t, `[
	 {"source_field":"sid","lookup_id":1,"lookup_profile":"call","lookup_field":"data_header.callid","lookup_range":[-300,200]},
	 {"source_field":"sid","lookup_id":1,"lookup_profile":"registration","lookup_field":"data_header.callid","lookup_range":[-300,200]}]`)
	lb := homer.ParseBlocks(logs, 100)
	if got := kinds(lb); got != "sip:call sip:registration" {
		t.Fatalf("%s", got)
	}
	for _, r := range homer.CompileBlocks(lb, 100) {
		if r.LookupField != "data_header->>'callid'" {
			t.Fatalf("not fixed: %q", r.LookupField)
		}
	}
}

// A block switched off writes no rule; a window and a suffix are kept;
// a rule no block stands for is written as it was.
func TestBlocksEdit(t *testing.T) {
	bs := []homer.Block{homer.NewBlock(homer.BlockLogs), homer.NewBlock(homer.BlockSuffix), homer.NewBlock(homer.BlockRTP)}
	bs[0].On = false
	bs[1].Suffix, bs[1].From, bs[1].To = "-leg2", -60, 60
	bs[2].ByIP = true
	odd := rules(t, `[{"source_field":"data_header.from_user","lookup_id":1,"lookup_profile":"default","lookup_field":"data_header->>'to_user'","lookup_range":[-10,10],"like_search":true}]`)[0]
	bs = append(bs, homer.Block{Kind: homer.BlockCustom, On: true, Rule: odd})
	out := homer.CompileBlocks(bs, 1)
	if len(out) != 3 || !strings.Contains(out[0].InputJS, "'-leg2'") || out[0].LookupRange[0] != -60 ||
		out[1].SourceField != "protocol_header.srcIp" || !out[2].LikeSearch {
		t.Fatalf("%+v", out)
	}
	if got := kinds(homer.ParseBlocks(out, 1)); got != "suffix:call rtp custom:1_default" {
		t.Fatalf("%s", got)
	}
	if homer.SuffixOK("a'b") || !homer.SuffixOK("_b2b-1") {
		t.Fatal("suffix check")
	}
}

// The blocks are the same for every mapping; each takes the Call-ID from
// where the mapping's table has it: SIP's data_header.callid, the session
// id of logs, RTCP and RTP reports.
func TestBlocksTakeTheCallIDOfTheTable(t *testing.T) {
	bs := []homer.Block{homer.NewBlock(homer.BlockLogs), homer.NewBlock(homer.BlockSIP), homer.NewBlock(homer.BlockLegs)}
	for _, c := range []struct {
		hepid int
		want  string
	}{{1, "data_header.callid"}, {35, "sid"}, {100, "sid"}} {
		for _, r := range homer.CompileBlocks(bs, c.hepid) {
			if r.SourceField != c.want && r.SourceField != "protocol_header.correlation_id" {
				t.Errorf("hepid %d: %+v", c.hepid, r)
			}
		}
		if got := kinds(homer.ParseBlocks(homer.CompileBlocks(bs, c.hepid), c.hepid)); got != "legs:call logs sip:call" {
			t.Errorf("hepid %d read back: %s", c.hepid, got)
		}
	}
	if len(homer.BlockKinds()) != 8 {
		t.Fatal(homer.BlockKinds())
	}
}
