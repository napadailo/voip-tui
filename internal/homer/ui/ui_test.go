package ui

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/internal/homer/config"
	"github.com/napadailo/voip-tui/internal/homer/mock"
)

type harness struct {
	t      *testing.T
	app    *App
	screen tcell.SimulationScreen
	srv    *mock.Server
	dl     string
}

func start(t *testing.T, cmd string) *harness {
	t.Helper()
	return startWith(t, cmd, false)
}

func startWith(t *testing.T, cmd string, useToken bool) *harness {
	t.Helper()
	return startHosts(t, cmd, func(url, token string) []config.Host {
		host := config.Host{Name: "test", URL: url, Username: "admin", Password: "sipcapture"}
		if useToken {
			host.Username, host.Password, host.Token = "", "", token
		}
		return []config.Host{host}
	})
}

// startHosts runs the app on the hosts hosts returns for the emulator's URL
// and API token; the first one is current. tweak changes the config.
func startHosts(t *testing.T, cmd string, hosts func(url, token string) []config.Host, tweak ...func(*config.Config)) *harness {
	t.Helper()
	srv := mock.New(7)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "none.yaml"))
	cfg.DownloadDir = t.TempDir()
	cfg.DefaultSince = "4h"
	for _, h := range hosts(ts.URL, srv.Token) {
		if err := cfg.AddHost(h); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range tweak {
		f(cfg)
	}
	app := New(cfg, &cfg.Hosts[0])
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
	})
	time.Sleep(50 * time.Millisecond)
	app.QueueUpdateDraw(func() { screen.SetSize(220, 50) })
	return &harness{t: t, app: app, screen: screen, srv: srv, dl: cfg.DownloadDir}
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

func (h *harness) waitNot(sub string) string {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var txt string
	for time.Now().Before(deadline) {
		if txt = h.text(); !strings.Contains(txt, sub) {
			return txt
		}
		time.Sleep(30 * time.Millisecond)
	}
	h.t.Fatalf("timeout waiting for %q to disappear; screen:\n%s", sub, txt)
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

// answered matches the rows of calls answered through the SBC to the PBX,
// by the form of their Call-ID: forwarded calls, fwd-…, have no RTCP.
const answered = `200 OK.*[0-9a-f]{8}-[0-9a-f]{4}@`

// filterAnswered filters the search to such calls; the first is selected.
func (h *harness) filterAnswered() {
	h.t.Helper()
	h.keys("/" + answered)
	h.key(tcell.KeyEnter)
	h.waitFor("</200 OK") // the title cuts the rest on a narrow screen
}

func (h *harness) command(c string) {
	h.keys(":" + c)
	h.key(tcell.KeyEnter)
}

func TestCallsSearchFlowAndExport(t *testing.T) {
	h := start(t, "")
	h.waitFor("SIP call(1_call)", "STATUS", "200 OK", "DURATION", "<1_call>", "Host:", "test", "User:", "admin (admin)", "Nodes:", "localnode")

	// raw messages view and live filter
	h.keys("v")
	h.waitFor("METHOD", "INVITE")
	h.keys("/BYE")
	h.key(tcell.KeyEnter)
	txt := h.waitFor("</BYE>")
	if strings.Contains(txt, " ACK ") {
		t.Fatalf("filter did not apply:\n%s", txt)
	}
	h.key(tcell.KeyEsc)
	h.waitNot("</BYE>")
	h.keys("v")
	h.waitFor("STATUS")

	// open an answered call
	h.filterAnswered()
	h.key(tcell.KeyEnter)
	h.waitFor("1 Flow", "SBC", "PBX", "▶", "Call-ID:")
	// the status line under the tabs: the answer, the talk, who hung up
	h.waitRe(`200 OK RNG  [\d.]+m?s  ASW  [\d.]+m?s  DUR  \d+(m\d+)?s  BYE  calle[re]  LEG  1  \d+ → \d+`)
	// BYE comes seconds after the answer: micro_ts of homer-app is in
	// milliseconds, read as microseconds every delta stays under a second
	h.waitRe(`\+[1-9]\d*\.\d{3}`)

	// messages tab and qos tab
	h.keys("2")
	h.waitFor("DESTINATION", "SIZE")
	h.keys("3")
	h.waitFor("QoS (RTCP / RTP)", "<v> QoS: table")
	// v draws the reports as graphs per stream and back
	h.keys("v")
	h.waitRe(`QoS graphs`, `10\.10\.0\.\d+\s+SBC\s+PBX`, `callee hears\s+│─+ ● \d\.\d`, `caller hears\s+│◀─+ ● \d\.\d`,
		`Summary`, `caller hears\s+● \w+\s+MOS`, `trouble`, `Over time`, `1 PBX → 10\.10\.0\.\d+\s+─+`, `<v> QoS: graphs`)
	h.keys("v")
	h.waitFor("QoS (RTCP / RTP)", "<v> QoS: table")
	// each tab shows its own row in the message pane: a report, then a log
	h.waitRe(`"report_blocks"`)
	h.keys("4")
	if txt := h.waitFor("pbx: call"); strings.Contains(txt, "report_blocks") {
		t.Fatal("the logs tab kept the QoS report in the message pane")
	}
	// w shows the logs whole and wrapped, and the table again
	h.waitFor("<w> wrap: off", "MESSAGE")
	h.keys("w")
	h.waitRe(`<w> wrap: on`, `\d\d:\d\d:\d\d\.\d{3}\s+192\.168\.10\.20 → 192\.168\.10\.1`, `pbx: call \d+ -> \d+ finished, cause=\d+`)
	h.keys("w")
	h.waitFor("<w> wrap: off", "MESSAGE")
	h.keys("3")
	if txt := h.waitRe(`"report_blocks"`); strings.Contains(txt, "pbx: call") {
		t.Fatal("the QoS tab kept the log in the message pane")
	}
	// :lang and r reload the call; the pane stays with the QoS tab
	h.command("lang ru")
	h.waitFor("QoS: таблица", "report_blocks")
	h.command("lang en")
	h.keys("r")
	time.Sleep(300 * time.Millisecond)
	if txt := h.waitRe(`"report_blocks"`); strings.Contains(txt, "INVITE sip:") {
		t.Fatal("a reload put a SIP message in the pane of the QoS tab")
	}
	h.keys("1")

	// pcap export of the whole call
	h.keys("p")
	h.waitFor("saved ")
	files, _ := filepath.Glob(filepath.Join(h.dl, "*.pcap"))
	if len(files) != 1 {
		t.Fatalf("pcap files: %v", files)
	}
	data, _ := os.ReadFile(files[0])
	if len(data) <= 24 || binary.LittleEndian.Uint32(data) != 0xa1b2c3d4 {
		t.Fatalf("bad pcap (%d bytes)", len(data))
	}

	// full message pager and back
	h.key(tcell.KeyEnter)
	h.waitFor("message #1", "Via:")
	h.key(tcell.KeyEsc)
	h.waitFor("1 Flow")
	h.key(tcell.KeyEsc)
	h.waitFor("SIP call(1_call)")
}

func TestCommandsAndQuery(t *testing.T) {
	h := start(t, "calls from_user=101 since=4h")
	txt := h.waitFor("from_user=101")
	rows := 0
	for _, line := range strings.Split(txt, "\n") {
		if strings.Contains(line, "@192.168.10.1") {
			rows++
			if !strings.Contains(line, " 101 ") {
				t.Fatalf("row without from_user=101: %q", line)
			}
		}
	}
	if rows == 0 {
		t.Fatalf("no rows for from_user=101:\n%s", txt)
	}
	h.command("reg")
	h.waitFor("SIP registration(1_registration)", "STATUS", "Forbidden")
	h.command("logs")
	h.waitFor("LOG default(100_default)", "pbx: call")
	h.command("bogus")
	h.waitFor("unknown command")
	h.command("calls nosuchfield=1")
	h.waitFor("unknown fields ignored: nosuchfield")
	h.command("help")
	h.waitFor("Navigation (vim)", "Call view")
	// the tool's sections are in the middle of the help: search for them
	h.keys("/Search syntax")
	h.key(tcell.KeyEnter)
	h.waitFor("Search syntax", "1_registration")
	h.key(tcell.KeyEsc)
	h.command("info")
	h.waitFor("Homer", "Database nodes", "localnode", "Modules", "loki", "Auth types", "internal", "Search profiles", "1_call")
	h.keys("y")
	h.waitFor(`"profile"`, `"nodes"`)
	h.keys("y")
	h.waitFor("Database nodes")
	h.command("mappings")
	h.waitFor("Protocol mappings", "registration")
}

func TestSettingsCRUD(t *testing.T) {
	h := start(t, "aliases")
	h.waitFor("Aliases", "SBC", "PBX")

	// edit with a scripted "editor"
	t.Setenv("HOMER_TUI_EDITOR", `sed -i.bak -e 's/"PBX"/"PBX-2"/'`)
	h.keys("/PBX")
	h.key(tcell.KeyEnter)
	h.keys("e")
	h.waitFor("PBX-2")
	found := false
	for _, k := range []string{"a1b2c3d4-0000-4000-8000-000000000002"} {
		if it := h.srv.Item("/alias", k); it != nil && it["alias"] == "PBX-2" {
			found = true
		}
	}
	if !found {
		t.Fatal("alias was not updated on the server")
	}

	// delete with confirmation
	h.keys("D")
	h.waitFor("Delete alias")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	deadline := time.Now().Add(3 * time.Second)
	for h.srv.Count("/alias") != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := h.srv.Count("/alias"); n != 1 {
		t.Fatalf("aliases left: %d", n)
	}

	// create from template
	t.Setenv("HOMER_TUI_EDITOR", `sed -i.bak -e 's/pbx-1/created-by-test/'`)
	h.key(tcell.KeyEsc)
	h.keys("c")
	h.waitFor("created-by-test")

	// an editor that comes back at once saved nothing: said so, no alias
	t.Setenv("HOMER_TUI_EDITOR", "true")
	h.keys("c")
	h.waitFor("came back at once", "HOMER_TUI_EDITOR")
	if n := h.srv.Count("/alias"); n != 2 {
		t.Fatalf("aliases after a quick editor: %d", n)
	}
	// the template saved as it is: asked, then created
	t.Setenv("HOMER_TUI_EDITOR", "sleep 1.2 #")
	h.keys("c")
	h.waitFor("The template is saved as it is")
	h.key(tcell.KeyEnter)
	h.waitFor("pbx-1")
	if n := h.srv.Count("/alias"); n != 3 {
		t.Fatalf("aliases after the template as it is: %d", n)
	}

	// every resource view loads
	for _, name := range []string{"users", "tokens", "mappings", "hepsub", "advanced", "usersettings", "agents", "dashboards", "nodes", "ctx"} {
		h.command(name)
		h.waitFor("<" + strings.Replace(name, "ctx", "hosts", 1) + ">")
	}
}

// With an API token, settings changes are rejected by homer-app; the user gets a hint.
func TestTokenCannotChangeAliases(t *testing.T) {
	h := startWith(t, "aliases", true)
	h.waitFor("Aliases", "SBC")
	t.Setenv("HOMER_TUI_EDITOR", `sed -i.bak -e 's/pbx-1/by-token/'`)
	h.keys("c")
	h.waitFor("API tokens have user rights")
	if n := h.srv.Count("/alias"); n != 2 {
		t.Fatalf("alias must not be created with a token, have %d", n)
	}
}

// Esc in the command or filter prompt used to deadlock the UI.
func TestPromptEsc(t *testing.T) {
	h := start(t, "")
	h.waitFor("SIP call(1_call)", "200 OK")
	for _, open := range []string{":", ":re", "/", "/OK"} {
		h.keys(open)
		h.key(tcell.KeyEsc)
		h.waitFor("SIP call(1_call)") // the UI still answers
	}
	h.command("reg")
	h.waitFor("SIP registration(1_registration)")
}

func TestLaneCells(t *testing.T) {
	cells := laneCells(3, 0, 2, 10, "INVITE", 0, 30)
	line := strings.Join(cells, "")
	if !strings.Contains(line, "INVITE") || !strings.Contains(line, "▶") || len([]rune(line)) != 30 {
		t.Fatalf("bad lane: %q", line)
	}
	back := strings.Join(laneCells(3, 2, 1, 10, "200 OK", 0, 30), "")
	if !strings.Contains(back, "◀") || !strings.Contains(back, "200 OK") {
		t.Fatalf("bad back lane: %q", back)
	}
	// the first host scrolled away: the label stays whole on the others
	if shown := strings.Join(laneCells(3, 0, 2, 10, "INVITE", 10, 30)[1:], ""); !strings.Contains(shown, "INVITE") || !strings.Contains(shown, "▶") {
		t.Fatalf("label lost behind the edge: %q", shown)
	}
	// an arrow to a host scrolled away keeps its head at the edge
	if shown := strings.Join(laneCells(3, 2, 0, 10, "200 OK", 10, 30)[1:], ""); !strings.HasPrefix(shown, "◀") || !strings.Contains(shown, "200 OK") {
		t.Fatalf("no head at the edge: %q", shown)
	}
	if shown := strings.Join(laneCells(3, 0, 2, 10, "INVITE", 0, 20)[:2], ""); !strings.HasSuffix(shown, "▶") {
		t.Fatalf("no head at the right edge: %q", shown)
	}
}

// TestScreenshots renders the main screens to text files: HOMER_TUI_SCREENSHOTS=dir go test -run Screenshots ./internal/ui
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("HOMER_TUI_SCREENSHOTS")
	if dir == "" {
		t.Skip("set HOMER_TUI_SCREENSHOTS=<dir>")
	}
	h := start(t, "")
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(150, 42) })
	shot := func(name string, wait ...string) {
		txt := h.waitFor(wait...)
		var lines []string
		for _, l := range strings.Split(txt, "\n") {
			lines = append(lines, strings.TrimRight(l, " "))
		}
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shot("1-calls", "SIP call(1_call)", "200 OK")
	h.keys("v")
	shot("1-messages", "METHOD", "INVITE")
	h.keys("v")
	h.filterAnswered()
	h.key(tcell.KeyEnter)
	shot("2-flow", "1 Flow", "▶", "Call-ID:")
	h.keys("2")
	shot("3-messages", "DESTINATION", "SIZE")
	h.keys("3")
	h.waitFor("QoS (RTCP / RTP)")
	h.waitNot("⟳")
	shot("4-qos", "QoS (RTCP / RTP)")
	h.key(tcell.KeyEsc)
	h.key(tcell.KeyEsc)
	h.keys("s")
	shot("5-form", "Profile", "SIP From user")
	h.key(tcell.KeyEsc)
	h.command("aliases")
	shot("6-aliases", "Aliases", "SBC")
	h.command("mappings")
	h.keys("f")
	shot("7-fields", "Searchable fields")
}

// The host list works as in rtpengine-tui and opensips-tui: several hosts
// start on it, a host that does not let in or does not answer says why in
// STATUS, enter opens the calls over the list, space makes a host current,
// d opens its card, :<host> switches.
func TestHostsList(t *testing.T) {
	h := startHosts(t, "", func(url, token string) []config.Host {
		return []config.Host{
			{Name: "lab", URL: url, Username: "admin", Password: "sipcapture", Labels: config.Labels{"env": "lab"}},
			{Name: "wrong", URL: url, Username: "admin", Password: "nope"},
			{Name: "down", URL: "http://127.0.0.1:1", Token: "t"},
			{Name: "ci", URL: url, Token: token, Insecure: true},
		}
	})
	h.waitFor("Hosts", "ADDRESS", "AUTH", "USER", "STATUS", "NODES", "LABELS")
	h.waitRe(`\*\s+lab\s+http://127\.0\.0\.1:\d+\s+password\s+admin \(admin\)\s+OK\s+localnode`,
		`wrong\s+\S+\s+password\s+admin\s+.*User couldn't be found`, `down\s+http://127\.0\.0\.1:1\s+token\s+Get .*dial tcp`,
		`ci\s+\S+\s+token · TLS not verified`)
	h.keys("j")
	h.keys(" ")
	h.waitRe(`current host: wrong`, `Host:\s+wrong`)
	h.keys("k")
	h.key(tcell.KeyEnter)
	h.waitFor("SIP call(1_call)", "200 OK")
	h.key(tcell.KeyEsc)
	h.waitFor("Hosts", "ADDRESS")
	h.keys("d")
	h.waitFor("Database nodes", "Search profiles", "env=lab")
	h.key(tcell.KeyEsc)
	h.command("down")
	h.waitRe(`Host:\s+down`, `down: Get .*dial tcp`)
}

// :lang switches the open view, the header and the views opened after.
func TestLanguageSwitch(t *testing.T) {
	h := start(t, "")
	t.Cleanup(func() { _ = i18n.Set("en") })
	h.waitFor("SIP call(1_call)", "200 OK", "Host:")
	h.command("lang ru")
	h.waitFor("Хост:", "Пользователь:", "Узлы:", "Вход:", "НАЧАЛО", "СТАТУС", "открыть звонок")
	h.key(tcell.KeyEnter)
	h.waitFor("Лесенка", "Сообщения", "Логи", "сообщение целиком")
	h.key(tcell.KeyEsc)
	h.command("hosts")
	h.waitFor("Хосты", "АДРЕС", "ВХОД", "ПОЛЬЗОВАТЕЛЬ", "УЗЛЫ", "МЕТКИ")
	h.command("info")
	h.waitFor("Узлы БД", "Модули", "Типы авторизации", "Профили поиска")
	h.command("lang en")
	h.waitFor("Database nodes", "Modules")
}

// d opens a card, y the JSON that y closes again: in search results (a
// session or a message), in settings, in the call view.
func TestCardsAndJSON(t *testing.T) {
	h := start(t, "")
	h.waitFor("SIP call(1_call)", "200 OK")
	h.keys("d")
	h.waitFor("Session", "status", "Messages", "INVITE", "<y> JSON/parsed")
	h.keys("y")
	h.waitFor(`"method": "INVITE"`)
	h.keys("y")
	h.waitFor("Session", "Messages")
	h.key(tcell.KeyEsc)
	h.waitFor("SIP call(1_call)")
	h.keys("y")
	h.waitFor(`"method"`, "<y> back")
	h.keys("y")
	h.waitFor("SIP call(1_call)", "START")
	// one message of the raw view
	h.keys("v")
	h.waitFor("view: messages", "METHOD")
	h.keys("d")
	h.waitRe(`Message`, `method\s+[A-Z0-9]+`, `source\s+\S+`, `destination\s+\S+`, `Call-ID\s+\S+@`)
	h.key(tcell.KeyEsc)
	// a settings item
	h.command("aliases")
	// SBC and PBX are in the search behind it too: wait for the loaded table
	h.waitFor("CAPTURE ID", "192.168.10.1")
	h.keys("d")
	h.waitRe(`alias\s+SBC`, `ip\s+192\.168\.10\.1`)
	h.key(tcell.KeyEsc)
	h.keys("y")
	h.waitFor(`"alias": "SBC"`, "<y> back")
	h.keys("y")
	h.waitFor("Aliases", "PBX")
	// the call view
	h.command("calls")
	h.waitFor("200 OK")
	h.key(tcell.KeyEnter)
	h.waitFor("Flow", "Messages")
	h.keys("y")
	h.waitFor(`"raw"`, "<y> back")
	h.keys("y")
	h.waitFor("Flow", "Messages")
}

func TestEpochTime(t *testing.T) {
	ms := time.Date(2026, 10, 21, 5, 44, 16, 615_000_000, time.UTC)
	us := ms.Add(123 * time.Microsecond)
	if got := epochTime(ms.UnixMilli()); !got.Equal(ms) {
		t.Errorf("milliseconds: %v", got)
	}
	if got := epochTime(us.UnixMicro()); !got.Equal(us) {
		t.Errorf("microseconds: %v", got)
	}
}

// Hosts that do not fit scroll by one beside TIME and DELTA.
func TestFlowScrollsHosts(t *testing.T) {
	h := start(t, "")
	h.waitFor("SIP call(1_call)", "200 OK")
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(80, 40) })
	h.filterAnswered()
	h.key(tcell.KeyEnter)
	txt := h.waitRe(`hosts 1-\d+ of \d+ ▶`)
	m := regexp.MustCompile(`hosts 1-(\d+) of (\d+)`).FindStringSubmatch(txt)
	shown, n := m[1], m[2]
	h.keys("l")
	h.waitRe(`◀ hosts 2-\d+ of ` + n)
	h.keys("$")
	h.waitRe(`◀ hosts \d+-` + n + ` of ` + n + `  `)
	h.keys("0")
	h.waitFor("hosts 1-" + shown + " of " + n + " ▶")
	h.waitRe(`TIME\s+DELTA`)
}

// f hides kinds of messages in the flow and the messages tab, a puts the
// addresses of an alias in one lane; both stay for the next call.
func TestFlowFilterAndAliases(t *testing.T) {
	h := start(t, "")
	h.waitFor("SIP call(1_call)", "200 OK")
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(160, 46) })
	h.filterAnswered()
	h.key(tcell.KeyEnter)
	h.waitFor("1 Flow", "──RTCP──", "<f> shown: all", "<a> hosts: by address", "<u> unfold: on")
	h.waitRe(`DELTA\s+TX`, `┌\s+│─+INVITE`, `└\s+│◀─+200 OK`)
	// v shows the message in plain words and back
	h.keys("v")
	h.waitRe(`Parties`, `request URI\s+\S+@example\.com`, `Via\s+UDP: `, `<v> message: readable`)
	// m goes into the message pane, esc back to the list, not out of the call
	h.keys("m")
	h.waitFor("m or esc: back to the list", "<m/esc> back to the list")
	h.keys("G")
	h.key(tcell.KeyEsc)
	h.waitFor("1 Flow", "<m> into the message")
	// enter opens the message in the same view, v toggles there too
	h.key(tcell.KeyEnter)
	h.waitFor("message #1 INVITE", "Parties", "<v> readable/as is")
	h.keys("v")
	h.waitFor("Via: SIP/2.0/UDP")
	h.key(tcell.KeyEsc)
	h.waitFor("1 Flow", "<v> message: as is")
	h.keys("v")
	h.waitFor("Parties", "<v> message: readable")
	h.keys("v")
	h.waitFor("INVITE sip:", "<v> message: as is")
	h.keys("f")
	h.waitRe(`✓\s+SIP\s+\d+`, `✓\s+RTCP\s+\d+`, `·\s+hosts by alias\s+\d+ hosts → \d+`)
	h.keys("j ")
	h.waitRe(`·\s+RTCP`)
	h.key(tcell.KeyEsc)
	txt := h.waitRe(`flow\[\d+/\d+\] hidden: RTCP`, `<f> shown: SIP, LOG`)
	if strings.Contains(txt, "──RTCP──") {
		t.Fatal("RTCP still in the flow")
	}
	h.keys("2")
	txt = h.waitFor("DESTINATION", "INVITE")
	if regexp.MustCompile(`\d\d:\d\d:\d\d\.\d{6}\s+\+\S+\s+RTCP`).MatchString(txt) {
		t.Fatal("RTCP still in the messages")
	}
	h.keys("1a")
	h.waitFor("hidden: RTCP · by alias", "addresses", "<a> hosts: by alias")
	// the next call opens the same way
	h.key(tcell.KeyEsc)
	h.waitFor("SIP call(1_call)")
	h.key(tcell.KeyEnter)
	h.waitFor("1 Flow", "hidden: RTCP · by alias")
	h.keys("a")
	txt = h.waitRe(`flow\[\d+/\d+\] hidden: RTCP `)
	if strings.Contains(txt, "hidden: RTCP · by alias") || strings.Contains(txt, "addresses") {
		t.Fatal("a did not switch back to addresses")
	}
}

// A forwarded call comes back from the PBX through the SBC: unfolded, the
// SBC is drawn again right of the PBX and every INVITE goes right; u
// draws each host once.
func TestFlowUnfold(t *testing.T) {
	h := start(t, "calls since=4h callid=fwd-%")
	h.waitFor("fwd-")
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(170, 50) })
	h.key(tcell.KeyEnter)
	left := regexp.MustCompile(`◀─+INVITE`)
	txt := h.waitRe(`flow\[\d+\] unfolded`, `SBC\s+PBX\s+SBC\s+203\.0\.113\.10:5060`)
	if left.MatchString(txt) {
		t.Fatalf("an INVITE goes left when unfolded:\n%s", txt)
	}
	h.keys("u")
	txt = h.waitFor("each host once")
	if strings.Contains(txt, "unfolded") || !left.MatchString(txt) {
		t.Fatalf("u did not fold the flow:\n%s", txt)
	}
	h.keys("f")
	// the call has no RTCP and no logs, the filter still lists them
	h.waitRe(`·\s+unfold calls that come back\s+4 hosts → 5`, `✓\s+RTCP\s+0`, `✓\s+LOG\s+0`)
}

// A B2BUA with an outside and an inside address under one alias: the call
// comes in outside, goes on inside to a PBX, the PBX calls back inside and
// the B2BUA calls out outside. Unfolded, both of its addresses are drawn
// again, so every INVITE goes right.
func TestLayoutTwoInterfaces(t *testing.T) {
	const (
		carrier = "198.51.100.7:5060"
		gwOut   = "203.0.113.6:5060"
		gwIn    = "192.168.20.6:5060"
		pbx     = "192.168.21.51:5060"
	)
	alias := map[string]string{carrier: "CARRIER", gwOut: "GW", gwIn: "GW", pbx: "PBX"}
	tx := &homer.Transaction{}
	add := func(sid, src, dst, method string) {
		tx.Data.Calldata = append(tx.Data.Calldata, homer.CallElement{Sid: sid, SrcID: src, DstID: dst,
			AliasSrc: alias[src], AliasDst: alias[dst], Method: method})
		tx.Data.Messages = append(tx.Data.Messages, map[string]any{"payloadType": float64(1)})
	}
	add("in", carrier, gwOut, "INVITE")
	add("in", gwOut, carrier, "100")
	add("in-b", gwIn, pbx, "INVITE")
	add("out-b", pbx, gwIn, "INVITE")
	add("out", gwOut, carrier, "INVITE")
	add("out", carrier, gwOut, "200")
	add("in", gwOut, carrier, "200")
	v := &CallView{tx: tx}
	for i := range tx.Data.Calldata {
		v.vis = append(v.vis, i)
	}
	hosts, arrows := v.layout(false, true)
	var lanes []string
	for _, h := range hosts {
		lanes = append(lanes, h.label+" "+h.sub)
	}
	want := []string{"CARRIER " + carrier, "GW " + gwOut, "GW " + gwIn, "PBX " + pbx, "GW " + gwIn, "GW " + gwOut, "CARRIER " + carrier}
	if strings.Join(lanes, " | ") != strings.Join(want, " | ") {
		t.Fatalf("lanes\n got %v\nwant %v", lanes, want)
	}
	for j, a := range arrows {
		if tx.Data.Calldata[j].Method == "INVITE" && a[0] >= a[1] {
			t.Errorf("INVITE %d goes left: %v", j, a)
		}
	}
	if arrows[6] != [2]int{1, 0} || arrows[5] != [2]int{6, 5} {
		t.Errorf("answers off their legs: %v", arrows)
	}
	// folded, each address once
	if hosts, _ := v.layout(false, false); len(hosts) != 4 {
		t.Errorf("folded: %d lanes", len(hosts))
	}
}

// An answer captured a microsecond before its request by another agent
// does not turn the leg around: the INVITE still goes right.
func TestLayoutEarlyAnswer(t *testing.T) {
	tx := &homer.Transaction{}
	for _, m := range [][3]string{{"b:5060", "a:5060", "100"}, {"a:5060", "b:5060", "INVITE"}, {"b:5060", "a:5060", "200"}} {
		tx.Data.Calldata = append(tx.Data.Calldata, homer.CallElement{Sid: "s", SrcID: m[0], DstID: m[1], Method: m[2]})
		tx.Data.Messages = append(tx.Data.Messages, map[string]any{"payloadType": float64(1)})
	}
	v := &CallView{tx: tx, vis: []int{0, 1, 2}}
	hosts, arrows := v.layout(false, true)
	if len(hosts) != 2 || hosts[0].label != "a:5060" || arrows[1] != [2]int{0, 1} || arrows[0] != [2]int{1, 0} {
		t.Fatalf("hosts %v arrows %v", hosts, arrows)
	}
}

// flow: in the config sets how calls open.
func TestFlowFromConfig(t *testing.T) {
	no := false
	lab := func(url, _ string) []config.Host {
		return []config.Host{{Name: "test", URL: url, Username: "admin", Password: "sipcapture"}}
	}
	flow := func(c *config.Config) {
		c.Flow = config.Flow{Hide: []string{"RTCP", "LOG"}, ByAlias: true, Unfold: &no, QoSGraphs: true}
	}
	h := startHosts(t, "", lab, flow)
	h.waitFor("SIP call(1_call)", "200 OK")
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(160, 46) })
	h.filterAnswered()
	h.key(tcell.KeyEnter)
	h.waitRe(`flow\[\d+/\d+\] hidden: RTCP, LOG · by alias `, `<f> shown: SIP `, `<u> unfold: off`)
	h.keys("3")
	h.waitFor("QoS graphs", "<v> QoS: graphs")

	h = startHosts(t, "calls since=4h callid=fwd-%", lab, flow)
	h.waitFor("fwd-")
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(170, 50) })
	h.key(tcell.KeyEnter)
	txt := h.waitRe(`flow\[\d+\] by alias `, `◀─+INVITE`)
	if strings.Contains(txt, "unfolded") {
		t.Fatal("unfold: false did not fold")
	}
}

// Transactions are brackets; a PRACK inside an INVITE takes a column of
// its own, an ACK to a failed INVITE closes the INVITE's bracket.
func TestTransactions(t *testing.T) {
	tx := &homer.Transaction{}
	add := func(sid, src, dst, method, cseq string) {
		tx.Data.Calldata = append(tx.Data.Calldata, homer.CallElement{Sid: sid, SrcID: src, DstID: dst, Method: method})
		tx.Data.Messages = append(tx.Data.Messages, map[string]any{"payloadType": float64(1), "cseq": cseq})
	}
	add("a", "x", "y", "INVITE", "1 INVITE")
	add("a", "y", "x", "100", "1 INVITE")
	add("a", "y", "x", "183", "1 INVITE")
	add("a", "x", "y", "PRACK", "2 PRACK")
	add("a", "y", "x", "200", "2 PRACK")
	add("a", "y", "x", "200", "1 INVITE")
	add("a", "x", "y", "ACK", "1 ACK")
	tx.Data.Calldata = append(tx.Data.Calldata, homer.CallElement{Sid: "a", SrcID: "x", DstID: "y", Method: "RTCP"})
	tx.Data.Messages = append(tx.Data.Messages, map[string]any{"payloadType": float64(5)})
	add("b", "x", "y", "INVITE", "1 INVITE")
	add("b", "y", "x", "486", "1 INVITE")
	add("b", "x", "y", "ACK", "1 ACK")
	v := &CallView{tx: tx}
	for i := range tx.Data.Calldata {
		v.vis = append(v.vis, i)
	}
	cells, width := txGraph(v.txKeys())
	tags := regexp.MustCompile(`\[[^\]]*\]`)
	var got []string
	for _, c := range cells {
		got = append(got, fmt.Sprintf("%-2s", tags.ReplaceAllString(c, "")))
	}
	want := []string{"┌ ", "├ ", "├ ", "│┌", "│└", "└ ", "╶ ", "  ", "┌ ", "├ ", "└ "}
	if width != 2 || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("width %d\n got %q\nwant %q", width, got, want)
	}
}

// ctrl-d and ctrl-u scroll a text view of the call by half its height.
func TestHalfPage(t *testing.T) {
	tv := tview.NewTextView()
	tv.SetText(strings.Repeat("line\n", 100))
	tv.SetRect(0, 0, 40, 20)
	if !halfPage(tv, tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl)) {
		t.Fatal("ctrl-d not taken")
	}
	if row, _ := tv.GetScrollOffset(); row < 9 || row > 10 { // about half of 20 lines
		t.Fatalf("ctrl-d: row %d", row)
	}
	halfPage(tv, tcell.NewEventKey(tcell.KeyCtrlU, 0, tcell.ModCtrl))
	halfPage(tv, tcell.NewEventKey(tcell.KeyCtrlU, 0, tcell.ModCtrl))
	if row, _ := tv.GetScrollOffset(); row != 0 {
		t.Fatalf("ctrl-u: row %d", row)
	}
	if halfPage(tv, tcell.NewEventKey(tcell.KeyRune, 'j', 0)) {
		t.Fatal("j taken")
	}
}

// The status of a session is the answer to its request: a registration
// renews itself, so its last answer, and a 401 or 407 only when nothing
// else answered; a call's first, past the challenge.
func TestSessionStatus(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 13, 31, 10, 0, time.UTC)
	status := func(msgs ...string) string {
		g := &sessionGroup{sid: "s"}
		for i, m := range msgs {
			method, cseq, _ := strings.Cut(m, "/")
			ts := t0.Add(time.Duration(i) * time.Second)
			g.items = append(g.items, map[string]any{"method": method, "cseq": cseq, "timeSeconds": float64(ts.Unix())})
		}
		return strings.Fields(g.row(time.UTC).Cells[1])[0]
	}
	for _, c := range []struct {
		want string
		msgs []string
	}{
		{"200", []string{"REGISTER/1 REGISTER", "401/1 REGISTER", "REGISTER/2 REGISTER", "200/2 REGISTER", "REGISTER/3 REGISTER", "401/3 REGISTER"}},
		{"401", []string{"REGISTER/1 REGISTER", "401/1 REGISTER"}},
		{"403", []string{"REGISTER/1 REGISTER", "401/1 REGISTER", "REGISTER/2 REGISTER", "403/2 REGISTER"}},
		{"200", []string{"INVITE/1 INVITE", "407/1 INVITE", "ACK/1 ACK", "INVITE/2 INVITE", "100/2 INVITE", "200/2 INVITE", "ACK/2 ACK", "BYE/3 BYE", "200/3 BYE"}},
		{"486", []string{"INVITE/1 INVITE", "486/1 INVITE", "ACK/1 ACK"}},
		{"200", []string{"INVITE/1 INVITE", "200/1 INVITE", "INVITE/2 INVITE", "491/2 INVITE"}},
	} {
		if got := status(c.msgs...); got != c.want {
			t.Errorf("%v: %s, want %s", c.msgs, got, c.want)
		}
	}
}

// F follows a call: an ended one stops at once and says so; one still
// talking stays live through reloads.
func TestFollowCall(t *testing.T) {
	h := start(t, "calls limit=2000")
	h.waitFor("SIP call(1_call)", "200 OK")
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(170, 50) })
	h.keys("/200 OK .*[0-9]+s .*[0-9a-f]{8}-[0-9a-f]{4}@") // answered, hung up
	h.key(tcell.KeyEnter)
	h.waitFor("</200 OK")
	h.key(tcell.KeyEnter)
	h.waitFor("1 Flow", "<F> follow: off", "messages, ") // loaded
	h.keys("F")
	h.waitFor("the call ended, following stopped", "<F> follow: off")
	h.key(tcell.KeyEsc)
	h.waitFor("SIP call(1_call)")
	h.key(tcell.KeyEsc) // the filter
	h.keys("/live-0001")
	h.key(tcell.KeyEnter)
	h.waitFor("</live-0001>")
	h.key(tcell.KeyEnter)
	h.waitFor("1 Flow", "──ACK──")
	h.keys("F")
	h.waitFor("● live", "<F> follow: on")
	h.keys("r")
	time.Sleep(300 * time.Millisecond)
	if txt := h.waitFor("1 Flow"); !strings.Contains(txt, "● live") {
		t.Fatalf("a call still talking stopped being followed:\n%s", txt)
	}
}

// :mappings says what each mapping is and what a call is built with; the
// card explains it, and names the mistake of the default 100_default.
func TestMappingsExplained(t *testing.T) {
	h := start(t, "mappings")
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(170, 50) })
	h.waitRe(`KEY\s+WHAT\s+FIELDS\s+FLOW\s+SEARCHED\s+ISSUES`, `1_call\s+SIP · call\s+\d+\s+LOG, RTCP, LEG\s+yes`,
		`100_default\s+LOG · default\s+\d+\s+SIP\s+yes\s+3`)
	h.keys("/1_call")
	h.key(tcell.KeyEnter)
	h.keys("d")
	h.waitRe(`1_call · SIP · call`, `payload\s+SIP\s+║`, `profile\s+call\s+║`, `table\s+hep_proto_1_call`, `searched\s+yes\s+║`,
		`Search fields`, `from_user\s+data_header\.from_user\s+║`, `(?m)^║\s+sid\s+║`)
	h.keys("G") // the flow is further down
	h.waitRe(`  SIP   the call's own messages, found by the search\s+1_call`, `\+ LOG   logs with this Call-ID\s+100_default`,
		`\+ RTCP  RTCP reports with this Call-ID\s+5_default`, `\+ LEG   the leg whose Call-ID is this call's correlation_id\s+1_call`,
		`\+ LEG   the leg whose Call-ID is this one's plus "_b2b-1"\s+1_call`, `each rule looks 5m before, 3m20s after the search window`, `Stored only`)
	h.key(tcell.KeyEsc)
	h.key(tcell.KeyEsc) // the filter
	h.keys("/100_default")
	h.key(tcell.KeyEnter)
	h.keys("d")
	h.waitRe(`Problems`, `rule 1: lookup_field data_header\.callid is not SQL, the lookup finds nothing; write data_header->>'callid'`)
	// its builder writes the rules otherwise, yet esc leaves without a question:
	// nothing was changed there
	h.key(tcell.KeyEsc)
	h.keys("b")
	h.waitRe(`saved; from the blocks the rules come out otherwise, 3 → 3, s writes them so`)
	h.key(tcell.KeyEsc)
	if txt := h.waitRe(`100_default\s+LOG · default`); strings.Contains(txt, "Leave without saving") {
		t.Fatal("esc asked to leave blocks not changed")
	}
	h.keys("d")
	h.keys("G")
	h.waitRe(`\+ SIP  SIP messages with this Call-ID\s+1_call\s+✗ finds nothing: not SQL`)
}

// b on a mapping builds its call assembly from blocks: the default 1_call
// reads as four blocks; RTCP off and saved, homer-app gets three rules.
func TestCorrelationBuilder(t *testing.T) {
	h := start(t, "mappings")
	h.app.QueueUpdateDraw(func() { h.screen.SetSize(170, 50) })
	h.waitFor("1_call")
	h.keys("/1_call")
	h.key(tcell.KeyEnter)
	h.keys("b")
	h.waitRe(`ON\s+BLOCK\s+PROFILE\s+MATCH\s+LOOKUP\s+WINDOW`, `✓\s+LOG\s+default\s+Call-ID\s+sid\s+5m before, 3m20s after`,
		`✓\s+RTCP\s+default\s+Call-ID\s+sid`, `✓\s+LEG\s+call\s+correlation_id\s+Call-ID`,
		`✓\s+LEG\s+call\s+Call-ID\+"_b2b-1"\s+Call-ID`, `how a call will be assembled`, `HEP logs whose session id`, `saved`)
	// shift-d asks; cancelled, the block stays
	h.keys("jjj")
	h.keys("D")
	h.waitFor("Delete the block LEG") // the modal wraps the rest
	h.key(tcell.KeyEnter)             // Cancel is first
	h.waitRe(`✓\s+LEG\s+call\s+Call-ID\+"_b2b-1"`, `\bsaved\b`)
	// a opens the blocks one under another; enter adds one and sets it up
	h.keys("a")
	h.waitRe(`BLOCK\s+MATCH\s+LOOKUP\s+║`, `(?m)^\S\s+LOG\s+Call-ID\s+sid\s+║`, `(?m)^\S\s+RTP\s+Call-ID\s+sid\s+║`)
	h.waitRe(`─ LOG Call-ID → sid ─`) // the whole help of the block selected, under the list
	h.keys("jjjj")
	h.waitRe(`─ LEG correlation_id → correlation_id · append_sid ─`, `every other block runs for them`,
		`LEG\s+correlation_id\s+correlation_id · append_sid\s+║`)
	h.keys("kk")
	h.key(tcell.KeyEnter)
	h.waitFor("RTP Call-ID → sid · default · ", "seconds before the search window")
	h.key(tcell.KeyEsc) // back to the list of blocks, nothing added
	h.waitFor("Add a block to the call assembly")
	h.key(tcell.KeyEsc)
	if txt := h.waitRe(`│ saved\s`); regexp.MustCompile(`✓\s+RTP\s`).MatchString(txt) {
		t.Fatalf("esc in the setup added the block:\n%s", txt)
	}
	h.keys("a")
	h.waitFor("Add a block to the call assembly")
	h.keys("jj")
	h.key(tcell.KeyEnter)
	h.waitFor("RTP Call-ID → sid · default · ", "match by")
	for i := 0; i < 3; i++ { // to Done
		h.key(tcell.KeyTab)
	}
	h.key(tcell.KeyEnter) // done: back in the builder, the list closed too
	if txt := h.waitRe(`✓\s+RTP\s+default\s+Call-ID\s+sid`, `not saved: 4 rules → 5`); strings.Contains(txt, "Add a block to the call assembly") {
		t.Fatal("the list of blocks stayed open")
	}
	h.keys("e") // e sets a block up too
	h.waitFor("RTP Call-ID → sid · default · ", "match by")
	h.key(tcell.KeyEsc)
	h.waitRe(`<enter/e> set up`)
	h.keys("G")
	h.keys("D")
	h.waitFor("Delete the block RTP Call-ID → sid · default?")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	h.waitRe(`│ saved\s`) // back to what homer-app has
	h.keys("gj ")         // RTCP off
	h.waitRe(`·\s+RTCP\s+default`, `not saved: 4 rules → 3, s saves`)
	h.keys("s")
	h.waitFor("Save the call assembly of 1_call: 4 rules → 3?")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyEnter)
	h.waitFor("successfully")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rules, _ := h.srv.Item("/mapping/protocol", "m-1")["correlation_mapping"].([]any); len(rules) == 3 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("homer-app has %v", h.srv.Item("/mapping/protocol", "m-1")["correlation_mapping"])
}

// A PBX that calls two parties and bridges them: its first call goes to
// the right, the second to the left, and what follows the second goes on
// to the left; the PBX stands between its two calls.
func TestLayoutBridge(t *testing.T) {
	alias := map[string]string{"192.168.20.24:5060": "PBX", "192.168.20.6:5060": "GW", "198.51.100.154:5060": "CARRIER", "192.168.20.25:5060": "WEBRTC", "192.168.22.160:35874": "user"}
	tx := &homer.Transaction{}
	add := func(sid, src, dst, method string) {
		tx.Data.Calldata = append(tx.Data.Calldata, homer.CallElement{Sid: sid, SrcID: src, DstID: dst,
			AliasSrc: alias[src], AliasDst: alias[dst], Method: method})
		tx.Data.Messages = append(tx.Data.Messages, map[string]any{"payloadType": float64(1)})
	}
	const pbx, gw, carrier, webrtc, user = "192.168.20.24:5060", "192.168.20.6:5060", "198.51.100.154:5060", "192.168.20.25:5060", "192.168.22.160:35874"
	add("a", pbx, gw, "INVITE")
	add("a", gw, pbx, "100")
	add("a2", gw, carrier, "INVITE")
	add("a2", carrier, gw, "200")
	add("a", gw, pbx, "200")
	add("b", pbx, webrtc, "INVITE")
	add("c", webrtc, user, "INVITE")
	add("c", user, webrtc, "200")
	add("b", webrtc, pbx, "200")
	v := &CallView{tx: tx}
	for i := range tx.Data.Calldata {
		v.vis = append(v.vis, i)
	}
	hosts, arrows := v.layout(true, true)
	var names []string
	for _, h := range hosts {
		names = append(names, h.label)
	}
	if strings.Join(names, " ") != "user WEBRTC PBX GW CARRIER" {
		t.Fatalf("lanes %v", names)
	}
	// the first call goes right, the second left
	if arrows[0] != [2]int{2, 3} || arrows[2] != [2]int{3, 4} || arrows[5] != [2]int{2, 1} || arrows[6] != [2]int{1, 0} || arrows[8] != [2]int{1, 2} {
		t.Fatalf("arrows %v", arrows)
	}
	// folded, each host once, in the order they came
	if hosts, _ := v.layout(true, false); len(hosts) != 5 || hosts[0].label != "PBX" {
		t.Fatalf("folded %v", hosts)
	}

	// by address the WebRTC gateway has two: SIP to the PBX, WSS to the
	// user; its second address stands to the left with the first
	for i := range tx.Data.Calldata {
		if c := &tx.Data.Calldata[i]; c.Sid == "c" {
			if c.SrcID == webrtc {
				c.SrcID = "192.168.20.25:8443"
			} else {
				c.DstID = "192.168.20.25:8443"
			}
			c.AliasSrc, c.AliasDst = alias[c.SrcID], alias[c.DstID]
			if c.SrcID == "192.168.20.25:8443" {
				c.AliasSrc = "WEBRTC"
			} else {
				c.AliasDst = "WEBRTC"
			}
		}
	}
	hosts, arrows = v.layout(false, true)
	var addrs []string
	for _, h := range hosts {
		addrs = append(addrs, h.key)
	}
	if strings.Join(addrs, " ") != user+" 192.168.20.25:8443 "+webrtc+" "+pbx+" "+gw+" "+carrier {
		t.Fatalf("by address %v", addrs)
	}
	if arrows[6] != [2]int{1, 0} {
		t.Fatalf("the user's INVITE goes left: %v", arrows[6])
	}
}

// enter on a log opens a call the way homer-app assembles it, by the
// mapping of the log profile.
func TestLogOpensByItsMapping(t *testing.T) {
	h := start(t, "logs since=4h")
	h.waitFor("pbx: call")
	h.key(tcell.KeyEnter)
	h.waitRe(`1 Flow`, `──LOG──|◀─+LOG`)
	if strings.Contains(h.waitFor("1 Flow"), "Flow · ") {
		t.Fatal("not a call view")
	}
}

func TestBrokenOff(t *testing.T) {
	for _, s := range []string{"Post \"x\": stream error: stream ID 15; INTERNAL_ERROR; received from peer", "Post \"x\": unexpected EOF", "Post \"x\": EOF"} {
		if !brokenOff(errors.New(s)) {
			t.Errorf("%q", s)
		}
	}
	if brokenOff(errors.New("homer: HTTP 400: invalid search profile key")) {
		t.Error("a 400 is an answer")
	}
}

// The window is read from its end back as the table scrolls: the rows
// fill the screen, G at the end brings the next part, until each session
// of the window is in the table once; the latest first.
func TestSearchPages(t *testing.T) {
	h := start(t, "calls limit=40")
	txt := h.waitRe(`more below`, `START↓`)
	var times []string // the latest first, the latest of the window at the top
	for _, m := range regexp.MustCompile(`(?m)^║ (\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{3})`).FindAllStringSubmatch(txt, -1) {
		times = append(times, m[1])
	}
	if len(times) < 2 || !sort.IsSorted(sort.Reverse(sort.StringSlice(times))) {
		t.Fatalf("rows not the latest first: %v\n%s", times, txt)
	}
	rows := func() []Row {
		got := make(chan []Row, 1)
		h.app.QueueUpdate(func() { got <- h.app.TopTable().Rows() })
		return <-got
	}
	for deadline := time.Now().Add(5 * time.Second); len(rows()) < 40 && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(rows()); n < 40 { // the parts are read on until the rows fill the screen
		t.Fatalf("%d rows do not fill the screen", n)
	}
	for i := 0; i < 50 && strings.Contains(h.text(), "more below"); i++ {
		h.keys("G")
		time.Sleep(150 * time.Millisecond)
	}
	if strings.Contains(h.text(), "more below") {
		t.Fatal("G did not read the window to its start")
	}
	now := time.Now()
	all, err := h.app.client.Search(context.Background(), homer.Query{Key: "1_call", HepID: 1, Nodes: []string{"localnode"}, From: now.Add(-4 * time.Hour), To: now, Limit: 100000})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, m := range all.Data {
		want[str(m, "sid")] = true
	}
	seen := map[string]bool{}
	for _, r := range rows() {
		if seen[r.Key] {
			t.Fatalf("%s twice", r.Key)
		}
		seen[r.Key] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("%d sessions of %d", len(seen), len(want))
	}
	h.keys("go[")                 // a sort of one's own stays through a shift of the window
	h.waitRe(`STATUS↓`, `0 rows`) // 4h before the emulator's 3h
	h.keys("]")
	h.waitRe(`\d+ rows`, `STATUS↓`, `more below`)

	for _, key := range []string{"1_call", "1_registration", "100_default", "5_default"} {
		t.Run(key, func(t *testing.T) { checkPages(t, h.app.client, key, 7) })
	}
	t.Run("1_call by 60", func(t *testing.T) { checkPages(t, h.app.client, "1_call", 60) })
}

// checkPages walks the pages of a profile a few messages at a time.
func checkPages(t *testing.T, c *homer.Client, key string, limit int) {
	hepid := 1
	fmt.Sscanf(key, "%d_", &hepid)
	ctx := context.Background()
	now := time.Now()
	q := homer.Query{Key: key, HepID: hepid, Nodes: []string{"localnode"}, From: now.Add(-4 * time.Hour), To: now, Limit: 100000}
	all, err := c.Search(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	session := func(m map[string]any) string { // a row with no session id is its own
		if sid := str(m, "sid"); sid != "" {
			return sid
		}
		return "row " + str(m, "dbnode") + "/" + str(m, "id")
	}
	want := map[string]int{}
	for _, m := range all.Data {
		want[session(m)]++
	}
	q.Limit = limit
	pq := pageQuery{Query: q}
	page := map[string]int{} // the page of a session
	var earliest time.Time   // the latest activity of the session least active of the page before
	var sessions []int       // on each page
	got := map[string]int{}
	cut := 0
	for n := 1; ; n++ {
		if n > len(all.Data) {
			t.Fatal("the pages do not end")
		}
		items, next, more, err := loadPage(ctx, c, pq)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) > q.Limit {
			cut++
		}
		for _, m := range items {
			sid := session(m)
			if p, ok := page[sid]; ok && p != n {
				t.Fatalf("%s on pages %d and %d", sid, p, n)
			}
			page[sid] = n
			got[sid]++
		}
		if !more {
			break
		}
		// the pages go back in time: a session is on the page of its latest
		// activity, so every session of this page was last active no later
		// than every session of the page before
		lasts := map[string]time.Time{}
		for _, m := range items {
			if s, t := session(m), itemTime(m); t.After(lasts[s]) {
				lasts[s] = t
			}
		}
		for _, s := range lasts {
			if !earliest.IsZero() && s.Truncate(time.Millisecond).After(earliest) {
				t.Fatalf("page %d has a session last active at %v, later than the page before, %v", n, s, earliest)
			}
		}
		sessions = append(sessions, len(lasts))
		var pageMin time.Time
		for _, s := range lasts {
			if s = s.Truncate(time.Millisecond); pageMin.IsZero() || s.Before(pageMin) {
				pageMin = s
			}
		}
		if !pageMin.IsZero() {
			earliest = pageMin
		}
		pq.pageStart = next
	}
	// the first page is no emptier than the rest: the calls going on at the
	// end of the window do not crowd its new ones out
	t.Logf("sessions by page: %v", sessions)
	if limit > 7 && len(sessions) > 2 && sessions[0]*3 < sessions[1] {
		t.Fatalf("sessions by page: %v", sessions)
	}
	if cut == 0 && key == "1_call" && limit == 7 {
		t.Fatal("no page had a session cut by the limit, the test shows nothing")
	}
	if len(got) != len(want) {
		t.Fatalf("pages showed %d sessions of %d", len(got), len(want))
	}
	for sid, n := range want {
		if got[sid] != n {
			t.Fatalf("%s: %d messages of %d", sid, got[sid], n)
		}
	}
}

// The search form shows the profile, the window and a few fields; ctrl-a
// adds one of the others, ctrl-d takes it out, enter in a field searches.
// The config gives a profile its own set.
func TestSearchForm(t *testing.T) {
	h := start(t, "calls")
	h.waitFor("SIP call(1_call)", "200 OK")
	h.keys("s")
	txt := h.waitRe(`Profile`, `Window\s+4h`, `SIP From user`, `SIP To user`, `CallID`, `RURI user`)
	for _, s := range []string{"Source IP", "User Agent", "all: localnode", "Where (smart)"} { // all: is the placeholder of Nodes
		if strings.Contains(txt, s) {
			t.Fatalf("%s is in the form:\n%s", s, txt)
		}
	}
	h.key(tcell.KeyCtrlA)
	h.waitFor("Add a field to the search form", "User Agent", "Limit")
	h.keys("/Source IP")
	h.key(tcell.KeyEnter)
	h.key(tcell.KeyEnter)
	h.waitFor("Source IP (protocol_header.srcIp)")
	h.keys("10.10.0.11")
	h.key(tcell.KeyEnter) // searches
	h.waitFor("protocol_header.srcIp=10.10.0.11")

	h.keys("s") // the set stays, with the value
	h.waitRe(`Source IP \(protocol_header\.srcIp\)\s+10\.10\.0\.11`)
	h.key(tcell.KeyTab)
	h.key(tcell.KeyTab)
	h.keys("101")
	h.waitRe(`SIP From user \(data_header\.from_user\)\s+101`)
	h.key(tcell.KeyCtrlC) // clears, the app stays
	h.waitRe(`SIP From user \(data_header\.from_user\)\s+║`, `Source IP \(protocol_header\.srcIp\)\s+║`)
	h.keys("10.10.0.11") // the focus stayed on From user
	h.key(tcell.KeyCtrlS)
	h.waitFor("data_header.from_user=10.10.0.11")
	h.keys("s")
	h.waitRe(`Source IP \(protocol_header\.srcIp\)`, `SIP From user \(data_header\.from_user\)\s+10\.10\.0\.11\s+║`)
	// the Clear button clears too, and the form goes on taking keys
	for i := 0; i < 8; i++ { // past the 7 fields to Search, then Clear
		h.key(tcell.KeyTab)
	}
	h.key(tcell.KeyEnter)
	h.waitRe(`SIP From user \(data_header\.from_user\)\s+║`)
	h.key(tcell.KeyBacktab)
	h.key(tcell.KeyBacktab) // Search, then the last field
	h.keys("192.168.10.1")
	h.waitRe(`Source IP \(protocol_header\.srcIp\)\s+192\.168\.10\.1\s+║`)
	h.key(tcell.KeyCtrlD) // the field and its value go
	h.waitNot("Source IP")
	h.key(tcell.KeyBacktab)
	h.key(tcell.KeyBacktab)
	h.key(tcell.KeyBacktab)
	h.key(tcell.KeyBacktab) // the window
	h.key(tcell.KeyCtrlU)
	h.keys("15m")
	h.key(tcell.KeyEnter)
	if txt := h.waitFor("last 15m"); strings.Contains(txt, "srcIp") {
		t.Fatal("the field taken out still filters")
	}

	h = startHosts(t, "calls", func(url, _ string) []config.Host {
		return []config.Host{{Name: "test", URL: url, Username: "admin", Password: "sipcapture"}}
	}, func(c *config.Config) { c.SearchFields = map[string][]string{"1_call": {"user_agent", "limit", "ua"}} })
	h.waitFor("SIP call(1_call)")
	h.keys("s")
	if txt := h.waitRe(`User Agent`, `Limit\s+\d+`, `unknown fields ignored: ua`); strings.Contains(txt, "SIP From user") {
		t.Fatalf("the config's set was not taken:\n%s", txt)
	}
}

// The status line of a call reads the leg of the caller: the answers it
// got through a proxy, the INVITE again after a 407, who ended the call
// and why.
func TestCallOutcome(t *testing.T) {
	const caller, proxy, pbx = "10.0.0.1:5060", "10.0.0.2:5060", "10.0.0.3:5060"
	type msg struct {
		src, dst, method, cseq, raw string
		at                          time.Duration
	}
	build := func(msgs ...msg) (*homer.Transaction, func(int) time.Time) {
		tx := &homer.Transaction{}
		t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
		var times []time.Time
		for _, m := range msgs {
			tx.Data.Calldata = append(tx.Data.Calldata, homer.CallElement{Sid: "a", SrcID: m.src, DstID: m.dst, Method: m.method})
			tx.Data.Messages = append(tx.Data.Messages, map[string]any{"payloadType": float64(1), "cseq": m.cseq, "raw": m.raw, "from_user": "101", "to_user": "202"})
			times = append(times, t0.Add(m.at))
		}
		return tx, func(i int) time.Time { return times[i] }
	}
	tx, at := build(
		msg{caller, proxy, "INVITE", "1 INVITE", "", 0},
		msg{proxy, caller, "407", "1 INVITE", "", 5 * time.Millisecond},
		msg{caller, proxy, "INVITE", "2 INVITE", "", 20 * time.Millisecond},
		msg{proxy, pbx, "INVITE", "2 INVITE", "", 25 * time.Millisecond},
		msg{pbx, proxy, "180", "2 INVITE", "", 500 * time.Millisecond},
		msg{proxy, caller, "180", "2 INVITE", "", 505 * time.Millisecond},
		msg{pbx, proxy, "200", "2 INVITE", "", 3 * time.Second},
		msg{proxy, caller, "200", "2 INVITE", "", 3*time.Second + 5*time.Millisecond},
		msg{pbx, proxy, "BYE", "1 BYE", "BYE sip:101@x SIP/2.0\r\nReason: Q.850;cause=16;text=\"Normal call clearing\"\r\n", 63 * time.Second},
		msg{proxy, caller, "BYE", "1 BYE", "", 63*time.Second + 5*time.Millisecond},
	)
	o := callOutcomeOf(tx, at)
	if o.status != "200" || o.endMethod != "BYE" || o.byCaller || !strings.Contains(o.reason, "Normal call clearing") {
		t.Fatalf("answered call: %+v", o)
	}
	plain := func(l string) string { return regexp.MustCompile(`\[[^\]]*\]`).ReplaceAllString(l, "") }
	if line := plain(o.line(false)); line != " 200 OK RNG  510ms  ASW  3.01s  DUR  1m0s  BYE  callee  RSN  Q.850 16: Normal call clearing  LEG  1  101 → 202" {
		t.Fatalf("answered call: %q", line)
	}
	if line := o.line(false); !strings.Contains(line, "[green::b]RNG[-::-] [white:green] 510ms ") {
		t.Fatalf("RNG is not green with its value white on green: %q", line)
	}

	// a CANCEL passed on in the same millisecond, the hop past the proxy first
	tx, at = build(
		msg{caller, proxy, "INVITE", "1 INVITE", "", 0},
		msg{pbx, proxy, "180", "1 INVITE", "", time.Second},
		msg{proxy, caller, "180", "1 INVITE", "", time.Second},
		msg{proxy, pbx, "CANCEL", "1 CANCEL", "", 5 * time.Second},
		msg{caller, proxy, "CANCEL", "1 CANCEL", "", 5 * time.Second},
		msg{proxy, caller, "487", "1 INVITE", "", 5 * time.Second},
	)
	if line := plain(callOutcomeOf(tx, at).line(false)); line != " 487 Cancelled RNG  1s  RBT  5s  CXL  caller  LEG  1  101 → 202" {
		t.Fatalf("cancelled call: %q", line)
	}
}

// The flow of a mapping card marks the rule with append_sid, and the rules
// after it run for the other legs too.
func TestFlowLinesAppendSID(t *testing.T) {
	rules := homer.CompileBlocks([]homer.Block{homer.NewBlock(homer.BlockLogs), homer.NewBlock(homer.BlockLegs)}, 1)
	txt := regexp.MustCompile(`\[[^\]]*\]`).ReplaceAllString(strings.Join(flowLines(rules, nil, nil, 1, "1_call"), "\n"), "")
	for _, re := range []string{`(?m)^ 1 \+ LEG\s+the other legs with the same correlation_id; the rules after it look for them too · append_sid\s+1_call`,
		`(?m)^ 3 \+ LOG\s+logs with this Call-ID, for the other legs too\s+100_default`} {
		if !regexp.MustCompile(re).MatchString(txt) {
			t.Fatalf("%s not in\n%s", re, txt)
		}
	}
}
