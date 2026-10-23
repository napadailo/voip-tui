package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// callOutcome is what became of a call, read from its SIP: the leg of its
// first request, as the caller sees it, decides; the other legs are counted.
type callOutcome struct {
	method      string // the first request: INVITE, or REGISTER and the like
	status      string // the final answer to it, "" when none came
	provisional string // the last provisional answer, 100 left out
	from, to    string // the users of the first request
	start       time.Time
	ringing     time.Time // the first 180 or 183
	final       time.Time // the final answer
	ended       time.Time // the BYE, or the CANCEL of a call not answered
	endMethod   string    // BYE or CANCEL
	byCaller    bool      // the caller sent it
	reason      string    // the Reason of the BYE, the CANCEL or the final answer
	legs        int       // SIP sessions in the call
}

// callOutcomeOf reads the outcome of a call; at gives the time of a message.
func callOutcomeOf(tx *homer.Transaction, at func(i int) time.Time) callOutcome {
	var o callOutcome
	first := -1
	sids := map[string]bool{}
	for i, c := range tx.Data.Calldata {
		if pt := homer.Stringify(msgAt(tx, i)["payloadType"]); c.Method == "" || pt != "" && pt != "1" {
			continue // a log or a report of the call
		}
		sids[c.Sid] = true
		if first < 0 && !isCode(c.Method) && c.Method != "ACK" {
			first = i
		}
	}
	o.legs = len(sids)
	if first < 0 {
		return o
	}
	req := tx.Data.Calldata[first]
	o.method, o.start = req.Method, at(first)
	if m := msgAt(tx, first); m != nil {
		o.from, o.to = str(m, "from_user"), str(m, "to_user")
	}
	if o.to == "" {
		o.to = req.RuriUser
	}
	caller := req.SrcID
	toCaller := hasHopTo(tx, req.Sid, caller, first) // else the answers of the next hop stand for them
	challenge := ""
	for i, c := range tx.Data.Calldata {
		if c.Sid != req.Sid || i < first {
			continue
		}
		m := msgAt(tx, i)
		_, of := cseqOf(m)
		switch {
		case isCode(c.Method) && of == o.method && o.status == "":
			if c.DstID != caller && toCaller { // the answers the caller got: a proxy passes each on
				continue
			}
			switch code := c.Method; {
			case code == "100":
			case code[0] == '1':
				o.provisional = code
				if o.ringing.IsZero() && (code == "180" || code == "183") {
					o.ringing = at(i)
				}
			case code == "401" || code == "407":
				challenge = code
			default:
				o.status, o.final = code, at(i)
				o.reason = reasonOf(m)
			}
		case (c.Method == "BYE" || c.Method == "CANCEL") && o.ended.IsZero() && o.method == "INVITE":
			// the hop of the caller: the hops after it come in any order
			// when captured in the same millisecond
			if c.Method == "BYE" && !strings.HasPrefix(o.status, "2") || toCaller && c.SrcID != caller && c.DstID != caller {
				continue
			}
			o.ended, o.endMethod, o.byCaller = at(i), c.Method, c.SrcID == caller
			if r := reasonOf(m); r != "" {
				o.reason = r
			}
		}
	}
	if o.status == "" {
		o.status = challenge
	}
	// a proxy may leave the Reason out: the other hops of the same request have it
	for i, c := range tx.Data.Calldata {
		if o.reason == "" && c.Sid == req.Sid && o.endMethod != "" && c.Method == o.endMethod {
			o.reason = reasonOf(msgAt(tx, i))
		}
	}
	return o
}

// hasHopTo tells whether a session has an answer to the caller at all.
func hasHopTo(tx *homer.Transaction, sid, caller string, from int) bool {
	for i := from; i < len(tx.Data.Calldata); i++ {
		if c := tx.Data.Calldata[i]; c.Sid == sid && isCode(c.Method) && c.DstID == caller {
			return true
		}
	}
	return false
}

func msgAt(tx *homer.Transaction, i int) map[string]any {
	if i < len(tx.Data.Messages) {
		return tx.Data.Messages[i]
	}
	return nil
}

// reasonOf reads the Reason header of a message, as words.
func reasonOf(m map[string]any) string {
	if m == nil {
		return ""
	}
	if r := parseSIP(str(m, "raw")).get("Reason", true); len(r) > 0 {
		return reasonText(r[0])
	}
	return ""
}

// The badges of the status line: a label in its colour, the value in
// white on it.
var (
	badgeRNG = tcell.ColorGreen        // ringing after
	badgeASW = tcell.ColorDodgerBlue   // answered after
	badgeRBT = tcell.ColorDarkOrange   // rang, a call not answered
	badgeDUR = tcell.ColorMediumPurple // talked
	badgeBYE = tcell.ColorIndianRed    // who ended it
	badgeRSN = tcell.ColorTeal         // the Reason
	badgeLEG = tcell.ColorSlateGray    // legs
)

func badge(label string, color tcell.Color, value string) string {
	return fmt.Sprintf("[%s::b]%s[-::-] [white:%s] %s [-:-:-]", color.String(), label, color.String(), tview.Escape(value))
}

// line says the outcome in one line of markup: the answer, then badges:
// RNG ringing after, ASW answered after, RBT how long it rang unanswered,
// DUR the talk, BYE or CXL who ended it, RSN why, LEG the legs; then who
// called whom.
func (o callOutcome) line(live bool) string {
	if o.method == "" {
		return " " + tui.Faint(T("no SIP request in the call"))
	}
	label, color := o.status, methodColor(o.status)
	switch {
	case o.status == "" && o.provisional != "":
		label, color = Tf("%s (no final)", o.provisional), tcell.ColorAqua
	case o.status == "":
		label, color = T("no answer"), tcell.ColorAqua
	case o.status == "487":
		color = theme.Warn
	}
	if r, ok := reasons[o.status]; ok {
		label += " " + r
	}
	parts := []string{fmt.Sprintf("[%s::b]%s[-::-]", color.String(), tview.Escape(label))}
	dur := func(d time.Duration) string {
		if d < 10*time.Second {
			return d.Round(10 * time.Millisecond).String()
		}
		return d.Round(time.Second).String()
	}
	answered := strings.HasPrefix(o.status, "2") && o.method == "INVITE"
	if !o.ringing.IsZero() {
		parts = append(parts, badge("RNG", badgeRNG, dur(o.ringing.Sub(o.start))))
	}
	switch {
	case answered:
		parts = append(parts, badge("ASW", badgeASW, dur(o.final.Sub(o.start))))
	case o.endMethod == "CANCEL":
		parts = append(parts, badge("RBT", badgeRBT, dur(o.ended.Sub(o.start))))
	}
	switch {
	case answered && !o.ended.IsZero():
		parts = append(parts, badge("DUR", badgeDUR, dur(o.ended.Sub(o.final))))
	case answered && live:
		parts = append(parts, badge("DUR", badgeDUR, dur(time.Since(o.final))+"…"))
	case answered:
		parts = append(parts, badge("BYE", badgeBYE, T("none")))
	}
	who := T("callee")
	if o.byCaller {
		who = T("caller")
	}
	switch o.endMethod {
	case "BYE":
		parts = append(parts, badge("BYE", badgeBYE, who))
	case "CANCEL":
		parts = append(parts, badge("CXL", badgeBYE, who))
	}
	if o.reason != "" {
		parts = append(parts, badge("RSN", badgeRSN, o.reason))
	}
	parts = append(parts, badge("LEG", badgeLEG, fmt.Sprint(max(o.legs, 1))))
	if o.from != "" || o.to != "" {
		parts = append(parts, tview.Escape(o.from+" → "+o.to))
	}
	return " " + strings.Join(parts, " ")
}
