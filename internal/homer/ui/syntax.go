package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/tui"
)

func init() { tui.RegisterSyntax("sip", colorizeSIP) }

var (
	sipHeaderRe  = regexp.MustCompile(`^([A-Za-z0-9\-]+)(\s*:)(.*)$`)
	sdpLineRe    = regexp.MustCompile(`^([a-z])=(.*)$`)
	sipRequestRe = regexp.MustCompile(`^([A-Z]+) (\S+) SIP/2\.0$`)
	sipStatusRe  = regexp.MustCompile(`^SIP/2\.0 (\d{3}) (.*)$`)
)

// colorizeJSON highlights JSON in the call detail pane.
var colorizeJSON = tui.ColorizeJSON

func colorizeSIP(s string) string {
	var b strings.Builder
	inBody := false
	for i, line := range strings.Split(s, "\n") {
		esc := tview.Escape(line)
		switch {
		case i == 0 && sipRequestRe.MatchString(line):
			m := sipRequestRe.FindStringSubmatch(line)
			fmt.Fprintf(&b, "[%s::b]%s[-::-] [white]%s[-] SIP/2.0\n", methodColor(m[1]).String(), m[1], tview.Escape(m[2]))
		case i == 0 && sipStatusRe.MatchString(line):
			m := sipStatusRe.FindStringSubmatch(line)
			fmt.Fprintf(&b, "SIP/2.0 [%s::b]%s %s[-::-]\n", methodColor(m[1]).String(), m[1], tview.Escape(m[2]))
		case strings.TrimSpace(line) == "":
			inBody = true
			b.WriteString("\n")
		case inBody && sdpLineRe.MatchString(line):
			m := sdpLineRe.FindStringSubmatch(line)
			fmt.Fprintf(&b, "[mediumpurple]%s[-]=%s\n", m[1], tview.Escape(m[2]))
		case !inBody && sipHeaderRe.MatchString(line):
			m := sipHeaderRe.FindStringSubmatch(line)
			fmt.Fprintf(&b, "[dodgerblue]%s[-]%s[white]%s[-]\n", m[1], m[2], tview.Escape(m[3]))
		default:
			b.WriteString(esc + "\n")
		}
	}
	return b.String()
}
