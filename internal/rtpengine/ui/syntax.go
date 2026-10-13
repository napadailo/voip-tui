package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

func init() {
	tui.RegisterSyntax("call", colorizeCall)
	tui.RegisterSyntax("stats", colorizeStats)
	tui.RegisterSyntax("log", colorizeLog)
}

// logLevelRe finds the level rtpengine writes in front of each message.
var logLevelRe = regexp.MustCompile(`\b(EMERG|ALERT|CRIT|ERR|WARNING|NOTICE|INFO|DEBUG):`)

// colorizeLog dims the prefix (time, host, process) of rtpengine's log
// lines and colors the level.
func colorizeLog(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		loc := logLevelRe.FindStringSubmatchIndex(line)
		if loc == nil {
			b.WriteString(tview.Escape(line) + "\n")
			continue
		}
		tone := theme.Fg
		switch line[loc[2]:loc[3]] {
		case "EMERG", "ALERT", "CRIT", "ERR":
			tone = theme.Err
		case "WARNING":
			tone = theme.Warn
		case "NOTICE":
			tone = theme.OK
		case "DEBUG":
			tone = theme.Dim
		}
		fmt.Fprintf(&b, "%s%s[-]%s%s[-]%s\n", theme.Tag(theme.Dim), tview.Escape(line[:loc[0]]),
			theme.Tag(tone), line[loc[0]:loc[1]], tview.Escape(line[loc[1]:]))
	}
	return b.String()
}

var (
	statLineRe  = regexp.MustCompile(`^(\s+)(.+?)(\s*):(.*)$`)
	callKVRe    = regexp.MustCompile(`^([a-z_ ]+):\s?(.*)$`)
	callTagRe   = regexp.MustCompile(`^(---\s+Tag\s+)'([^']*)'(.*)$`)
	callMediaRe = regexp.MustCompile(`^(------\s+Media\s+#\d+)(.*?)(using\s+)(.*)$`)
	callPortRe  = regexp.MustCompile(`^(--------\s+Port\s+)(\S+)(\s+<>\s+)(\S+)(.*)$`)
)

// colorizeStats highlights `list totals` / `list counters` layout.
func colorizeStats(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		switch m := statLineRe.FindStringSubmatch(line); {
		case m != nil && strings.TrimSpace(m[4]) != "":
			fmt.Fprintf(&b, "%s%s%s[-]%s:%s%s[-]\n", m[1], theme.Tag(theme.Fg), tview.Escape(m[2]), m[3], theme.Tag(theme.OK), tview.Escape(m[4]))
		case strings.HasSuffix(t, ":"):
			fmt.Fprintf(&b, "%s[::b]%s[::-][-]\n", theme.Tag(theme.Title), tview.Escape(line))
		default:
			b.WriteString(tview.Escape(line) + "\n")
		}
	}
	return b.String()
}

// colorizeCall highlights `list sessions <callid>` / `call <id> info`.
func colorizeCall(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		switch {
		case callTagRe.MatchString(line):
			m := callTagRe.FindStringSubmatch(line)
			fmt.Fprintf(&b, "%s%s'%s'[-]%s\n", tview.Escape(m[1]), theme.Tag(theme.Accent), tview.Escape(m[2]), tview.Escape(m[3]))
		case callMediaRe.MatchString(line):
			m := callMediaRe.FindStringSubmatch(line)
			fmt.Fprintf(&b, "%s%s[-]%s%s%s%s[-]\n", theme.Tag(theme.Title), tview.Escape(m[1]), tview.Escape(m[2]), m[3], theme.Tag(theme.OK), tview.Escape(m[4]))
		case callPortRe.MatchString(line):
			m := callPortRe.FindStringSubmatch(line)
			fmt.Fprintf(&b, "%s%s%s[-]%s%s%s[-]%s\n", tview.Escape(m[1]), theme.Tag(theme.InfoValue), tview.Escape(m[2]), m[3], theme.Tag(theme.InfoKey), tview.Escape(m[4]), tview.Escape(m[5]))
		case callKVRe.MatchString(line) && !strings.Contains(line, " | "):
			m := callKVRe.FindStringSubmatch(line)
			fmt.Fprintf(&b, "%s%s:[-] %s\n", theme.Tag(theme.InfoKey), tview.Escape(m[1]), tview.Escape(m[2]))
		default:
			b.WriteString(tview.Escape(line) + "\n")
		}
	}
	return b.String()
}
