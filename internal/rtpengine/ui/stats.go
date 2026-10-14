package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// newStatsPanel is :stats: `list jsonstats` (mr8.5+) laid out as a card,
// refreshed with the ticker. y shows the JSON as rtpengine printed it, s
// saves the JSON. Hosts before mr8.5 show `list counters`, `list totals`
// and `list transcoders` as text: before mr8.5 the per-second rates are only
// in counters.
func newStatsPanel(app *App) View {
	v := newTextViewer(app, "stats", "markup")
	v.SetLoading()
	var reply string
	var st rtpengine.Stats
	showRaw, legacy := false, false
	show := func() {
		switch {
		case legacy:
		case showRaw:
			v.SetContent(prettyJSON(reply), "json")
		case st != nil:
			v.SetContent(statsText(app, st), "markup")
		}
	}
	v.Loader = func() {
		c := app.Client()
		app.Async(func(ctx context.Context) func() {
			if !c.Has("list jsonstats") {
				out, err := c.Text(ctx, "list totals")
				if err == nil && c.Has("list counters") {
					var counters string
					if counters, err = c.Text(ctx, "list counters"); err == nil {
						out = strings.TrimRight(counters, "\n") + "\n\n" + strings.TrimLeft(out, "\n")
					}
				}
				if err == nil && c.Has("list transcoders") { // mr7.5
					var tr string
					if tr, err = c.Text(ctx, "list transcoders"); err == nil {
						out = strings.TrimRight(out, "\n") + "\n\n" + T("Transcoders:") + "\n" + tr
					}
				}
				return func() {
					legacy = true
					if err != nil {
						v.SetError(err)
						return
					}
					v.SetContent(out, "stats")
				}
			}
			s, r, err := c.JSONStats(ctx)
			return func() {
				if err != nil {
					v.SetError(err)
					return
				}
				st, reply = s, r
				show()
			}
		})
	}
	v.Keys['y'] = func() {
		if legacy {
			app.Flash().Warnf("this host prints no JSON statistics (list jsonstats needs mr8.5+)")
			return
		}
		showRaw = !showRaw
		show()
		v.Text().ScrollToBeginning() // the other layout has other lines
	}
	v.Keys['s'] = func() {
		ext, content := ".json", prettyJSON(reply)
		if legacy {
			ext, content = ".txt", v.Content()
		}
		path, err := app.SaveFile("stats "+app.host.Name, ext, content)
		if err != nil {
			app.Flash().Err(err)
			return
		}
		app.Flash().Infof("saved %s", path)
	}
	v.Extra = []Hint{{Key: "y", Desc: N("JSON/parsed")}}
	app.ensureProbed(v.Loader)
	return &autoText{v}
}

// prettyJSON indents rtpengine's one-line JSON; unparsable text stays as is.
func prettyJSON(raw string) string {
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(strings.TrimSpace(raw)), "", "  "); err != nil {
		return raw
	}
	return b.String() + "\n"
}

// statsText lays out jsonstats: what runs now, totals since the start,
// voice quality, NG commands, interfaces and transcoders. Sections and keys
// the release does not print are left out.
func statsText(app *App, st rtpengine.Stats) string {
	cur, tot := st.Obj("currentstatistics"), st.Obj("totalstatistics")
	sub := []string{app.host.Name, app.Client().Info().Version.String()}
	if up, ok := tot.Num("uptime"); ok {
		sub = append(sub, Tf("up %s", humanAge(time.Duration(up)*time.Second)))
	}
	c := tui.NewCard(T("Statistics")).Sub(sub...)

	if cur != nil {
		c.Section(N("Sessions"), T("now"))
		c.KV(N("own"), tui.Strong(count(cur, "sessionsown")))
		c.KV(N("foreign"), count(cur, "sessionsforeign"))
		c.KV(N("total"), count(cur, "sessionstotal"))
		optKV(c, cur, N("transcoded media"), "transcodedmedia")
		for _, k := range []struct{ label, key string }{{N("media cache"), "mediacache"}, {N("player cache"), "playercache"}} {
			if cur.Int(k.key) > 0 {
				c.KV(k.label, count(cur, k.key))
			}
		}

		c.Section(N("Traffic"), T("per second, now"))
		c.Line(tui.Paint(theme.Header, padLeft(T("PACKETS"), 14)+padLeft(T("BYTES"), 12)+padLeft(T("ERRORS"), 9)))
		for _, r := range []struct{ label, suffix string }{{N("userspace"), "_user"}, {N("kernel"), "_kernel"}, {N("total"), ""}} {
			if _, ok := cur.Num("packetrate" + r.suffix); !ok {
				continue
			}
			c.KV(r.label, fmt.Sprintf("%14s%12s", count(cur, "packetrate"+r.suffix), humanBytes(strconv.FormatUint(cur.Int("byterate"+r.suffix), 10))+"/s")+
				warnIfAny(cur, "errorrate"+r.suffix, 9))
		}
		if _, ok := cur.Num("media_kernel"); ok {
			c.KV(N("streams"), Tf("%s in kernel · %s in userspace · %s mixed", count(cur, "media_kernel"), count(cur, "media_userspace"), count(cur, "media_mixed")))
		}
	}

	if tot != nil {
		c.Section(N("Since start"))
		c.KV(N("managed sessions"), tui.Strong(count(tot, "managedsessions")))
		c.KV(N("rejected"), warnIfAny(tot, "rejectedsessions", 0))
		c.KV(N("ended normally"), count(tot, "regularterminatedsessions"))
		c.KV(N("terminated by operator"), count(tot, "forcedterminatedsessions"))
		c.KV(N("timed out: no RTP"), count(tot, "timeoutsessions"))
		optKV(c, tot, N("timed out: silent RTP"), "silenttimeoutsessions")
		optKV(c, tot, N("timed out: no answer"), "offertimeoutsessions")
		optKV(c, tot, N("timed out: final"), "finaltimeoutsessions")
		c.KV(N("relayed"), Tf("%s packets · %s", count(tot, "relayedpackets"), humanBytes(strconv.FormatUint(tot.Int("relayedbytes"), 10)))+
			tui.Faint(" · ")+warnIfAny(tot, "relayedpacketerrors", 0)+tui.Faint(" "+T("errors")))
		optKV(c, tot, N("streams without media"), "zerowaystreams")
		optKV(c, tot, N("one-way streams"), "onewaystreams")
		if avg, ok := tot.Num("avgcallduration"); ok {
			d := humanAge(time.Duration(avg * float64(time.Second)))
			if sd, ok := tot.Num("totalcallsduration_stddev"); ok && sd > 0 {
				d += tui.Faint(" ± " + humanAge(time.Duration(sd*float64(time.Second))))
			}
			c.KV(N("average call"), d)
		}
	}

	if mos, vm := st.Obj("mos"), st.Obj("voip_metrics"); mos != nil || vm != nil {
		c.Section(N("Quality"), T("averages over sampled streams"))
		if mos != nil {
			c.KV(N("MOS"), graded(avgOf(mos, "mos"), func(f float64) tcell.Color {
				switch {
				case f >= 4:
					return theme.OK
				case f >= 3.6:
					return theme.Warn
				}
				return theme.Err
			}, "")+tui.Faint(" · "+Tf("%s samples", count(mos, "mos_samples_total"))))
		}
		if vm != nil {
			c.KV(N("packet loss"), graded(avgOf(vm, "packetloss"), func(f float64) tcell.Color {
				switch {
				case f <= 1:
					return theme.OK
				case f <= 3:
					return theme.Warn
				}
				return theme.Err
			}, "%"))
			jitter := orNoSamples(avgOf(vm, "jitter"))
			if m := avgOf(vm, "jitter_measured"); m != "" {
				jitter += tui.Faint(" · " + Tf("measured %s", m))
			}
			c.KV(N("jitter"), jitter)
			c.KV(N("RTT e2e"), orNoSamples(avgOf(vm, "rtt_e2e")))
			c.KV(N("RTT dsct"), orNoSamples(avgOf(vm, "rtt_dsct")))
			c.KV(N("lost packets"), count(vm, "packets_lost"))
			c.KV(N("duplicates"), count(vm, "rtp_duplicates"))
			optKV(c, vm, N("sequence skips"), "rtp_skips")
			optKV(c, vm, N("sequence resets"), "rtp_seq_resets")
			optKV(c, vm, N("reordered"), "rtp_reordered")
		}
	}

	if cmds := st.NGTotals(); len(cmds) > 0 {
		c.Section(N("NG commands"), T("all proxies since the start, per proxy: :proxies"))
		var errs uint64
		for _, n := range st.NGByProxy() {
			errs += n.Errors
		}
		for _, kv := range cmds {
			n, _ := strconv.ParseUint(kv.Value, 10, 64)
			c.KVRaw(kv.Key, fmt.Sprintf("%14s", groupDigits(strconv.FormatUint(n, 10))))
		}
		tone := theme.Fg
		if errs > 0 {
			tone = theme.Warn
		}
		c.KV(N("errors"), tui.Paint(tone, fmt.Sprintf("%14d", errs)))
	}

	if ifaces := st.List("interfaces"); len(ifaces) > 0 {
		c.Section(N("Interfaces"), T("details: :interfaces"))
		for _, i := range ifaces {
			ports := i.Obj("ports")
			pct, _ := ports.Num("used_pct")
			tone := theme.OK
			switch {
			case pct >= 90:
				tone = theme.Err
			case pct >= 70:
				tone = theme.Warn
			}
			c.KVRaw(i.Str("name"), tui.Faint(i.Str("address")+" · ")+Tf("ports %s of %s", count(ports, "used"), count(ports, "totals"))+
				tui.Faint(" · ")+tui.Paint(tone, fmt.Sprintf("%.1f%%", pct)))
		}
	}

	if _, ok := st["transcoders"]; ok {
		c.Section(N("Transcoders"), T("per second, now; total since the start"))
		tr := st.List("transcoders")
		if len(tr) == 0 {
			c.Line(tui.Faint(T("nothing transcoded yet")))
		} else {
			c.Line(tui.Paint(theme.Header, padLeft(T("NOW"), 5)+padLeft(T("PACKETS"), 10)+padLeft(T("BYTES"), 12)+padLeft(T("SAMPLES"), 12)))
		}
		for _, t := range tr {
			line := fmt.Sprintf("%5s%10s%12s%12s", count(t, "num"), count(t, "packetrate"),
				humanBytes(strconv.FormatUint(t.Int("byterate"), 10))+"/s", count(t, "samplerate"))
			if _, ok := t.Num("packets"); ok { // mr11.5+
				line += tui.Faint("  " + Tf("total %s packets · %s", count(t, "packets"), humanBytes(strconv.FormatUint(t.Int("bytes"), 10))))
			}
			c.KVRaw(t.Str("chain"), line)
		}
	}
	return c.String()
}

// count is a counter of o with digits grouped, "" if missing.
func count(o rtpengine.Stats, key string) string {
	if _, ok := o.Num(key); !ok {
		return ""
	}
	return groupDigits(strconv.FormatUint(o.Int(key), 10))
}

// optKV adds a counter only when the release prints it.
func optKV(c *tui.Card, o rtpengine.Stats, label, key string) {
	if _, ok := o.Num(key); ok {
		c.KV(label, count(o, key))
	}
}

// warnIfAny is a counter that should stay 0: warn colored otherwise,
// right-aligned in width (0: as is).
func warnIfAny(o rtpengine.Stats, key string, width int) string {
	s := count(o, key)
	if width > 0 {
		s = padLeft(s, width)
	}
	if o.Int(key) > 0 {
		return tui.Paint(theme.Warn, s)
	}
	return s
}

// avgOf is <stat>_average, "" without samples.
func avgOf(o rtpengine.Stats, stat string) string {
	if o.Int(stat+"_samples_total") == 0 {
		return ""
	}
	f, ok := o.Num(stat + "_average")
	if !ok {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
