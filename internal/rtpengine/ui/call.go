package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/tui/theme"
)

// Thresholds for coloring the age of the last packet: RTP normally arrives
// every 20-30 ms, RTCP every ~5 s.
const (
	rtpStaleWarn, rtpStaleErr   = 5, 30
	rtcpStaleWarn, rtcpStaleErr = 15, 60
	// mediaGrace is how long a fresh leg may stay silent: the answer and the
	// first packets need a moment
	mediaGrace = 5
)

// callText renders a parsed call: header, media status, the path through
// rtpengine and per-leg stream tables. ifaces (`list interfaces`, may be
// empty) name the rtpengine side of each stream.
func callText(ci *rtpengine.CallInfo, ifaces []rtpengine.Interface) string {
	now := ci.Now()
	var b strings.Builder
	keys := []string{T("Call-ID"), T("Proxy"), T("Media"), T("Created"), T("Last signal"), T("Owner"), T("Redis"), T("Recording"), T("TOS"), T("Deletion")}
	keyW := 0
	for _, k := range keys {
		keyW = max(keyW, strWidth(k))
	}
	kv := func(k, v string) {
		fmt.Fprintf(&b, "%s%s[-] %s\n", theme.Tag(theme.InfoKey), pad(k+":", keyW+1), v)
	}
	esc := tview.Escape

	fmt.Fprintf(&b, "%s[::b]%s[::-][-]\n", theme.Tag(theme.Title), T("Call"))
	kv(keys[0], esc(ci.CallID))
	kv(keys[1], esc(ci.Proxy))
	kv(keys[2], callStatus(ci, now))
	kv(keys[3], clock(ci.Created)+dim(" · "+ago(now, ci.Created)))
	if ci.LastSignal > 0 {
		kv(keys[4], clock(ci.LastSignal)+dim(" · "+ago(now, ci.LastSignal)))
	}
	owner := theme.Tag(theme.OK) + "own[-]"
	if ci.Foreign {
		owner = theme.Tag(theme.Warn) + "foreign[-]" + dim(" "+T("(owned by another rtpengine via redis)"))
	}
	kv(keys[5], owner)
	redis := "keyspace " + esc(ci.Keyspace)
	if ci.RedisUpdate > 0 {
		redis += dim(" · " + Tf("updated %s", ago(now, ci.RedisUpdate/1e6)))
	}
	kv(keys[6], redis)
	if ci.Recording != "" {
		kv(keys[7], esc(ci.Recording))
	}
	if ci.TOS != "" {
		kv(keys[8], esc(ci.TOS)+dim(tosNote(ci.TOS)))
	}
	if ci.Deleted {
		kv(keys[9], theme.Tag(theme.Warn)+esc(T("marked, the call is being torn down"))+"[-]")
	}

	if p := callPath(ci, ifaces); p != "" {
		fmt.Fprintf(&b, "\n%s[::b]%s[::-][-]\n%s\n", theme.Tag(theme.Title), T("Path"), p)
	}

	for _, l := range ci.Legs {
		b.WriteString("\n")
		head := []string{l.Type, humanAge(time.Duration(l.Duration * float64(time.Second)))}
		if len(l.SubscribedTo) > 0 {
			names := make([]string, len(l.SubscribedTo))
			for i, t := range l.SubscribedTo {
				names[i] = ci.LegName(t)
			}
			head = append(head, Tf("receives media from %s", strings.Join(names, ", ")))
		}
		fmt.Fprintf(&b, "%s[::b]%s[::-][-]%s\n", theme.Tag(theme.Accent), esc(l.Name()), dim(" · "+strings.Join(head, " · ")))
		sub := []string{"tag " + l.Tag}
		if l.Branch != "" {
			sub = append(sub, "branch "+l.Branch)
		}
		if l.Dialogue != "" {
			sub = append(sub, Tf("in dialogue with %s", ci.LegName(l.Dialogue)))
		}
		if len(l.Aliases) > 0 {
			sub = append(sub, "aliases "+strings.Join(l.Aliases, ", "))
		}
		b.WriteString(dim("  "+strings.Join(sub, " · ")) + "\n")
		for _, m := range l.Media {
			b.WriteString(mediaText(m, l.Duration, now, ifaces))
		}
	}

	if len(ci.Extra) > 0 {
		fmt.Fprintf(&b, "\n%s[::b]%s[::-][-]\n", theme.Tag(theme.Title), T("Other"))
		for _, e := range ci.Extra {
			if e.Key == "" {
				b.WriteString(esc(e.Value) + "\n")
			} else {
				kv(e.Key, esc(e.Value))
			}
		}
	}
	return b.String()
}

func mediaText(m rtpengine.CallMedia, dur float64, now int64, ifaces []rtpengine.Interface) string {
	var b strings.Builder
	kind := m.Type
	if kind == "" { // not printed before mr8.5
		kind = "media"
	}
	desc := []string{fmt.Sprintf("%s #%d", kind, m.Index)}
	if m.Protocol != "" {
		desc = append(desc, m.Protocol)
	}
	if m.Codec != "" {
		desc = append(desc, m.Codec)
	} else if m.Type != "" {
		desc = append(desc, T("codec unknown"))
	}
	if name := mediaInterface(m, ifaces); name != "" {
		desc = append(desc, "interface "+name)
	}
	fmt.Fprintf(&b, "\n  %s%s[-]\n", theme.Tag(theme.InfoValue), tview.Escape(strings.Join(desc, " · ")))
	fmt.Fprintf(&b, "%s  %s %s   %s %s %s %s %s %s %s[-]\n", theme.Tag(theme.Header), pad("", 5), pad(T("FROM PEER"), 23), pad(T("TO RTPENGINE"), 23),
		padLeft(T("PACKETS"), 10), padLeft(T("BYTES"), 10), padLeft(T("ERR"), 5), pad("  "+T("AVG RATE"), 21), pad(T("LAST PACKET"), 22), "SSRC")
	for _, s := range m.Streams {
		kind, warn, errAt := "RTP", int64(rtpStaleWarn), int64(rtpStaleErr)
		if s.RTCP {
			kind, warn, errAt = "RTCP", rtcpStaleWarn, rtcpStaleErr
		}
		rate := ""
		if !s.RTCP && dur >= 1 && s.Packets > 0 {
			rate = fmt.Sprintf("%3.0f p/s %4.0f kbit/s", float64(s.Packets)/dur, float64(s.Bytes)*8/dur/1000)
		}
		last, tone := "", theme.Fg
		switch {
		case s.Packets == 0 && dur < mediaGrace:
			last, tone = T("waiting"), theme.Warn
		case s.Packets == 0:
			last, tone = T("nothing received"), theme.Err
			if s.RTCP {
				tone = theme.Warn
			}
		case s.Last() > 0:
			age := now - s.Last()
			last = ago(now, s.Last())
			switch {
			case age >= errAt:
				tone = theme.Err
			case age >= warn:
				tone = theme.Warn
			}
			if s.HasKernel {
				if s.Kernel() {
					last += " · kernel"
				} else {
					last += " · userspace"
				}
			}
		}
		errTone := theme.Fg
		if s.Errors > 0 {
			errTone = theme.Warn
		}
		ssrc := s.SSRC
		if ssrc == "0" {
			ssrc = ""
		}
		// pad before coloring: tags do not take space on screen
		fmt.Fprintf(&b, "  %-5s %-23s → %-23s %10s %10s %s%5d[-] %-21s %s%s[-] %s\n",
			kind, tview.Escape(s.Peer), tview.Escape(s.Local),
			groupDigits(strconv.FormatUint(s.Packets, 10)), humanBytes(strconv.FormatUint(s.Bytes, 10)),
			theme.Tag(errTone), s.Errors, rate, theme.Tag(tone), pad(last, 22), ssrc)
		if s.Delay != "" {
			fmt.Fprintf(&b, "        %s\n", dim(Tf("delay min/avg/max %s s", s.Delay)))
		}
	}
	return b.String()
}

// callStatus sums up whether media flows: the first thing to check on a
// call with one-way or no audio.
func callStatus(ci *rtpengine.CallInfo, now int64) string {
	var problems, notes []string
	legs := 0
	for _, l := range ci.Legs {
		for _, m := range l.Media {
			for _, s := range m.Streams {
				if s.RTCP {
					continue
				}
				legs++
				switch age := now - s.Last(); {
				case s.Packets == 0 && l.Duration < mediaGrace:
					problems = append(problems, theme.Tag(theme.Warn)+tview.Escape(Tf("waiting for RTP from %s", l.Name()))+"[-]")
				case s.Packets == 0:
					problems = append(problems, theme.Tag(theme.Err)+tview.Escape(Tf("no RTP from %s", l.Name()))+"[-]")
				case age >= rtpStaleWarn:
					problems = append(problems, theme.Tag(theme.Warn)+tview.Escape(Tf("no RTP from %s for %s", l.Name(), humanAge(time.Duration(age)*time.Second)))+"[-]")
				}
				if s.Errors > 0 {
					notes = append(notes, theme.Tag(theme.Warn)+tview.Escape(Tf("errors from %s: %d", l.Name(), s.Errors))+"[-]")
				}
			}
		}
	}
	switch {
	case legs == 0:
		return dim(T("no media streams yet"))
	case len(problems) == 0 && legs >= 2:
		problems = []string{theme.Tag(theme.OK) + T("RTP flowing both ways") + "[-]"}
	case len(problems) == 0:
		problems = []string{theme.Tag(theme.OK) + T("RTP flowing") + "[-]"}
	}
	return strings.Join(append(problems, notes...), dim(" · "))
}

// mediaInterface is the interface all streams of m use, "" if unknown or mixed.
func mediaInterface(m rtpengine.CallMedia, ifaces []rtpengine.Interface) string {
	name := ""
	for i, s := range m.Streams {
		n := rtpengine.InterfaceFor(ifaces, s.Local)
		if n == "" || i > 0 && n != name {
			return ""
		}
		name = n
	}
	return name
}

// callPath draws peer ⇄ rtpengine ⇄ peer for a plain two-leg call.
func callPath(ci *rtpengine.CallInfo, ifaces []rtpengine.Interface) string {
	if len(ci.Legs) != 2 {
		return ""
	}
	rtp := func(l rtpengine.CallLeg) (rtpengine.CallStream, bool) {
		for _, m := range l.Media {
			for _, s := range m.Streams {
				if !s.RTCP {
					return s, true
				}
			}
		}
		return rtpengine.CallStream{}, false
	}
	a, okA := rtp(ci.Legs[0])
	z, okZ := rtp(ci.Legs[1])
	if !okA || !okZ {
		return ""
	}
	e := tview.Escape
	iface := func(local string) string {
		if n := rtpengine.InterfaceFor(ifaces, local); n != "" {
			return " " + dim("("+n+")")
		}
		return ""
	}
	return fmt.Sprintf("  %s%s[-] %s ⇄ %s%s  %s[::b]rtpengine[::-][-]  %s%s ⇄ %s %s%s[-]",
		theme.Tag(theme.Accent), e(ci.Legs[0].Name()), e(a.Peer), e(a.Local), iface(a.Local),
		theme.Tag(theme.InfoValue), e(z.Local), iface(z.Local), e(z.Peer), theme.Tag(theme.Accent), e(ci.Legs[1].Name()))
}

func clock(unix int64) string {
	if unix <= 0 {
		return "-"
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04:05")
}

func ago(now, t int64) string {
	if t <= 0 {
		return T("never")
	}
	return Tf("%s ago", humanAge(time.Duration(max(now-t, 0))*time.Second))
}

func dim(s string) string { return theme.Tag(theme.Dim) + tview.Escape(s) + "[-]" }

// tosNote names common DSCP values of the TOS byte.
func tosNote(tos string) string {
	n, err := strconv.Atoi(tos)
	if err != nil || n == 0 {
		return ""
	}
	names := map[int]string{46: "EF", 34: "AF41", 26: "AF31", 24: "CS3", 40: "CS5"}
	if name, ok := names[n>>2]; ok {
		return fmt.Sprintf(" (DSCP %d %s)", n>>2, name)
	}
	return fmt.Sprintf(" (DSCP %d)", n>>2)
}
