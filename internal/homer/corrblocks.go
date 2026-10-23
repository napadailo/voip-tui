package homer

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/napadailo/voip-tui/i18n"
)

// The correlation of a mapping as building blocks: each kind of rule a
// call assembly is made of, read from the rules and written back to them.
// A rule no block stands for stays as it is, a custom block.

// Block kinds.
const (
	BlockLogs   = "logs"   // HEP logs of the Call-ID
	BlockRTCP   = "rtcp"   // RTCP of the Call-ID
	BlockRTP    = "rtp"    // RTP agent reports, by the Call-ID or the IP
	BlockLegs   = "legs"   // the legs with the same correlation_id, and all that is theirs
	BlockXCID   = "xcid"   // the leg whose Call-ID is my correlation_id
	BlockSuffix = "suffix" // the leg whose Call-ID is mine and a suffix
	BlockSIP    = "sip"    // the SIP of the Call-ID in a profile
	BlockCustom = "custom" // a rule as it is
)

// Block is one block of a call assembly.
type Block struct {
	Kind    string
	On      bool
	From    int    // seconds added to the start of the search window
	To      int    // and to its end
	Profile string // where legs, xcid, suffix and sip look: call, registration, default
	Suffix  string // suffix: what the leg's Call-ID adds to mine
	ByIP    bool   // rtp: by the device's IP instead of the Call-ID
	Rule    CorrRule
}

// The window of homer-app's default rules: 5 minutes before the search
// window, 3m20s after.
const (
	DefaultFrom = -300
	DefaultTo   = 200
)

// BlockKinds are the blocks of a call assembly, the same for every
// mapping: each takes the Call-ID from where the mapping's table keeps it.
func BlockKinds() []string {
	return []string{BlockLogs, BlockRTCP, BlockRTP, BlockSIP, BlockLegs, BlockXCID, BlockSuffix, BlockCustom}
}

// CallIDField is where the table of a mapping keeps the Call-ID: SIP
// parses it into data_header.callid; logs, RTCP and RTP reports have it as
// their session id only.
func CallIDField(hepid int) string {
	if hepid == 1 {
		return "data_header.callid"
	}
	return "sid"
}

// BlockTag names a block short, as the flow does: LOG, RTCP, RTP, SIP,
// LEG; a custom rule by what it looks for.
func BlockTag(b Block, hepid int) string {
	switch b.Kind {
	case BlockLogs:
		return "LOG"
	case BlockRTCP:
		return "RTCP"
	case BlockRTP:
		return "RTP"
	case BlockSIP:
		return "SIP"
	case BlockLegs, BlockXCID, BlockSuffix:
		return "LEG"
	}
	id := -1
	if b.Rule.LookupID != nil {
		id = *b.Rule.LookupID
	}
	if id == hepid {
		return "LEG"
	}
	return ShortPayload(id)
}

// BlockProfile is the profile a block looks in.
func BlockProfile(b Block) string {
	switch b.Kind {
	case BlockLogs, BlockRTCP, BlockRTP:
		return "default"
	case BlockCustom:
		return b.Rule.LookupProfile
	}
	return b.Profile
}

// BlockMatch says what of the call a block takes and the field it looks
// it up in there: Call-ID and sid for logs, correlation_id and Call-ID for
// the leg by X-CID; append_sid with the lookup of a rule that passes the
// session ids it finds on to the rules after it.
func BlockMatch(b Block) (match, lookup string) {
	switch b.Kind {
	case BlockLogs, BlockRTCP:
		return "Call-ID", "sid"
	case BlockRTP:
		if b.ByIP {
			return "IP", "IP"
		}
		return "Call-ID", "sid"
	case BlockSIP:
		return "Call-ID", "Call-ID"
	case BlockLegs:
		return "correlation_id", "correlation_id · append_sid"
	case BlockXCID:
		return "correlation_id", "Call-ID"
	case BlockSuffix:
		return fmt.Sprintf("Call-ID+%q", b.Suffix), "Call-ID"
	}
	lookup = ShortColumn(b.Rule.LookupField)
	if b.Rule.AppendSID {
		lookup += " · append_sid"
	}
	return ShortColumn(b.Rule.SourceField), lookup
}

// BlockName says a block in a line: LEG correlation_id → Call-ID · call.
func BlockName(b Block, hepid int) string {
	match, lookup := BlockMatch(b)
	return fmt.Sprintf("%s %s → %s · %s", BlockTag(b, hepid), match, lookup, BlockProfile(b))
}

// ShortColumn names a source_field or lookup_field short: Call-ID,
// correlation_id, IP, sid, or the field as it is.
func ShortColumn(f string) string {
	switch plainField(f) {
	case "sid":
		return "sid"
	case "data_header.callid":
		return "Call-ID"
	case "protocol_header.correlation_id":
		return "correlation_id"
	case "protocol_header.srcIp":
		return "IP"
	}
	return plainField(f)
}

// BlockHelp says what a kind of block brings into a call.
func BlockHelp(kind string) string {
	switch kind {
	case BlockLogs:
		return i18n.T("the HEP logs whose session id is the call's Call-ID")
	case BlockRTCP:
		return i18n.T("the RTCP reports whose session id is the call's Call-ID")
	case BlockRTP:
		return i18n.T("the RTP agent reports of the call's Call-ID, or of the IP its messages came from")
	case BlockLegs:
		return i18n.T("the other legs whose correlation_id is the call's, with their messages; every other block runs for them too")
	case BlockXCID:
		return i18n.T("the leg whose Call-ID is the correlation_id the capture agent put on this call")
	case BlockSuffix:
		return i18n.T("the leg whose Call-ID is this one's with a suffix, as a B2BUA such as Asterisk makes it")
	case BlockSIP:
		return i18n.T("the SIP messages of a profile with the same Call-ID: for a log or a report its call, for a call the same Call-ID in another profile")
	}
	return i18n.T("a rule of correlation_mapping as it is, edited as JSON")
}

// NewBlock is a block of a kind with the default window.
func NewBlock(kind string) Block {
	b := Block{Kind: kind, On: true, From: DefaultFrom, To: DefaultTo, Profile: "call"}
	switch kind {
	case BlockSuffix:
		b.Suffix = "_b2b-1"
	case BlockCustom:
		one := 1
		b.Rule = CorrRule{SourceField: "data_header.callid", LookupID: &one, LookupProfile: "call", LookupField: "sid", LookupRange: []int{DefaultFrom, DefaultTo}}
	}
	return b
}

var suffixJS = regexp.MustCompile(`^\s*var\s+returnData\s*=\s*\[\];\s*for\s*\(var i = 0; i < data\.length; i\+\+\)\s*\{\s*returnData\.push\(data\[i\]\s*\+\s*'([^'\\]*)'\);?\s*\};?\s*returnData;?\s*$`)

// SuffixOK tells a suffix the JS of a block can carry as it is.
func SuffixOK(s string) bool { return s != "" && !strings.ContainsAny(s, `'"\`+"\n") }

// plainField writes a lookup_field the way the blocks compare them:
// data_header->>'callid' as data_header.callid.
func plainField(f string) string {
	if col, key, ok := strings.Cut(f, "->>"); ok {
		return strings.TrimSpace(col) + "." + strings.Trim(strings.TrimSpace(key), `'"`)
	}
	return strings.TrimSpace(f)
}

// recognize finds the block a rule is; legsid is the second rule of a
// legs block, the messages of the legs by their session ids.
func recognize(r CorrRule, hepid int) (Block, bool) {
	if r.LookupID == nil || len(r.LookupRange) != 2 || r.InputScript != "" || r.OutputScript != "" || r.LikeSearch {
		return Block{}, false
	}
	id := *r.LookupID
	b := Block{On: true, From: r.LookupRange[0], To: r.LookupRange[1], Profile: r.LookupProfile}
	plain := r.InputJS == "" && !r.AppendSID
	lf := plainField(r.LookupField)
	callid := r.SourceField == "data_header.callid" || r.SourceField == "sid"
	switch {
	case plain && callid && lf == "sid" && r.LookupProfile == "default" && id == 100:
		b.Kind = BlockLogs
	case plain && callid && lf == "sid" && r.LookupProfile == "default" && id == 5:
		b.Kind = BlockRTCP
	case plain && callid && lf == "sid" && r.LookupProfile == "default" && id == 35:
		b.Kind = BlockRTP
	case plain && r.SourceField == "protocol_header.srcIp" && lf == "protocol_header.srcIp" && r.LookupProfile == "default" && id == 35:
		b.Kind, b.ByIP = BlockRTP, true
	case r.AppendSID && r.InputJS == "" && r.SourceField == "protocol_header.correlation_id" && lf == "protocol_header.correlation_id" && id == hepid:
		b.Kind = BlockLegs
	case plain && callid && lf == "sid" && id == hepid:
		b.Kind = "legsid"
	case plain && r.SourceField == "protocol_header.correlation_id" && lf == "sid" && id == 1:
		b.Kind = BlockXCID
	case !r.AppendSID && callid && lf == "data_header.callid" && id == 1 && suffixJS.MatchString(r.InputJS):
		b.Kind, b.Suffix = BlockSuffix, suffixJS.FindStringSubmatch(r.InputJS)[1]
	case plain && callid && lf == "data_header.callid" && id == 1:
		b.Kind = BlockSIP
	default:
		return Block{}, false
	}
	return b, true
}

// same tells two blocks that bring the same messages, whatever their
// windows.
func (b Block) same(o Block) bool {
	return b.Kind == o.Kind && b.Kind != BlockCustom && b.Profile == o.Profile && b.Suffix == o.Suffix && b.ByIP == o.ByIP
}

func custom(r CorrRule) Block {
	b := Block{Kind: BlockCustom, On: true, Rule: r}
	if len(r.LookupRange) == 2 {
		b.From, b.To = r.LookupRange[0], r.LookupRange[1]
	}
	return b
}

// ParseBlocks reads the rules of a mapping into blocks, the legs first, as
// CompileBlocks writes them. A rule that brings what a block before it
// brings is that block again, as homer-app's own mappings repeat rules
// after a legs rule for the legs: the blocks run after the legs one anyway.
func ParseBlocks(rules []CorrRule, hepid int) []Block {
	blocks, _ := parseBlocks(rules, hepid)
	return blocks
}

// parseBlocks is ParseBlocks with the numbers, 1-based, of the rules each
// block was read from: its own, and for a legs block those of its second
// rule.
func parseBlocks(rules []CorrRule, hepid int) ([]Block, [][2][]int) {
	var out []Block
	var nums [][2][]int
	for i, r := range rules {
		b, ok := recognize(r, hepid)
		switch {
		case !ok:
			out, nums = append(out, custom(r)), append(nums, [2][]int{{i + 1}})
			continue
		case b.Kind == "legsid":
			legs := slices.IndexFunc(out, func(o Block) bool { return o.Kind == BlockLegs && o.Profile == b.Profile })
			if legs < 0 {
				out, nums = append(out, custom(r)), append(nums, [2][]int{{i + 1}})
			} else {
				nums[legs][1] = append(nums[legs][1], i+1)
			}
			continue
		}
		if k := slices.IndexFunc(out, b.same); k >= 0 {
			nums[k][0] = append(nums[k][0], i+1)
			continue
		}
		out, nums = append(out, b), append(nums, [2][]int{{i + 1}})
	}
	// the legs go first, as CompileBlocks writes them
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return b2i(out[b].Kind == BlockLegs) - b2i(out[a].Kind == BlockLegs) })
	sorted, sortedNums := make([]Block, len(out)), make([][2][]int, len(out))
	for i, k := range order {
		sorted[i], sortedNums[i] = out[k], nums[k]
	}
	return sorted, sortedNums
}

// BlocksOf reads rules into blocks and writes them back as CompileBlocks
// does, with the numbers of the rules read that each rule written stands
// for: 1 and 7 for the logs of a mapping that has them twice.
func BlocksOf(rules []CorrRule, hepid int) ([]Block, []CorrRule, [][]int) {
	blocks, nums := parseBlocks(rules, hepid)
	var from [][]int
	for k, b := range blocks { // the order of CompileBlocks: the legs first, two rules each
		if b.Kind == BlockLegs {
			from = append(from, nums[k][0], nums[k][1])
		}
	}
	for k, b := range blocks {
		if b.Kind != BlockLegs {
			from = append(from, nums[k][0])
		}
	}
	return blocks, CompileBlocks(blocks, hepid), from
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CompileBlocks writes the blocks back as rules. A legs block goes first:
// the session ids it finds go to every rule after it, so the logs, RTCP and
// legs of the blocks are looked up for the other legs as well.
func CompileBlocks(blocks []Block, hepid int) []CorrRule {
	var out []CorrRule
	callid := CallIDField(hepid)
	rule := func(b Block, source string, id int, profile, field string) CorrRule {
		return CorrRule{SourceField: source, LookupID: &id, LookupProfile: profile, LookupField: field, LookupRange: []int{b.From, b.To}}
	}
	for _, b := range blocks {
		if b.On && b.Kind == BlockLegs {
			r := rule(b, "protocol_header.correlation_id", hepid, b.Profile, "protocol_header->>'correlation_id'")
			r.AppendSID = true
			out = append(out, r, rule(b, callid, hepid, b.Profile, "sid"))
		}
	}
	for _, b := range blocks {
		if !b.On {
			continue
		}
		switch b.Kind {
		case BlockLogs:
			out = append(out, rule(b, callid, 100, "default", "sid"))
		case BlockRTCP:
			out = append(out, rule(b, callid, 5, "default", "sid"))
		case BlockRTP:
			if b.ByIP {
				out = append(out, rule(b, "protocol_header.srcIp", 35, "default", "protocol_header->>'srcIp'"))
			} else {
				out = append(out, rule(b, callid, 35, "default", "sid"))
			}
		case BlockXCID:
			out = append(out, rule(b, "protocol_header.correlation_id", 1, b.Profile, "sid"))
		case BlockSuffix:
			r := rule(b, callid, 1, b.Profile, "data_header->>'callid'")
			r.InputJS = fmt.Sprintf("var returnData=[]; for (var i = 0; i < data.length; i++) { returnData.push(data[i]+'%s'); }; returnData;", b.Suffix)
			out = append(out, r)
		case BlockSIP:
			out = append(out, rule(b, callid, 1, b.Profile, "data_header->>'callid'"))
		case BlockCustom:
			out = append(out, b.Rule)
		}
	}
	return out
}

// BlockWindow says the window of a block.
func BlockWindow(b Block) string { return WindowText([]int{b.From, b.To}) }

// RuleSummary says a rule of a call assembly the way the flow shows it: a
// short tag, LOG, RTCP, RTP, SIP or LEG, and what it adds in plain words.
// legsBefore tells a rule that runs for the other legs too, found by a
// legs rule before it.
func RuleSummary(r CorrRule, hepid int, legsBefore bool) (tag, text string) {
	b, ok := recognize(r, hepid)
	switch {
	case !ok:
		id := -1
		if r.LookupID != nil {
			id = *r.LookupID
		}
		tag = ShortPayload(id)
		if id == hepid {
			tag = "LEG"
		}
		text = i18n.Tf("where %s is this call's %s", HumanColumn(r.LookupField), HumanColumn(r.SourceField))
		if r.InputJS != "" || r.InputScript != "" {
			text += ", " + i18n.T("the values changed by a script first")
		}
	case b.Kind == BlockLogs:
		tag, text = "LOG", i18n.T("logs with this Call-ID")
	case b.Kind == BlockRTCP:
		tag, text = "RTCP", i18n.T("RTCP reports with this Call-ID")
	case b.Kind == BlockRTP && b.ByIP:
		tag, text = "RTP", i18n.T("RTP agent reports from the same IP")
	case b.Kind == BlockRTP:
		tag, text = "RTP", i18n.T("RTP agent reports with this Call-ID")
	case b.Kind == BlockSIP:
		tag, text = "SIP", i18n.T("SIP messages with this Call-ID")
	case b.Kind == BlockLegs:
		tag, text = "LEG", i18n.T("the other legs with the same correlation_id; the rules after it look for them too")
	case b.Kind == "legsid":
		tag, text = "LEG", i18n.T("the messages of those legs")
	case b.Kind == BlockXCID:
		tag, text = "LEG", i18n.T("the leg whose Call-ID is this call's correlation_id")
	case b.Kind == BlockSuffix:
		tag, text = "LEG", i18n.Tf("the leg whose Call-ID is this one's plus %q", b.Suffix)
	}
	if legsBefore && (!ok || b.Kind != "legsid") {
		text += ", " + i18n.T("for the other legs too")
	}
	return tag, text
}

// WindowText says a lookup_range short: how far before and after the
// search window a rule looks.
func WindowText(r []int) string {
	if len(r) < 2 {
		return "?"
	}
	d := func(s int) string {
		if s < 0 {
			s = -s
		}
		t := (time.Duration(s) * time.Second).String() // 5m, not 5m0s; 1h, not 1h0m0s
		if strings.HasSuffix(t, "m0s") {
			t = strings.TrimSuffix(t, "0s")
		}
		if strings.HasSuffix(t, "h0m") {
			t = strings.TrimSuffix(t, "0m")
		}
		return t
	}
	before, after := i18n.Tf("%s before", d(r[0])), i18n.Tf("%s after", d(r[1]))
	if r[0] > 0 {
		before = i18n.Tf("%s into it", d(r[0]))
	}
	if r[1] < 0 {
		after = i18n.Tf("%s short of its end", d(r[1]))
	}
	return before + ", " + after
}
