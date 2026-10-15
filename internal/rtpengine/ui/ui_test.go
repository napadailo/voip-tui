package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/internal/rtpengine/config"
	"github.com/napadailo/voip-tui/internal/rtpengine/mock"
	"github.com/napadailo/voip-tui/tui/theme"
)

type harness struct {
	t      *testing.T
	app    *App
	screen tcell.SimulationScreen
	srv    map[string]*mock.Server
	logs   map[string]string // host name -> mock log file
	dir    string            // downloadDir
}

// start runs the app against two mock hosts: "old" (mr6.5) and "new" (mr13.3).
func start(t *testing.T, cmd string) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg, _ := config.Load(filepath.Join(dir, "none.yaml"))
	cfg.DownloadDir = dir
	h := &harness{t: t, srv: map[string]*mock.Server{}, logs: map[string]string{}, dir: dir}
	for i, m := range []struct {
		name, version string
	}{{"old", "mr6.5.9.12"}, {"new", "mr13.3.1.4"}} {
		srv := mock.New(m.version, int64(10+i))
		srv.Freeze = true
		l, err := srv.Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		mdir := filepath.Join(dir, m.name)
		conf, rec, err := mock.WriteConfs(mdir, mock.ConfOpts{CLI: l.Addr().String()})
		if err != nil {
			t.Fatal(err)
		}
		h.srv[m.name] = srv
		logPath := filepath.Join(mdir, "rtpengine.log")
		if err := srv.WriteLog(logPath); err != nil {
			t.Fatal(err)
		}
		h.logs[m.name] = logPath
		cfg.Hosts = append(cfg.Hosts, config.Host{Name: m.name, Address: "local", Via: config.ViaDirect, CLI: l.Addr().String(),
			Version: m.version, ConfigFile: conf, RecordingConfigFile: rec, CtlBin: "rtpengine-ctl",
			LogCommand: "grep -hF -- {callid} " + logPath})
	}
	cfg.RefreshSeconds = 60
	cur, _ := cfg.Find("new")
	app := New(cfg, cur)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(220, 50)
	app.SetScreen(screen)
	done := make(chan struct{})
	go func() {
		_ = app.Start(cmd)
		close(done)
	}()
	t.Cleanup(func() {
		app.Stop()
		<-done
		_ = theme.Apply("default")
	})
	time.Sleep(50 * time.Millisecond)
	app.QueueUpdateDraw(func() { screen.SetSize(220, 50) })
	h.app, h.screen = app, screen
	return h
}

func (h *harness) text() string {
	var out string
	h.app.QueueUpdate(func() {
		cells, w, _ := h.screen.GetContents()
		var b strings.Builder
		skip := false // the second column of a wide (CJK) character
		for i, c := range cells {
			switch {
			case skip:
				skip = false
			case len(c.Runes) > 0:
				b.WriteRune(c.Runes[0])
				skip = strWidth(string(c.Runes[0])) == 2 && (i+1)%w != 0
			default:
				b.WriteRune(' ')
			}
			if (i+1)%w == 0 {
				b.WriteRune('\n')
			}
		}
		out = b.String()
	})
	return out
}

func (h *harness) waitFor(subs ...string) string {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var txt string
	for time.Now().Before(deadline) {
		txt = h.text()
		ok := true
		for _, s := range subs {
			if !strings.Contains(txt, s) {
				ok = false
				break
			}
		}
		if ok {
			return txt
		}
		time.Sleep(30 * time.Millisecond)
	}
	h.t.Fatalf("timeout waiting for %q; screen:\n%s", subs, txt)
	return ""
}

// waitRe waits until every regular expression matches the screen.
func (h *harness) waitRe(patterns ...string) string {
	h.t.Helper()
	res := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		res[i] = regexp.MustCompile(p)
	}
	deadline := time.Now().Add(5 * time.Second)
	var txt string
	for time.Now().Before(deadline) {
		txt = h.text()
		ok := true
		for _, re := range res {
			if !re.MatchString(txt) {
				ok = false
				break
			}
		}
		if ok {
			return txt
		}
		time.Sleep(30 * time.Millisecond)
	}
	h.t.Fatalf("timeout waiting for %q; screen:\n%s", patterns, txt)
	return ""
}

func (h *harness) keys(s string) {
	for _, r := range s {
		h.screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) key(k tcell.Key) {
	h.screen.InjectKey(k, 0, tcell.ModNone)
	time.Sleep(10 * time.Millisecond)
}

func (h *harness) command(c string) {
	h.keys(":" + c)
	h.key(tcell.KeyEnter)
}

func (h *harness) confirm() {
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
}

func TestHostsOverview(t *testing.T) {
	h := start(t, "")
	h.waitFor("Hosts", "VERSION", "<hosts>")
	h.waitRe(`new\s.*mr13\.3\.1\.4\s+up\s`, `old\s.*mr6\.5\.9\.12\s+up\s`)
	var txt string
	// space on the first row (old) makes it current, no marks here
	h.keys(" ")
	txt = h.waitFor("current host: old", "Host:    old")
	if !regexp.MustCompile(`\*\s+old\s`).MatchString(txt) || strings.Contains(txt, "✓") || strings.Contains(txt, "CALL-ID") {
		t.Fatalf("space should select the host and stay in the list:\n%s", txt)
	}
	// enter opens its sessions on top of the list
	h.key(tcell.KeyEnter)
	h.waitFor("Sessions", "old · all", "CALL-ID", "<hosts>", "<sessions>")
	h.key(tcell.KeyEsc)
	h.waitFor("Hosts", "VERSION")
}
func TestSessionsDetailsAndTerminate(t *testing.T) {
	h := start(t, "sessions")
	ids := h.srv["new"].CallIDs()
	h.waitFor("Sessions", "new · all", "PROXY", "OWNER")

	h.keys("/" + ids[0])
	h.key(tcell.KeyEnter)
	h.waitFor("</" + ids[0] + ">")
	h.key(tcell.KeyEnter)
	// parsed by default: path through rtpengine, legs, stream table
	h.waitFor("Call-ID:", ids[0], "Path", "rtpengine", "caller", "callee", "FROM PEER", "LAST PACKET", "kernel",
		"(priv)", "(pub)", "interface priv", "interface pub")
	h.keys("y")
	h.waitFor("Media #1", "Port", "callid:", "uts")
	h.keys("y")
	h.waitFor("Call-ID:", "FROM PEER")
	h.key(tcell.KeyEsc)
	h.waitFor("CALL-ID")

	h.key(tcell.KeyCtrlK)
	h.waitFor("Terminate 1 call(s) on new?")
	h.confirm()
	h.waitFor("terminated 1 call(s)")
	if got := h.srv["new"].CallIDs(); len(got) != len(ids)-1 {
		t.Fatalf("calls left %d, want %d", len(got), len(ids)-1)
	}
}

// fgAt is the foreground color of the first cell of s on the screen.
func (h *harness) fgAt(s string) tcell.Color {
	h.t.Helper()
	var fg tcell.Color
	found := false
	h.app.QueueUpdate(func() {
		cells, w, _ := h.screen.GetContents()
		for i := range cells {
			ok := true
			for j, r := range s {
				if i+j >= len(cells) || (i+j)/w != i/w || len(cells[i+j].Runes) == 0 || cells[i+j].Runes[0] != r {
					ok = false
					break
				}
			}
			if ok {
				fg, _, _ = cells[i].Style.Decompose()
				found = true
				return
			}
		}
	})
	if !found {
		h.t.Fatalf("%q not on screen:\n%s", s, h.text())
	}
	return fg
}

func TestCallViewTextKeys(t *testing.T) {
	h := start(t, "sessions")
	id := h.srv["new"].CallIDs()[0]
	txt := h.waitFor("Sessions", "PROXY", id)
	if !strings.Contains(txt, "</> filter") {
		t.Fatalf("a table view should hint / filter:\n%s", txt)
	}
	h.keys("/" + id)
	h.key(tcell.KeyEnter)
	h.waitFor("</" + id + ">")
	h.key(tcell.KeyEnter)
	txt = h.waitFor("Call-ID:", "FROM PEER", "<h/l> sideways")
	if strings.Contains(txt, "</> filter") {
		t.Fatalf("a text view has its own / search, not the filter:\n%s", txt)
	}
	keyColor := h.fgAt("Call-ID:")

	h.keys("n")
	h.waitFor("no search: press / to search")

	// the search highlights matches and keeps the colors of the rest
	h.keys("/RTP")
	h.key(tcell.KeyEnter)
	h.waitRe(`match 1/\d+`)
	if got := h.fgAt("Call-ID:"); got != keyColor {
		t.Fatalf("search lost the colors: Call-ID: is %v, was %v", got, keyColor)
	}
	h.keys("n")
	h.waitRe(`match 2/\d+`)
	h.keys("N")
	h.waitRe(`match 1/\d+`)

	h.keys("w")
	h.waitFor("wrap on")
	h.keys("w")
	h.waitFor("wrap off: h/l scroll sideways")
}

// appendLog adds lines about a call to the mock log of host, "<prefix> 1" …
func (h *harness) appendLog(host, id, prefix string, n int) {
	h.t.Helper()
	f, err := os.OpenFile(h.logs[host], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	for i := 1; i <= n; i++ {
		fmt.Fprintf(f, "%s rtpe-mock rtpengine[4242]: INFO: [%s]: %s %d\n", time.Now().Format("2006-01-02T15:04:05.000000-0700"), id, prefix, i)
	}
}

func TestCallLog(t *testing.T) {
	h := start(t, "sessions")
	id := h.srv["new"].CallIDs()[0]
	h.waitFor("Sessions", "PROXY", id, "<L> log")
	h.keys("/" + id)
	h.key(tcell.KeyEnter)
	h.waitFor("</" + id + ">")
	h.key(tcell.KeyEnter)
	h.waitFor("Call-ID:", "<L> log")

	h.keys("L")
	txt := h.waitFor("<log ", "Received command 'offer'", "Replying to 'answer'", "<f> follow")
	if strings.Count(txt, "rtpengine[4242]") != 4 {
		t.Fatalf("want the 4 lines of this call only:\n%s", txt)
	}
	// debug on from the call details, ctrl-r then reads its line
	h.key(tcell.KeyEsc)
	h.keys("D")
	h.waitFor("Per-call debug logging for")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	h.waitFor("Enabled debugging for call", "L in the call details shows its log")
	h.keys("L")
	h.waitFor("Received command 'offer'")
	h.key(tcell.KeyCtrlR)
	h.waitFor("DEBUG: [" + id + "]: Debug logging enabled")
}

func TestCallLogLongSavesAll(t *testing.T) {
	h := start(t, "sessions")
	id := h.srv["new"].CallIDs()[0]
	h.appendLog("new", id, "debug line", 5100)
	h.waitFor("Sessions", "PROXY", id)
	h.keys("/" + id)
	h.key(tcell.KeyEnter)
	h.waitFor("</" + id + ">")
	h.keys("L")
	h.waitFor("debug line 5100", "last 5000 of 5104 lines, s saves all")
	h.keys("s")
	h.waitFor("saved 5104 lines to")
	files, _ := filepath.Glob(filepath.Join(h.dir, "log_*.log"))
	if len(files) != 1 {
		t.Fatalf("saved files: %v", files)
	}
	data, _ := os.ReadFile(files[0])
	if n := strings.Count(string(data), "\n"); n != 5104 || !strings.Contains(string(data), "Received command 'offer'") {
		t.Fatalf("saved %d lines, want the whole log of 5104", n)
	}
}

func TestCallLogFollow(t *testing.T) {
	h := start(t, "sessions")
	id := h.srv["new"].CallIDs()[0]
	h.appendLog("new", id, "older line", 120)
	h.waitFor("Sessions", "PROXY", id)
	h.keys("/" + id)
	h.key(tcell.KeyEnter)
	h.waitFor("</" + id + ">")
	// L in the list opens the log of the selected call (not the vim L of tables)
	h.keys("L")
	// opens at the bottom
	txt := h.waitFor("<log ", "older line 120")
	if strings.Contains(txt, "Received command 'offer'") {
		t.Fatalf("the log should open at the bottom:\n%s", txt)
	}

	h.app.QueueUpdate(func() { h.app.SetRefresh(100 * time.Millisecond) })
	h.keys("f")
	h.waitFor("following the log", "· following")
	h.appendLog("new", id, "fresh line", 3)
	txt = h.waitFor("fresh line 3")
	if strings.Count(txt, "fresh line 1") != 1 {
		t.Fatalf("new lines should come once:\n%s", txt)
	}
	// scrolled up, new lines do not pull the view down
	h.key(tcell.KeyCtrlU)
	h.key(tcell.KeyCtrlU)
	h.appendLog("new", id, "later line", 1)
	time.Sleep(400 * time.Millisecond)
	if strings.Contains(h.text(), "later line 1") {
		t.Fatalf("the view jumped to the end while scrolled up:\n%s", h.text())
	}
	h.keys("G")
	h.waitFor("later line 1")
	h.keys("f")
	h.waitFor("not following the log")
}

func TestOptionCardAndWideTable(t *testing.T) {
	h := start(t, "config")
	h.waitFor("Config", "DESCRIPTION", "port-max")
	if strings.Contains(h.text(), "<h/l> sideways") {
		t.Fatalf("the table fits 220 columns, no h/l hint:\n%s", h.text())
	}
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(110, 40) })
	h.waitFor("<h/l> sideways")
	// o picks the sort column, O reverses it; both say so
	h.keys("o")
	h.waitFor("sort by DAEMON ↑")
	h.keys("O")
	h.waitFor("sort by DAEMON ↓")

	h.keys("/silent-timeout")
	h.key(tcell.KeyEnter)
	h.waitFor("</silent-timeout>")
	h.key(tcell.KeyEnter)
	txt := h.waitFor("option silent-timeout", "Value", "in the file", "3600", "1800 · differs from the file: changed at runtime",
		"status", "runtime differs", "Option", "releases", "since mr", "on this host", "yes, mr13.3.1.4")
	if strings.Contains(txt, "Present in") {
		t.Fatalf("the card lists releases as ranges:\n%s", txt)
	}
	if got, want := h.fgAt("1800"), theme.Warn; got.TrueColor() != want.TrueColor() {
		t.Fatalf("a runtime value that differs is %v, want warn %v", got, want)
	}
}

func TestStatsPanel(t *testing.T) {
	h := start(t, "stats")
	h.waitFor("Statistics", "Sessions · now", "Traffic · per second, now", "Since start", "managed sessions",
		"Quality · averages over sampled streams", "MOS", "<y> JSON/parsed")
	h.keys("G")
	h.waitFor("NG commands", "offer", "Interfaces", "pub", "Transcoders · per second, now; total since the start",
		"NOW", "SAMPLES", "PCMA/8000 <> opus/48000/2", "25.8 kB/s", "total 9 120 000 packets")
	h.keys("g")
	// y: the JSON as rtpengine printed it, s saves it
	h.keys("y")
	h.waitFor(`"controlstatistics": {`, `"proxies": [`)
	h.keys("/currentstatistics")
	h.key(tcell.KeyEnter)
	h.waitFor(`"currentstatistics": {`, "match 1/1")
	h.keys("s")
	h.waitFor("saved ")
	files, _ := filepath.Glob(filepath.Join(h.dir, "stats_new-*.json"))
	if len(files) != 1 {
		t.Fatalf("saved files: %v", files)
	}
	data, _ := os.ReadFile(files[0])
	if _, err := rtpengine.ParseJSONStats(string(data)); err != nil {
		t.Fatalf("saved JSON does not parse: %v", err)
	}
	h.keys("y")
	h.waitFor("Statistics", "Since start")

	// before mr8.5 there is no JSON: list counters and list totals as text
	h.command("nodes old")
	h.command("stats")
	h.waitFor("Current per-second counters:", "Statistics over lifetime:", "Control statistics:")
	h.keys("y")
	h.waitFor("list jsonstats needs mr8.5+")
}

func TestConfigViewFlagsVersionIssues(t *testing.T) {
	h := start(t, "host old")
	h.command("config")
	txt := h.waitFor("Config", "max-sesions", "unknown", "io-uring", "added in mr12.5", "runtime differs", "recording")
	if !strings.Contains(txt, "1800") {
		t.Fatalf("runtime value of silent-timeout missing:\n%s", txt)
	}
	h.keys("v")
	h.keys("v")
	h.waitFor("issues")
	h.keys("/io-uring")
	h.key(tcell.KeyEnter)
	h.key(tcell.KeyEnter)
	h.waitFor("Use io_uring", "releases", "since mr12.5", "on this host", "no: mr6.5.9.12 does not have it")
	h.key(tcell.KeyEsc)

	// the same file on mr13.3 has no version findings for io-uring
	h.command("host new")
	h.command("config")
	h.keys("/io-uring")
	h.key(tcell.KeyEnter)
	txt = h.waitFor("io-uring", "Use io_uring")
	if strings.Contains(txt, "added in") {
		t.Fatalf("io-uring flagged on mr13.3:\n%s", txt)
	}
}

func TestLimitsSetAndVersionGates(t *testing.T) {
	h := start(t, "limits")
	h.waitFor("Limits", "maxsessions", "2000", "deletedelay")
	h.command("set timeout 120")
	h.waitFor("set timeout 120 on new?")
	h.confirm()
	h.waitFor("Success setting timeout to 120")
	if v := h.srv["new"].Get("timeout"); v != "120" {
		t.Fatalf("mock timeout = %s", v)
	}
	h.waitRe(`timeout\s+120\s`)
	// a cancel says so instead of leaving the old value silently
	h.command("set timeout 90")
	h.waitFor("set timeout 90 on new?")
	h.key(tcell.KeyEsc)
	h.waitFor("cancelled")
	if v := h.srv["new"].Get("timeout"); v != "120" {
		t.Fatalf("mock timeout = %s after cancel", v)
	}

	h.command("host old")
	h.command("limits")
	txt := h.waitRe(`Limits\[\d+\] old `)
	if strings.Contains(txt, "deletedelay") {
		t.Fatalf("deletedelay shown on mr6.5:\n%s", txt)
	}
	h.command("interfaces")
	h.waitFor(`"list interfaces" needs rtpengine mr7.5+`)
	h.command("set redisdisable 1")
	h.waitFor(`"set redisdisable" needs rtpengine mr11.5+`)
}

func TestThemesAndHelp(t *testing.T) {
	h := start(t, "sessions")
	h.waitFor("CALL-ID")
	h.command("theme dracula")
	h.waitFor("dev · dracula")
	if theme.Bg != tcell.GetColor("#282a36") {
		t.Fatal("theme colors not applied")
	}
	h.command("themes")
	h.waitFor("Themes", "gruvbox", "catppuccin")
	h.command("help")
	h.waitFor("Navigation (vim)", "Sessions", "ctrl-k")
	h.keys("G")
	h.waitFor(":config, conf, cfg", "catppuccin")
	h.key(tcell.KeyEsc)
	h.command("describe")
	h.waitFor("CLI commands · CLI help", "list numsessions", "since mr6.5", "version", "source: config")
	if got, want := h.fgAt("yes  since"), theme.OK; got.TrueColor() != want.TrueColor() {
		t.Fatalf("a supported command is %v, want ok %v", got, want)
	}
	h.command("bogus")
	h.waitFor("unknown command")
}

func TestInterfacesDrillDown(t *testing.T) {
	h := start(t, "interfaces")
	txt := h.waitFor("Interfaces", "pub", "priv", "USAGE")
	if strings.Contains(txt, "OTHER") || strings.Contains(txt, "Ingress") {
		t.Fatalf("table should hold port usage only:\n%s", txt)
	}
	h.key(tcell.KeyEnter)
	h.waitFor("interface pub/", "Ports", "range", "used", "Traffic", "PACKETS", "ingress", "egress", "lost packets",
		"Quality · averages over sampled streams", "MOS", "jitter", "· measured")
	if got, want := h.fgAt("4.2"), theme.OK; got.TrueColor() != want.TrueColor() {
		t.Fatalf("MOS 4.2 is %v, want ok %v", got, want)
	}
	h.key(tcell.KeyEsc)
	h.keys("j")
	h.key(tcell.KeyEnter)
	h.waitFor("interface priv/", "no samples yet")
}

func TestProxiesGroupAndDrillDown(t *testing.T) {
	h := start(t, "proxies")
	h.waitFor("Proxies", "PROXY", "SOCKETS", "LAST CALL")
	txt := h.waitRe(`calls from \d+ proxies`, `10\.10\.0\.\d+\s+\d+\s+[\d.]+%`)
	// the mock proxies send NG from several worker ports: grouped by address
	if regexp.MustCompile(`10\.10\.0\.\d+:\d+`).MatchString(txt) {
		t.Fatalf("proxy shown with a port:\n%s", txt)
	}
	h.key(tcell.KeyEnter)
	txt = h.waitRe(`Sessions\[\d+\] new · all · proxy (10\.10\.0\.\d+)`)
	proxy := regexp.MustCompile(`· proxy (10\.10\.0\.\d+)`).FindStringSubmatch(txt)[1]
	for _, m := range regexp.MustCompile(`(10\.10\.0\.\d+):\d+`).FindAllStringSubmatch(txt, -1) {
		if m[1] != proxy {
			t.Fatalf("call from %s in the list of %s:\n%s", m[1], proxy, txt)
		}
	}
	h.key(tcell.KeyEsc)
	h.waitFor("Proxies", "SOCKETS")

	// NG commands per proxy from jsonstats; the mock proxy with errors is marked
	txt = h.waitRe(`NG: commands since the start`, `OFFER AVG`, `10\.10\.0\.5 .*\s3\s+0\.82 ms`)
	if !regexp.MustCompile(`10\.10\.0\.6 .*\s0\s+0\.82 ms`).MatchString(txt) {
		t.Fatalf("NG columns missing:\n%s", txt)
	}
	errCell := regexp.MustCompile(`3\s+0\.82 ms`).FindString(regexp.MustCompile(`10\.10\.0\.5 .*`).FindString(txt))
	if got, want := h.fgAt(errCell), theme.Warn; got.TrueColor() != want.TrueColor() {
		t.Fatalf("NG errors are %v, want warn %v", got, want)
	}
	// before mr8.5 no jsonstats: the NG columns stay empty
	h.command("host old")
	h.command("proxies")
	txt = h.waitRe(`Proxies\[\d+\] old`)
	if strings.Contains(txt, "ms ") || strings.Contains(txt, "NG: commands") {
		t.Fatalf("mr6.5 has no NG statistics:\n%s", txt)
	}
}

func TestConfigTemplateExplained(t *testing.T) {
	h := start(t, "config")
	h.waitFor("Config", "WebRTC", "templates")
	h.keys("/strict-sorce")
	h.key(tcell.KeyEnter)
	h.waitRe(`templates\s+offer\s.*unknown`)
	h.key(tcell.KeyEnter)
	h.waitFor("Template offer", "applied to every offer message", "strict-sorce", "unknown flag",
		"replace-origin", "Replace the address", "codec strip", "RTP/AVP", "As written")
	h.key(tcell.KeyEsc)
	h.key(tcell.KeyEsc)
	h.keys("/WebRTC")
	h.key(tcell.KeyEnter)
	h.key(tcell.KeyEnter)
	h.waitFor("Template WebRTC", "template=WebRTC", "trickle-ICE", "force →", "offer →", "require →")
}

func TestLanguageSwitch(t *testing.T) {
	h := start(t, "")
	t.Cleanup(func() { _ = i18n.Set("en") })
	h.waitFor("Hosts", "VERSION", "STATUS")
	h.command("lang ru")
	// headers, hints and header info switch on the open screen
	h.waitFor("Хосты", "ВЕРСИЯ", "СТАТУС", "сессии", "Процесс:", "язык ru", "работает")
	h.key(tcell.KeyEnter)
	h.waitFor("Сессии", "ВЛАДЕЛЕЦ", "детали")
	h.key(tcell.KeyEnter)
	h.waitFor("Звонок", "Медиа:", "RTP идёт в обе стороны", "ОТ ПИРА", "назад")
	h.command("lang en")
	h.waitFor("Call", "Media:", "RTP flowing both ways", "FROM PEER")
	h.command("lang xx")
	h.waitFor(`unknown language "xx"`)
}

// CLI command names are data: the host card shows them untranslated.
func TestInfoKeepsCommandNames(t *testing.T) {
	h := start(t, "")
	t.Cleanup(func() { _ = i18n.Set("en") })
	h.waitFor("Hosts", "VERSION")
	h.command("lang ru")
	h.command("info")
	// the log levels load after the card opens and make it longer: wait for
	// them, or G lands above the end
	h.waitFor("describe new", "e: изменить", "core")
	h.keys("G")
	h.waitRe(`terminate\s+да`, `active\s+да`, `debug\s+да`, `help\s+да`)
}

// The statistics are one panel; the old command names open it too.
func TestStatsAliases(t *testing.T) {
	h := start(t, "")
	h.waitFor("Hosts", "VERSION")
	for _, c := range []string{"totals", "counters", "jsonstats", "json", "transcoders"} {
		h.command("hosts")
		h.waitFor("<hosts>", "VERSION")
		h.command(c)
		h.waitFor("<stats>", "Statistics", "Since start")
	}
}

// The log levels are in the host card; :loglevel opens it.
func TestLogLevelsInInfo(t *testing.T) {
	h := start(t, "loglevel")
	h.waitFor("describe new", "Logging · e: change", "core", "6 info", "Everything that isn't part of another subsystem",
		"ice", "ICE negotiation")
	// before mr9.5: one level for the daemon
	h.command("host old")
	h.command("info")
	h.waitFor("describe old", "Logging", "log level", "6 info")
}

func TestLogLevelsChange(t *testing.T) {
	h := start(t, "info")
	h.waitFor("Logging · e: change", "<e> log levels")
	h.keys("e")
	h.waitFor("Log levels", "SUBSYSTEM", "core", "ICE negotiation", "<+/-> level up/down")
	srv := h.srv["new"]

	// + on the first row (core) raises it alone
	h.keys("+")
	h.waitFor("core: 7 debug · Success setting loglevel to 7")
	if srv.LogLevel("core") != 7 || srv.LogLevel("ice") != 6 {
		t.Fatalf("core %d, ice %d", srv.LogLevel("core"), srv.LogLevel("ice"))
	}
	h.keys("+")
	h.waitFor("log levels go from 1 (alert) to 7 (debug)")

	// enter types the level in the prompt
	h.key(tcell.KeyEnter)
	h.keys("3")
	h.key(tcell.KeyEnter)
	h.waitFor("set loglevel core 3 on new?")
	h.confirm()
	h.waitFor("Success setting loglevel to 3")
	if srv.LogLevel("core") != 3 {
		t.Fatalf("core %d", srv.LogLevel("core"))
	}

	// rtpengine would report success for both: checked here
	h.command("set loglevel bogus 5")
	h.confirm()
	h.waitFor(`no log subsystem "bogus"`)
	h.command("set loglevel 9")
	h.confirm()
	h.waitFor("log level 9 is out of range")

	// before mr9.5: one level
	h.command("host old")
	h.command("info")
	h.waitFor("describe old", "log level")
	h.keys("e")
	h.waitFor("Log levels", "(all)")
	h.keys("-")
	h.waitFor("all subsystems: 5 notice")
	if n := h.srv["old"].LogLevel(""); n != 5 {
		t.Fatalf("old level %d", n)
	}
}
