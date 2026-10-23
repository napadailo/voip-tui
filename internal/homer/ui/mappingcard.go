package ui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// mappingCard explains a mapping: what messages it is about and where
// they lie, what a search may use, what the flow of a call found here is
// made of, what homer-app only stores, and what in it homer-app trips on.
func mappingCard(item map[string]any, all []map[string]any) *tui.Card {
	hepid := 0
	fmt.Sscan(homer.Stringify(item["hepid"]), &hepid)
	profile := str(item, "profile")
	key := fmt.Sprintf("%d_%s", hepid, profile)
	card := tui.NewCard(key + " · " + homer.ShortPayload(hepid) + " · " + profile).Sub(str(item, "guid"))

	if issues := homer.MappingIssues(item, all); len(issues) > 0 {
		card.Section(N("Problems"), T("what homer-app skips, misreads or crashes on"))
		for _, s := range issues {
			card.Line(theme.Tag(theme.Warn) + "• " + tview.Escape(s) + "[-]")
		}
	}

	card.Section(N("What it is"))
	card.KV(N("payload"), tview.Escape(homer.ShortPayload(hepid)))
	card.KV(N("profile"), tview.Escape(profile))
	card.KV(N("table"), tview.Escape(homer.MappingTable(key)))
	searched := theme.Tag(theme.OK) + T("yes") + "[-]"
	if p := homer.Stringify(item["partid"]); p != "10" {
		searched = theme.Tag(theme.Warn) + tview.Escape(Tf("no: partid is %s, homer-app searches only 10", p)) + "[-]"
	}
	card.KVRaw(N("searched"), searched)

	fields, _ := homer.MappingFields(item["fields_mapping"])
	card.Section(N("Search fields"), T("what a search can filter by, besides sid, raw, id and create_date"))
	if len(fields) == 0 {
		card.Line(tui.Faint(T("none: only sid, raw, id and create_date")))
	}
	terms := map[string]int{} // a short name two fields share is no name
	for _, f := range fields {
		terms[homer.FieldTerm(f.ID)]++
	}
	for _, f := range fields {
		label, value := homer.FieldTerm(f.ID), ""
		if terms[label] > 1 {
			label = f.ID
		} else if label != f.ID {
			value = tui.Faint(f.ID)
		}
		if strings.Contains(f.ID, ".") && homer.FieldPlace(f.ID) == "" {
			value += " " + theme.Tag(theme.Warn) + T("✗ homer-app refuses it") + "[-]"
		}
		card.KVRaw(label, value)
	}

	// the flow as the blocks of the builder have it, each line with the
	// numbers of the rules homer-app keeps for it and their problems
	rules, err := homer.CorrRules(item["correlation_mapping"])
	_, written, from := homer.BlocksOf(rules, hepid)
	card.Section(N("Flow"), T("what the flow of a call found here is made of, block by block as the builder (b) has it"))
	if err != nil {
		card.Line(theme.Tag(theme.Warn) + tview.Escape(err.Error()) + "[-]")
	}
	stored := ruleIssues(append(homer.EmptyScriptIssues(item["correlation_mapping"]), homer.RuleIssues(rules, fields)...))
	labels, issues := make([]string, len(written)), map[int][]string{}
	for k, nums := range from {
		var ls []string
		for _, n := range nums {
			ls = append(ls, fmt.Sprint(n))
			issues[k] = append(issues[k], stored[n-1]...)
		}
		labels[k] = strings.Join(ls, ",")
	}
	for _, l := range flowLines(written, labels, issues, hepid, key) {
		card.Line(l)
	}
	if len(rules) > 0 {
		card.Line(tui.Faint(Tf("The numbers on the left are of the %d rules homer-app keeps; two on a line are one rule twice.", len(rules))))
		if rulesJSON(written) != rulesJSON(rules) {
			card.Line(tui.Faint(Tf("s in the builder writes them as these %d.", len(written))))
		}
	}
	card.Line(tui.Faint(T("The QoS and Logs tabs of a call find RTCP, RTP reports and logs by the Call-ID themselves.")))

	card.Section(N("Stored only"), T("homer-app does not read these; the capture server may have its own"))
	for _, k := range []string{"retention", "partition_step", "version", "hep_alias"} {
		if v := homer.Stringify(item[k]); v != "" {
			card.KVRaw(k, tview.Escape(v))
		}
	}
	return card
}

// flowLines says what the flow of a call found under key is made of: its
// own messages, then a line for each rule with its number, a tag, LOG,
// RTCP, RTP, SIP or LEG, what it adds and where from, and what homer-app
// trips on in it; the window the rules look in, once when they share it.
// labels numbers the rules, 1, 2, 3 when nil; issues are by the index of
// the rule. Markup lines.
func flowLines(rules []homer.CorrRule, labels []string, issues map[int][]string, hepid int, key string) []string {
	type row struct{ label, tag, text, target, window, bad string }
	rows := []row{{tag: homer.ShortPayload(hepid), text: T("the call's own messages, found by the search"), target: key}}
	legs, windows := false, map[string]bool{}
	for i, r := range rules {
		tag, text := homer.RuleSummary(r, hepid, legs)
		w := homer.WindowText(r.LookupRange)
		windows[w] = true
		if r.AppendSID {
			text += " · append_sid"
		}
		label := fmt.Sprint(i + 1)
		if labels != nil {
			label = labels[i]
		}
		rows = append(rows, row{label: label, tag: tag, text: text, target: r.Target(), window: w, bad: strings.Join(issues[i], ", ")})
		legs = legs || r.AppendSID
	}
	labelW, tagW, textW, targetW := 0, 0, 0, 0
	for _, r := range rows {
		labelW, tagW = max(labelW, len([]rune(r.label))), max(tagW, len([]rune(r.tag)))
		textW, targetW = max(textW, len([]rune(r.text))), max(targetW, len([]rune(r.target)))
	}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", max(w-len([]rune(s)), 0)) }
	var out []string
	for i, r := range rows {
		plus := strings.Repeat(" ", labelW+4)
		if i > 0 {
			plus = " " + strings.Repeat(" ", labelW-len([]rune(r.label))) + r.label + " + "
		}
		color := theme.InfoKey
		if r.bad != "" {
			color = theme.Warn
		}
		l := fmt.Sprintf("%s%s[::b]%s[::-][-]  %s  %s", plus, theme.Tag(color), tview.Escape(pad(r.tag, tagW)), tview.Escape(pad(r.text, textW)), tui.Faint(pad(r.target, targetW)))
		if len(windows) > 1 && r.window != "" {
			l += "  " + tui.Faint(r.window)
		}
		if r.bad != "" {
			l += "  " + theme.Tag(theme.Warn) + "✗ " + tview.Escape(r.bad) + "[-]"
		}
		out = append(out, l)
	}
	if len(rules) == 0 {
		out = append(out, tui.Faint(T("no rules: a call found here is its own messages only")))
	}
	if len(windows) == 1 {
		for w := range windows {
			out = append(out, tui.Faint(Tf("each rule looks %s the search window", w)))
		}
	}
	return out
}

// ruleIssues are the short problems of rules by the index of the rule.
func ruleIssues(found []homer.RuleIssue) map[int][]string {
	out := map[int][]string{}
	for _, ri := range found {
		out[ri.Rule-1] = append(out[ri.Rule-1], ri.Short)
	}
	return out
}
