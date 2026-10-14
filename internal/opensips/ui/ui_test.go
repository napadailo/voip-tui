package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/opensips/config"
	"github.com/napadailo/voip-tui/internal/opensips/mock"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

type harness struct {
	t      *testing.T
	app    *App
	screen tcell.SimulationScreen
}

func start(t *testing.T, cmd string, readOnly bool) *harness {
	t.Helper()
	mock.Reset()
	cfg := mock.Demo()
	cfg.RefreshSeconds = 60
	cfg.DownloadDir = t.TempDir()
	host, _ := cfg.Current()
	theme.Apply("default")
	app := New(cfg, host, readOnly, mock.NewTransport)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(200, 45)
	app.SetScreen(screen)
	done := make(chan struct{})
	go func() {
		_ = app.Start(cmd)
		close(done)
	}()
	t.Cleanup(func() {
		app.Stop()
		<-done
	})
	time.Sleep(50 * time.Millisecond)
	app.QueueUpdateDraw(func() { screen.SetSize(200, 45) })
	return &harness{t: t, app: app, screen: screen}
}

func (h *harness) text() string {
	var out string
	h.app.QueueUpdate(func() {
		cells, w, _ := h.screen.GetContents()
		var b strings.Builder
		for i, c := range cells {
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			} else {
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

func (h *harness) rows() int {
	n := 0
	h.app.QueueUpdate(func() {
		if t := h.app.TopTable(); t != nil {
			n = t.VisibleCount()
		}
	})
	return n
}

func TestInfoDialogsAndEnd(t *testing.T) {
	h := start(t, "info", false)
	h.waitRe(`via cli \(opensips-cli\) · role=edge`, `version\s+3\.4\.7`, `process\s+\d+ up \d+d\w*\s+/usr/sbin/opensips -P`,
		`packages\s+opensips 3\.4\.7-1`, `config\s+/etc/opensips/opensips\.cfg`, `probed\s+[\d:]+ · in`,
		`Logging`, `log_level\s+3 info`, `xlog_level\s+2 notice`, `dlg_list\s+yes`,
		`Host:\s+edge-n1 mock-3\.4\.7`, `Version:\s+3\.4\.7`, `MI:\s+opensips-cli`, `Process:\s+pid \d+ · up \d+d`)
	h.command("dlg")
	h.waitRe(`Dialogs\[\d+\] edge-n1`)
	h.waitFor("CALL-ID", "confirmed", "<D>")
	before := h.rows()

	// mark two dialogs, end both with one confirmation
	h.keys("  ")
	h.keys("D")
	h.waitFor("End dialog on edge-n1 (OpenSIPS 3.4.7)?", "opensips-cli -x mi dlg_end_dlg dialog_id=")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	h.waitFor("end dialog × 2")
	deadline := time.Now().Add(3 * time.Second)
	for h.rows() != before-2 && time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
	}
	if got := h.rows(); got != before-2 {
		t.Fatalf("rows %d, want %d", got, before-2)
	}

	// y switches a card to its JSON and back
	h.keys("d")
	h.waitFor("Dialog", "state", "Caller", "Callee", "CSeq", "<y> JSON/parsed")
	h.keys("y")
	h.waitFor(`"callid"`, `"from_uri"`)
	h.keys("y")
	h.waitFor("Caller", "Callee")
	h.key(tcell.KeyEsc)
	h.waitRe(`Dialogs\[\d+\] edge-n1`)
	// in a table y opens the row's JSON, y again closes it
	h.keys("y")
	h.waitFor(`"callid"`, `"from_uri"`, "<y> back to the table")
	h.keys("y")
	h.waitRe(`Dialogs\[\d+\] edge-n1`)

	// and the host card
	h.command("info")
	h.waitFor("Logging")
	h.keys("y")
	h.waitFor(`"host": {`, `"Via": "cli"`)
	h.keys("y")
	h.waitFor("Logging", "MI commands")
}

// d and enter open a card; fields the card does not know stay under Other.
func TestCards(t *testing.T) {
	h := start(t, "contacts", false)
	t.Cleanup(func() { _ = i18n.Set("en") })
	h.waitRe(`Contacts\[\d+\] edge-n1`)
	h.waitFor("sip:1000@")
	h.key(tcell.KeyEnter)
	h.waitFor("Registration", "expires in", "Call-ID", "User-Agent", "Network", "socket", "AOR 10", "table location")
	h.key(tcell.KeyEsc)
	h.command("dispatcher")
	h.waitRe(`Dispatcher\[\d+\] edge-n1`)
	h.waitFor("media-1-1") // rows are there, not only the title
	h.keys("d")
	h.waitRe(`Destination`, `state\s+Active`, `weight\s+1`, `set 1 · partition default`)
	h.key(tcell.KeyEsc)
	h.command("gateways")
	h.waitRe(`Gateways\[\d+\] edge-n1`)
	h.waitFor("carrier-a")
	h.keys("d")
	h.waitRe(`Gateway`, `state\s+(Active|Inactive)`, `partition`)
	h.key(tcell.KeyEsc)
	h.command("hosts")
	h.waitFor("Hosts", "VERSION")
	h.keys("d")
	h.waitRe(`mock-3\.4\.7 · via cli \(opensips-cli\) · role=edge`, `version\s+3\.4\.7`, `MI commands`, `dlg_end_dlg\s+yes`)
	h.key(tcell.KeyEsc)
	// an open card follows :lang
	h.command("dialogs")
	h.waitRe(`Dialogs\[\d+\] edge-n1`)
	h.keys("d")
	h.waitFor("Caller")
	h.command("lang ru")
	h.waitFor("Вызывающий", "Вызываемый", "состояние", "сокет")
}

func TestCardKeepsUnknownFields(t *testing.T) {
	c := tui.StripTags(gatewayCard(map[string]any{"ID": "gw1", "IP": "192.0.2.10", "State": "Active", "probe_mode": "2"}).String())
	if !strings.Contains(c, "Other") || !strings.Contains(c, "probe_mode  2") || strings.Contains(c, "\n  ID ") {
		t.Fatalf("unknown fields under Other, known ones once:\n%s", c)
	}
}

func TestVersionAwareness(t *testing.T) {
	h := start(t, "hosts", false)
	h.waitFor("Hosts", "running 3.4.7, installed 3.4.9", "Connection refused", "opensips-cli needs 3.0+")
	// address, via, version, status, dialogs and labels; a host whose mi_http is off says why
	h.waitRe(`edge-n1\s+mock-3\.4\.7\s+cli\s+3\.4\.7\s+OK\s+\d+\s+role=edge`,
		`core-fifo\s+mock-3\.6\.1-fifo\s+fifo\s+3\.6\.1\s+OK`, `core-http\s+mock-3\.6\.1-http\s+http\s+3\.6\.1\s+OK`,
		`edge-direct\s+http://127\.0\.0\.1:18888/mi\s+direct\s+3\.4\.7\s+OK`, `lab-nohttp\s.*connection refused`,
		`legacy-24\s+mock-2\.4\.11\s+cli → opensipsctl\s+2\.4\.11`)
	h.command("host lab-31")
	h.command("info")
	h.waitRe(`Host:\s+lab-31`, `sr_list_status\s+no\s+status/report · status/report needs OpenSIPS 3\.2\+`, `ds_list\s+no`)
	h.command("sr")
	h.waitFor("needs OpenSIPS 3.2+, host runs 3.1.10")
	h.command("ds")
	h.waitFor("no MI command ds_list on the host: module not loaded")
	h.command("host legacy-24")
	h.waitRe(`Host:\s+legacy-24`, `via cli \(opensipsctl fifo\)`, `version\s+2\.4\.11`)
}

func TestSwitchHostKeepsView(t *testing.T) {
	h := start(t, "gw", false)
	h.waitRe(`Gateways\[\d+\] edge-n1`)
	h.waitFor("carrier-a", "Inactive")
	h.command("host core-36")
	h.waitRe(`Gateways\[\d+\] core-36`)
	h.waitFor("carrier-a")
}

func TestToggleGatewayAndReadOnly(t *testing.T) {
	h := start(t, "gw", false)
	h.waitRe(`Gateways\[\d+\] edge-n1`)
	h.waitFor("carrier-a")
	h.keys("/carrier-a")
	h.key(tcell.KeyEnter)
	h.waitFor("</carrier-a>")
	h.keys("T")
	// an active gateway is disabled: the question and the button say so
	h.waitFor("Disable on edge-n1 (OpenSIPS 3.4.7)?", "dr_gw_status gw_id=carrier-a", "partition_name=external status=0", "Disable")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	h.waitFor("Disabled MI")

	ro := start(t, "gw", true)
	ro.waitRe(`Gateways\[\d+\] edge-n1`)
	txt := ro.waitFor("read-only")
	if strings.Contains(txt, "<T>") {
		t.Fatal("toggle offered in read-only mode")
	}
}

func TestRawMIAndThemes(t *testing.T) {
	h := start(t, "info", false)
	h.waitFor("Logging")
	h.command("mi get_statistics dialog:")
	h.waitFor(`"dialog:active_dialogs"`)
	h.key(tcell.KeyEsc)
	h.command("mi dr_reload")
	h.waitFor("This MI command may change edge-n1")
	h.key(tcell.KeyEnter) // Cancel is focused
	h.command("theme nord")
	h.waitFor("· nord · en")
	h.command("theme")
	h.waitFor("Themes", "dracula", "tokyo-night")
	h.keys("j")
	h.key(tcell.KeyEsc)
	h.waitFor("· nord · en")
}

func TestHelpShowsAvailability(t *testing.T) {
	h := start(t, "host lab-31", false)
	h.waitRe(`Dialogs\[\d+\] lab-31`)
	h.keys("?")
	h.waitFor("Navigation (vim)")
	// the host section is in the middle of the help: search for it
	h.keys("/dispatcher   no MI")
	h.key(tcell.KeyEnter)
	h.waitFor("On lab-31 (OpenSIPS 3.1.10)", "dispatcher   no MI command ds_list")
}

func TestIsSafeMI(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		args []string
		safe bool
	}{{"uptime", nil, true}, {"ds_list", nil, true}, {"dr_gw_status", nil, true},
		{"dr_gw_status", []string{"gw_id=1", "status=0"}, false}, {"dr_reload", nil, false}, {"log_level", []string{"4"}, false}} {
		if IsSafeMI(c.cmd, c.args) != c.safe {
			t.Errorf("%s %v", c.cmd, c.args)
		}
	}
}

func TestLanguageSwitch(t *testing.T) {
	h := start(t, "info", false)
	t.Cleanup(func() { _ = i18n.Set("en") })
	h.waitFor("Logging", "Host:")
	h.command("lang ru")
	// the open view and the header switch at once
	h.waitFor("Логирование", "MI-команды", "Хост:", "Версия:", "Процесс:", "Сборка:", "работает")
	h.command("hosts")
	h.waitFor("Хосты", "ВЕРСИЯ", "АДРЕС", "ДИАЛОГИ", "МЕТКИ", "установлен 3.4.9")
	h.command("host lab-31")
	h.command("dispatcher")
	h.waitFor("на хосте нет MI-команды ds_list: модуль не загружен")
	h.command("lang en")
	h.waitFor("DURATION")
	h.command("dispatcher")
	h.waitFor("no MI command ds_list on the host")
}

// The host list works as in rtpengine-tui: space makes a host current,
// enter opens its default view over the list, d its card, :<host> switches.
func TestHostsKeys(t *testing.T) {
	h := start(t, "", false)
	h.waitFor("Hosts", "ADDRESS", "DIALOGS") // more than one host: the list first
	h.keys("j")
	h.keys(" ")
	h.waitRe(`current host: edge-n2`, `\*\s+edge-n2`, `Host:\s+edge-n2`, `<enter> dialogs`)
	// enter opens the dialogs, as sessions in rtpengine-tui, whatever defaultView says
	h.app.QueueUpdate(func() { h.app.cfg.DefaultView = "info" })
	h.key(tcell.KeyEnter)
	h.waitRe(`Dialogs\[\d+\] edge-n2`)
	h.key(tcell.KeyEsc)
	h.waitFor("Hosts", "ADDRESS")
	h.keys("j")
	h.keys("d")
	h.waitRe(`info core-36`, `version\s+3\.6\.1`)
	h.key(tcell.KeyEsc)
	h.command("lab-31")
	h.waitRe(`Host:\s+lab-31`)
	h.command("node core-36")
	h.waitRe(`Host:\s+core-36`)
}

// e in the host card opens the log levels: log_level of all processes and
// xlog_level; + and - change one step right away, read-only offers no change.
func TestLogLevels(t *testing.T) {
	h := start(t, "info", false)
	h.waitFor("xlog_level")
	h.keys("e")
	h.waitRe(`Log levels\[2\] edge-n1`, `log_level\s+3\s+info\s+all 14 processes`, `xlog_level\s+2\s+notice\s+xlog\(\) in the script`)
	h.keys("+")
	h.waitRe(`log_level: 4 debug, until OpenSIPS restarts`, `log_level\s+4\s+debug\s+all 14 processes`)
	h.keys("j")
	h.keys("-")
	h.waitRe(`xlog_level: 1 warn`, `xlog_level\s+1\s+warn`)
	h.keys("-")
	h.waitRe(`xlog_level: -1 err`) // no level 0
	h.key(tcell.KeyEnter)
	h.waitFor("mi xlog_level level=")

	ro := start(t, "info", true)
	ro.waitFor("xlog_level")
	ro.keys("e")
	ro.waitRe(`log_level\s+3`)
	ro.keys("+")
	ro.waitFor("read-only mode")
}

// A disabled rtpengine node is enabled; the confirmation says Enable.
func TestToggleSaysEnableOrDisable(t *testing.T) {
	h := start(t, "rtpengine", false)
	h.waitRe(`RTPengine\[\d+\] edge-n1`)
	h.waitFor("udp:10.20.0.11:2223")
	h.keys("/10.20.0.11")
	h.key(tcell.KeyEnter)
	h.waitFor("</10.20.0.11>")
	h.keys("T")
	h.waitFor("Enable on edge-n1 (OpenSIPS 3.4.7)?", "rtpengine_enable enable=1 url=udp:", "Cancel     Enable")
}

// :stats is a card by category; a refresh gives rates, y the JSON, a every
// statistic as a table; switching hosts keeps the panel.
func TestStatsPanel(t *testing.T) {
	h := start(t, "stats", false)
	h.waitFor("Statistics", "SIP messages", "requests received", "PER SECOND", "TOTAL", "Transactions", "Dialogs")
	h.waitRe(`active\s+\d+`)
	h.keys("G")
	h.waitFor("Shared memory", "Load", "Private memory (pkmem)")
	h.waitRe(`process 0\s+[\d.]+K / 16\.0M`)
	h.waitRe(`used\s+[\d.]+M / 256\.0M`)
	h.keys("g")
	time.Sleep(1100 * time.Millisecond)
	h.key(tcell.KeyCtrlR)
	h.waitRe(`requests received\s+\d+\.\d\s+[\d ]+`)
	h.keys("y")
	h.waitFor(`"core:rcv_requests"`)
	h.keys("y")
	h.waitFor("SIP messages")
	// a switches to every statistic and back, as y to JSON
	h.keys("a")
	h.waitRe(`All statistics\[\d+\] edge-n1`)
	h.waitFor("GROUP", "RATE/S", "<a> by category")
	h.keys("a")
	h.waitFor("SIP messages")
	// :allstats opened by itself goes to the panel with a too
	h.command("allstats")
	h.waitRe(`All statistics\[\d+\] edge-n1`)
	h.keys("a")
	h.waitFor("SIP messages")
	h.command("host core-36")
	h.waitRe(`Host:\s+core-36`, `Statistics\n|core-36 · 3\.6\.1`)
	h.waitFor("SIP messages")
}

// Every group gets a section: a module's statistics under their own names,
// the extra ones of shmem and load below the known lines, pkmem per process.
func TestStatsCardSections(t *testing.T) {
	cur := map[string]int64{
		"core:rcv_requests": 1100, "shmem:total_size": 1 << 20, "shmem:real_used_size": 1 << 18, "shmem:used_size": 200000,
		"load:load": 7, "load:load-proc-3": 12, "pkmem:0-real_used_size": 1 << 19, "pkmem:0-total_size": 1 << 20,
		"acc:acc_events": 50, "acc:failed_events": 2,
	}
	prev := map[string]int64{"core:rcv_requests": 1000, "acc:acc_events": 40, "acc:failed_events": 2}
	out := tui.StripTags(statsCard(&config.Host{Name: "x"}, nil, cur, prev, 2*time.Second).String())
	for _, want := range []string{`requests received\s+50\.0\s+1 100`, `Shared memory\n\s+used\s+256\.0K / 1\.0M \(25%\)`,
		`used_size\s+-\s+200 000`, `load-proc-3\s+-\s+12`, `process 0\s+512\.0K / 1\.0M \(50%\)`,
		`acc · statistics of the module`, `acc_events\s+5\.0\s+50`, `failed_events\s+0\.0\s+2`} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
}

// The SDP each side sent and got, after the parties; a callee field the card
// does not know stays in that callee's section.
func TestDialogCardSDP(t *testing.T) {
	d := map[string]any{"ID": "1", "callid": "c1@192.0.2.1", "state": 4, "from_uri": "sip:a@example.com", "to_uri": "sip:b@example.com",
		"caller_sdp": "v=0\r\nc=IN IP4 192.0.2.10\r\nm=audio 20000 RTP/AVP 8\r\n", "caller_sent_sdp": "v=0\r\nc=IN IP4 192.0.2.99\r\n",
		"CALLEES": []any{map[string]any{"callee_tag": "t2", "callee_sdp": "v=0\r\nc=IN IP4 192.0.2.20\r\n", "callee_sent_sdp": "",
			"callee_extra": "x"}}}
	out := tui.StripTags(dialogCard(d).String())
	for _, want := range []string{`Callee\n\s+URI\s+sip:b@example\.com\n\s+tag\s+t2\n\s+callee_extra\s+x`,
		`Caller SDP\n\s+received SDP\s+v=0\n\s+c=IN IP4 192\.0\.2\.10\n\s+m=audio 20000 RTP/AVP 8\n\s+sent SDP\s+v=0\n\s+c=IN IP4 192\.0\.2\.99`,
		`Callee SDP\n\s+received SDP\s+v=0\n\s+c=IN IP4 192\.0\.2\.20`} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "Other") {
		t.Errorf("SDP must not fall under Other:\n%s", out)
	}
}

// Attributes of a gateway or a destination read as key and value lines.
func TestCardAttributes(t *testing.T) {
	out := tui.StripTags(gatewayCard(map[string]any{"ID": "gw1", "IP": "192.0.2.10", "State": "Active", "ATTRS": "carrier=carrier-a;cps=50; region = eu"}).String())
	if !regexp.MustCompile(`Attributes\n\s+carrier\s+carrier-a\n\s+cps\s+50\n\s+region\s+eu`).MatchString(out) {
		t.Errorf("attributes not split:\n%s", out)
	}
	out = tui.StripTags(destinationCard(map[string]any{"URI": "sip:192.0.2.20", "set": "1", "attrs": "free text"}).String())
	if !regexp.MustCompile(`Attributes\n\s+free text`).MatchString(out) {
		t.Errorf("free-form attributes must stay as they are:\n%s", out)
	}
	if pairs, ok := splitAttrs("a=1,b=2"); !ok || len(pairs) != 2 || pairs[1] != [2]string{"b", "2"} {
		t.Errorf("comma pairs: %v %v", pairs, ok)
	}
}

// State, Methods and Flags of a contact read as words, from usrloc's
// cstate_t, the METHOD_* bits and FL_* (ul_dump prints numbers and codes).
func TestContactCardDecodes(t *testing.T) {
	card := func(c map[string]any) string {
		c["Contact"], c["AOR"], c["table"] = "sip:1000@192.0.2.5", "1000", "location"
		return tui.StripTags(contactCard(c).String())
	}
	out := card(map[string]any{"State": "CS_DIRTY", "Methods": 8063, "Flags": 5})
	for _, want := range []string{`state\s+CS_DIRTY\s+changed, not written to the database yet`,
		`methods\s+INVITE, CANCEL, ACK, BYE, INFO, OPTIONS, UPDATE, MESSAGE, SUBSCRIBE, NOTIFY, PRACK, REFER\s+8063`,
		`flags\s+FL_MEM\s+in memory only.*\n\s+FL_PN_ON\s+push notifications`} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
	out = card(map[string]any{"State": "CS_SYNC", "Methods": int64(4294967295), "Flags": 0})
	for _, want := range []string{`written to the database`, `methods\s+all: the REGISTER had no Allow`, `flags\s+none`} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
}
