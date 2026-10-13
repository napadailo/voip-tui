package rtpengine_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/internal/rtpengine/config"
	"github.com/napadailo/voip-tui/internal/rtpengine/mock"
)

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{
		"Version: 12.5.1.3+0~mr12.5.1.3 git-mr12.5-bd5b5a1": "mr12.5.1.3",
		"mr6.5.9.12":            "mr6.5.9.12",
		"10.5.3.5-1":            "mr10.5.3.5",
		"ngcp-rtpengine 13.3.1": "mr13.3.1",
	} {
		v, err := rtpengine.ParseVersion(in)
		if err != nil || v.String() != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %s", in, v, err, want)
		}
	}
	if _, err := rtpengine.ParseVersion("bash: rtpengine: command not found"); err == nil {
		t.Error("expected an error for missing binary")
	}
}

func TestCapsForVersion(t *testing.T) {
	v, _ := rtpengine.ParseVersion("mr9.5.1")
	c := rtpengine.CapsForVersion(v)
	for cmd, want := range map[string]bool{
		"list sessions": true, "list interfaces": true, "list jsonstats": true, "debug": true,
		"call": false, "set redisdisable": false, "usage": false,
	} {
		if c.Has(cmd) != want {
			t.Errorf("mr9.5 Has(%q) = %v", cmd, !want)
		}
	}
	err := c.Check("call", v)
	if err == nil || !strings.Contains(err.Error(), "mr10.5") {
		t.Errorf("Check(call) = %v", err)
	}
}

func TestParseSessionsBothLayouts(t *testing.T) {
	old := "callid:                                         abc@host | deletionmark:  no | created:  1700000000 | proxy:10.0.0.5:5060 | redis_keyspace:1 | foreign:yes\n"
	neu := "ID:                                         abc@host | del:no | creat:  1700000000 | prx:10.0.0.5:5060 | redis: 1 | frgn:yes | rec:no\n"
	for _, in := range []string{old, neu} {
		s := rtpengine.ParseSessions(in)
		if len(s) != 1 || s[0].CallID != "abc@host" || !s[0].Foreign || s[0].Proxy != "10.0.0.5:5060" || s[0].Created.Unix() != 1700000000 || s[0].Keyspace != "1" {
			t.Errorf("ParseSessions(%q) = %+v", in, s)
		}
	}
	if s := rtpengine.ParseSessions("No sessions on this media relay.\n"); len(s) != 0 {
		t.Errorf("empty list parsed as %+v", s)
	}
}

func TestParseValue(t *testing.T) {
	for in, want := range map[string]string{
		"Maximum sessions configured on rtpengine: -1\n": "-1",
		"TIMEOUT=60\n": "60",
		"184\n":        "184",
		"Maximum open-files configured on rtpengine: 65536; cat /proc/4242/limits\n":    "65536",
		"Maximum load average configured on rtpengine: 1.50\n":                          "1.50",
		"Maximum open-files configured on rtpengine: infinite; cat /proc/4242/limits\n": "infinite",
	} {
		if got := rtpengine.ParseValue(in); got != want {
			t.Errorf("ParseValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRuntimeOptions(t *testing.T) {
	m := rtpengine.RuntimeOptions(rtpengine.ParseParams("kernel_table = 0\nmax_sessions = -1\ntimeout_us = 60000000\ndefault_tos = 184\n[max-cpu = 0.0]\n"))
	for k, want := range map[string]string{"table": "0", "max-sessions": "-1", "timeout": "60", "tos": "184", "max-cpu": "0.0"} {
		if m[k] != want {
			t.Errorf("runtime[%s] = %q, want %q (all: %v)", k, m[k], want, m)
		}
	}
}

func TestAnalyzeConf(t *testing.T) {
	cf, err := rtpengine.ParseConf("rtpengine.conf", mock.SampleConf(mock.ConfOpts{CLI: "127.0.0.1:9900"}))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := rtpengine.ParseVersion("mr9.5.6")
	runtime := map[string]string{"silent-timeout": "1800", "timeout": "60"}
	got := map[string]rtpengine.Setting{}
	for _, s := range rtpengine.Analyze(cf, rtpengine.DaemonRTPEngine, "", v, runtime) {
		if !s.Overridden {
			got[s.Section+"/"+s.Key] = s
		}
	}
	check := func(key string, st rtpengine.Status, note string) {
		t.Helper()
		s, ok := got[key]
		if !ok {
			t.Fatalf("%s missing", key)
		}
		if s.Status != st || !strings.Contains(s.Note, note) {
			t.Errorf("%s: status %v note %q, want %v %q", key, s.Status, s.Note, st, note)
		}
	}
	check("rtpengine/timeout", rtpengine.StatusOK, "")
	check("rtpengine/silent-timeout", rtpengine.StatusDiffers, "runtime")
	check("rtpengine/max-sesions", rtpengine.StatusUnknown, "no such option")
	check("rtpengine/io-uring", rtpengine.StatusNotInVersion, "added in mr12.5")
	check("rtpengine/mqtt-host", rtpengine.StatusNotInVersion, "added in mr10.5")
	check("rtpengine/listen-http", rtpengine.StatusOK, "")
	check("rtpengine/log-level-ice", rtpengine.StatusOK, "")
	check("interface-pub/address", rtpengine.StatusOtherSection, "interface")
	check("templates/WebRTC", rtpengine.StatusNotInVersion, "need mr13.2+")
	check("rtpengine-testing/table", rtpengine.StatusOtherSection, "not read")
	if listen, _ := cf.Get("rtpengine", "max-sessions"); listen != "-1" {
		t.Errorf("last max-sessions should win, got %q", listen)
	}

	newV, _ := rtpengine.ParseVersion("mr13.3.1")
	for _, s := range rtpengine.Analyze(cf, rtpengine.DaemonRTPEngine, "", newV, nil) {
		switch {
		case s.Key == "io-uring" && s.Status != rtpengine.StatusOK:
			t.Errorf("io-uring on mr13.3: %v %s", s.Status, s.Note)
		case s.Section == "templates" && s.Key == "WebRTC" && (s.Status != rtpengine.StatusOtherSection || s.Template == nil || len(s.Template.Items) != 7):
			t.Errorf("WebRTC template on mr13.3: %v %s %+v", s.Status, s.Note, s.Template)
		case s.Section == "templates" && s.Key == "offer" && (s.Status != rtpengine.StatusUnknown || !strings.Contains(s.Note, "strict-sorce") || !strings.Contains(s.Desc, "every offer")):
			t.Errorf("offer template on mr13.3: %v %q %q", s.Status, s.Note, s.Desc)
		}
	}
}

// TestClientAgainstMock runs the whole client path against every emulated release.
func TestClientAgainstMock(t *testing.T) {
	for _, ver := range []string{"mr6.5.9.12", "mr9.5.6.2", "mr11.5.1.30", "mr13.3.1.4"} {
		t.Run(ver, func(t *testing.T) {
			srv := mock.New(ver, 3)
			srv.Freeze = true
			l, err := srv.Listen("127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { l.Close() })
			dir := t.TempDir()
			conf, rec, err := mock.WriteConfs(dir, mock.ConfOpts{CLI: l.Addr().String()})
			if err != nil {
				t.Fatal(err)
			}
			cfg, _ := config.Load(filepath.Join(dir, "none.yaml"))
			cfg.Hosts = []config.Host{{Name: "m", Address: "local", Via: config.ViaDirect, ConfigFile: conf, RecordingConfigFile: rec, CtlBin: "rtpengine-ctl"}}
			c := rtpengine.New(cfg, &cfg.Hosts[0])
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			info, err := c.Probe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// no --version on this machine: the version is estimated from CLI commands
			want := map[string]string{"mr6.5.9.12": "≥mr6.5", "mr9.5.6.2": "≥mr9.5", "mr11.5.1.30": "≥mr11.5", "mr13.3.1.4": "≥mr13.2"}[ver]
			if info.Version.String() != want {
				t.Errorf("version %s, want %s (caps from %s: %v)", info.Version, want, info.Caps.Source, info.Caps.Commands())
			}
			if info.Conf == nil {
				t.Fatalf("config not read: %v", info.ConfErr)
			}

			n, err := c.NumSessions(ctx)
			if err != nil || n.Total == 0 || n.Total != n.Own+n.Foreign {
				t.Fatalf("numsessions %+v %v", n, err)
			}
			sess, err := c.Sessions(ctx, "all")
			if err != nil || len(sess) != n.Total {
				t.Fatalf("sessions: %d vs %d, %v", len(sess), n.Total, err)
			}
			detail, err := c.SessionInfo(ctx, sess[0].CallID)
			if err != nil || !strings.Contains(detail, "Media #1") {
				t.Fatalf("details: %v\n%s", err, detail)
			}
			if _, err := c.Terminate(ctx, sess[0].CallID); err != nil {
				t.Fatal(err)
			}
			if after, _ := c.Sessions(ctx, "all"); len(after) != len(sess)-1 {
				t.Errorf("terminate did not remove the call: %d → %d", len(sess), len(after))
			}

			tun := c.Tunables(ctx)
			byName := map[string]rtpengine.Tunable{}
			for _, x := range tun {
				byName[x.Name] = x
			}
			if byName["maxsessions"].Value != "2000" || byName["timeout"].Value != "60" || byName["maxopenfiles"].Value != "65536" {
				t.Errorf("tunables: %+v", tun)
			}
			_, hasDD := byName["deletedelay"]
			if hasDD != (ver != "mr6.5.9.12") {
				t.Errorf("deletedelay listed = %v on %s", hasDD, ver)
			}
			if _, err := c.Set(ctx, "timeout", "120"); err != nil || srv.Get("timeout") != "120" {
				t.Errorf("set timeout: %v, mock has %s", err, srv.Get("timeout"))
			}
			if _, err := c.Set(ctx, "maxsessions", "abc"); err == nil {
				t.Error("expected error for a non-numeric value")
			}

			_, err = c.Debug(ctx, sess[1].CallID, true)
			if ver == "mr6.5.9.12" {
				var ue *rtpengine.UnsupportedError
				if err == nil || !strings.Contains(err.Error(), "mr9.5") {
					t.Errorf("debug on mr6.5 should be refused, got %v (%T)", err, ue)
				}
			} else if err != nil {
				t.Errorf("debug: %v", err)
			}

			settings, errs := c.Settings(ctx)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			var differs, recording int
			for _, s := range settings {
				if s.Status == rtpengine.StatusDiffers {
					differs++
				}
				if s.Daemon == rtpengine.DaemonRecording {
					recording++
				}
			}
			if differs == 0 || recording == 0 {
				t.Errorf("settings: %d differ, %d recording entries", differs, recording)
			}
		})
	}
}

// fakeBin puts an executable script on PATH for the test.
func fakeBin(t *testing.T, name, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCtlTransportArgs(t *testing.T) {
	fakeBin(t, "rtpengine-ctl", `echo "args: $*"`)
	ctx := context.Background()
	out, err := (&rtpengine.CtlCLI{Shell: &rtpengine.Local{}, Bin: "rtpengine-ctl", Addr: "127.0.0.1:2224"}).Do(ctx, "list sessions all")
	if err != nil || strings.TrimSpace(out) != "args: -ip 127.0.0.1 -port 2224 list sessions all" {
		t.Fatalf("got %q, %v", out, err)
	}
	// without an address rtpengine-ctl picks listen-cli from rtpengine.conf itself
	out, _ = (&rtpengine.CtlCLI{Shell: &rtpengine.Local{}, Bin: "rtpengine-ctl"}).Do(ctx, "list totals")
	if strings.TrimSpace(out) != "args: list totals" {
		t.Fatalf("got %q", out)
	}
}

func TestProbeDetectsVersionAndListenCLI(t *testing.T) {
	fakeBin(t, "rtpengine", `echo "Version: 12.5.1.3+0~mr12.5.1.3 git-mr12.5-bd5b5a1"`)
	srv := mock.New("mr12.5.1.3", 5)
	l, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	dir := t.TempDir()
	conf, _, _ := mock.WriteConfs(dir, mock.ConfOpts{CLI: l.Addr().String()})
	cfg, _ := config.Load(filepath.Join(dir, "none.yaml"))
	// no cli: in the host entry, listen-cli comes from rtpengine.conf
	if err := cfg.AddHost(config.Host{Name: "x", Address: "local", ConfigFile: conf}); err != nil {
		t.Fatal(err)
	}
	c := rtpengine.New(cfg, &cfg.Hosts[0])
	info, err := c.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version.String() != "mr12.5.1.3" || info.Version.Source != "rtpengine --version" {
		t.Errorf("version %s from %q", info.Version, info.Version.Source)
	}
	if info.CLI != "tcp "+l.Addr().String() {
		t.Errorf("CLI %q, want listen-cli from the config", info.CLI)
	}
	if !info.Caps.Has("set redisdisable") || info.Caps.Has("usage") {
		t.Errorf("caps from help: %v", info.Caps.Commands())
	}
}

// TestTunnelExchange checks the ssh -W pipe handling with a stand-in ssh: the
// test binary itself, see TestHelperSSHW.
func TestTunnelExchange(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fakeBin(t, "ssh", `exec env VOIP_TUI_FAKE_SSH=1 "`+exe+`" -test.run='^TestHelperSSHW$' -- "$@"`)
	srv := mock.New("mr11.5.1", 9)
	l, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	cli := &rtpengine.TunnelCLI{SSH: &rtpengine.SSH{Dest: "rtp1"}, Addr: l.Addr().String()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		out, err := cli.Do(ctx, "list numsessions")
		if err != nil || !strings.Contains(out, "Current sessions total") {
			t.Fatalf("round %d: %q %v", i, out, err)
		}
	}
}

// TestHelperSSHW is not a test but the ssh of TestTunnelExchange: with
// VOIP_TUI_FAKE_SSH set it forwards stdio to the address after -W and exits
// when the server closes the connection, as ssh -W does. nc is no stand-in:
// OpenBSD nc keeps running while its stdin is open.
func TestHelperSSHW(t *testing.T) {
	if os.Getenv("VOIP_TUI_FAKE_SSH") == "" {
		return
	}
	addr := ""
	for i, a := range os.Args {
		if a == "-W" && i+1 < len(os.Args) {
			addr = os.Args[i+1]
		}
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "channel 0: open failed:", err)
		os.Exit(255)
	}
	go func() { _, _ = io.Copy(conn, os.Stdin) }()
	_, _ = io.Copy(os.Stdout, conn)
	os.Exit(0)
}

// Fixtures follow cli_incoming_list_interfaces of each branch.
func TestParseInterfacesLayouts(t *testing.T) {
	mr95 := "Interface 'pub' address '10.0.0.1' (IPv4)\n Port range: 30000 - 40000\n Ports used:    64 / 10001 (  0.6%)\n Last port used: 30032\n"
	i := rtpengine.ParseInterfaces(mr95)
	if len(i) != 1 || i[0].Used != 64 || i[0].Total != 10001 || i[0].LastPort != "30032" || i[0].HasTraffic || len(i[0].Extra) != 0 {
		t.Fatalf("mr9.5: %+v", i)
	}
	mr115 := "Interface 'pub' address '10.0.0.1' (IPv4)\n Port range: 30000 - 40000\n Ports used:    64 / 10001 (  0.6%)\n" +
		"Interface 'priv' address 'fd00::1' (IPv6)\n Port range: 20000 - 29999\n Ports used:     2 / 10000 (  0.0%)\n"
	if i = rtpengine.ParseInterfaces(mr115); len(i) != 2 || i[1].Family != "IPv6" || i[1].PortMax != 29999 {
		t.Fatalf("mr11.5: %+v", i)
	}
	mr125 := `Interface 'pub' address '10.0.0.1' (IPv4)
 Port range: 30000 - 40000
 Ports used:    84 / 10001 (  0.8%)
 Packets/bytes/errors:
  Ingress:   1393629496 / 238438752930 /         99
  Egress:    1380000000 / 236000000000 /          0
 Packets lost/duplicates:     696814 /      15484
 MOS:    avg 4.2, packet loss avg   1%
 Jitter: avg   3 (measured   2)
 RTT:    e2e  18, dsct   6
 Something new: 7

Interface 'priv' address '10.10.0.2' (IPv4)
 Port range: 20000 - 29999
 Ports used:     0 / 10000 (  0.0%)
 Packets/bytes/errors:
  Ingress:            0 /          0 /          0
  Egress:             0 /          0 /          0
 Packets lost/duplicates:          0 /          0
 MOS:    avg nan, packet loss avg -nan%
 Jitter: avg nan (measured nan)
 RTT:    e2e nan, dsct nan
`
	i = rtpengine.ParseInterfaces(mr125)
	if len(i) != 2 {
		t.Fatalf("mr12.5: %d interfaces", len(i))
	}
	p := i[0]
	if !p.HasTraffic || p.In.Packets != "1393629496" || p.In.Bytes != "238438752930" || p.In.Errors != "99" || p.Out.Errors != "0" ||
		p.Lost != "696814" || p.Duplicates != "15484" || p.MOS != "4.2" || p.PacketLoss != "1" || p.Jitter != "3" || p.JitterMeas != "2" ||
		p.RTTE2E != "18" || p.RTTDiscrete != "6" {
		t.Fatalf("pub: %+v", p)
	}
	if len(p.Extra) != 1 || p.Extra[0].Key != "Something new" {
		t.Fatalf("unknown lines should land in Extra: %+v", p.Extra)
	}
	if q := i[1]; q.MOS != "" || q.PacketLoss != "" || q.Jitter != "" || q.RTTE2E != "" || len(q.Extra) != 0 {
		t.Fatalf("nan averages should be empty: %+v", q)
	}
}

func TestRedisEndpointAndMask(t *testing.T) {
	e, ok := rtpengine.ParseRedisEndpoint("s3cr3t@10.0.0.5:6379/3")
	if !ok || e.Addr() != "10.0.0.5:6379" || e.DB != 3 || e.String() != "***@10.0.0.5:6379/3" {
		t.Fatalf("endpoint %+v %q", e, e.String())
	}
	if e, ok := rtpengine.ParseRedisEndpoint("127.0.0.1:6379"); !ok || e.DB != -1 {
		t.Fatalf("no-db endpoint %+v", e)
	}
	for k, v := range map[string]string{"redis": "pw@192.0.2.4:6379/1", "redis-write": "pw@192.0.2.4:6379/1", "mysql-pass": "secret"} {
		if m := rtpengine.MaskSecret(k, v); strings.Contains(m, "pw@") || strings.Contains(m, "secret") {
			t.Errorf("%s not masked: %q", k, m)
		}
	}
	if m := rtpengine.MaskSecret("timeout", "60"); m != "60" {
		t.Errorf("plain value changed: %q", m)
	}
}

func TestLogCommand(t *testing.T) {
	c := rtpengine.New(&config.Config{}, &config.Host{Name: "x", Address: "local"})
	got := c.LogCommand("a'b@x", 1000)
	want := `journalctl -t rtpengine --since @1000 --no-pager -o short-iso-precise | grep -F -- 'a'\''b@x'`
	if got != want {
		t.Fatalf("default:\n%s\nwant\n%s", got, want)
	}
	if got := c.LogCommand("id", 0); !strings.Contains(got, "--since -24h") {
		t.Fatalf("unknown start: %s", got)
	}
	c.Host.LogCommand = "grep -hF -- {callid} /var/log/rtpengine.log"
	if got := c.LogCommand("id@h", 1000); got != "grep -hF -- id@h /var/log/rtpengine.log" {
		t.Fatalf("custom: %s", got)
	}
}

func TestReleaseRanges(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"mr6.5", "mr7.5", "mr8.5", "mr9.5", "mr10.5", "mr11.5", "mr12.5", "mr13.0", "mr13.2", "mr13.3", "master"}, "since mr6.5"},
		{[]string{"mr13.3", "master"}, "since mr13.3"},
		{[]string{"mr6.5", "mr7.5", "mr8.5"}, "mr6.5 – mr8.5"},
		{[]string{"mr9.5", "mr12.5", "mr13.0", "mr13.2", "mr13.3", "master"}, "mr9.5, since mr12.5"},
		{nil, ""},
	} {
		if got := rtpengine.ReleaseRanges(c.in); got != c.want {
			t.Errorf("%v: got %q, want %q", c.in, got, c.want)
		}
	}
}

// A reply in the layout of statistics.c: one line, sections, two sockets of
// one proxy.
const jsonStatsReply = `{"currentstatistics":{"sessionsown":3,"sessionsforeign":1,"sessionstotal":4},` +
	`"totalstatistics":{"uptime":3600,"managedsessions":120},` +
	`"controlstatistics":{"proxies":[` +
	`{"proxy":"10.0.0.5:40001","pingcount":10,"pingduration":0.001,"offercount":4,"offerduration":0.004,"answercount":3,"answerduration":0.003,"errorcount":1},` +
	`{"proxy":"10.0.0.5:40002","pingcount":10,"pingduration":0.001,"offercount":6,"offerduration":0.008,"answercount":5,"answerduration":0.005,"errorcount":0},` +
	`{"proxy":"10.0.0.6:40001","pingcount":5,"pingduration":0.001,"offercount":0,"offerduration":0,"answercount":0,"answerduration":0,"errorcount":0}],` +
	`"totalpingcount":25,"totaloffercount":10,"totalanswercount":8,"totaldeletecount":0},` +
	`"interfaces":[{"name":"pub","address":"10.0.0.1","ports":{"min":30000,"max":40000,"used":5,"used_pct":0.05,"free":9996,"totals":10001}}]}`

func TestJSONStats(t *testing.T) {
	st, err := rtpengine.ParseJSONStats(jsonStatsReply + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if n := st.Obj("currentstatistics").Int("sessionstotal"); n != 4 {
		t.Fatalf("sessionstotal %d", n)
	}
	if _, ok := st.Obj("mos").Num("mos_average"); ok {
		t.Fatal("a section the release does not print is missing, not 0")
	}
	ng := st.NGByProxy()
	p := ng["10.0.0.5"]
	if len(ng) != 2 || p == nil || p.Sockets != 2 || p.Counts["offer"] != 10 || p.Errors != 1 || p.Total() != 38 {
		t.Fatalf("by proxy: %+v", p)
	}
	if avg, ok := p.Avg("offer"); !ok || avg != 1200*time.Microsecond {
		t.Fatalf("offer avg %v", avg)
	}
	if _, ok := ng["10.0.0.6"].Avg("offer"); ok {
		t.Fatal("no offers, no average")
	}
	tot := st.NGTotals()
	if len(tot) != 3 || tot[0].Key != "ping" || tot[1].Key != "offer" || tot[2].Key != "answer" {
		t.Fatalf("totals: %+v", tot)
	}
	if ports := st.List("interfaces")[0].Obj("ports"); ports.Int("used") != 5 {
		t.Fatalf("interface ports: %v", ports)
	}
}

func TestParseLogLevels(t *testing.T) {
	got := rtpengine.ParseLogLevels("core = 6\nice = 7\n", "core - Everything that isn't part of another subsystem\nice - ICE negotiation\n")
	if len(got) != 2 || got[0] != (rtpengine.LogLevel{Name: "core", Level: 6, Desc: "Everything that isn't part of another subsystem"}) ||
		got[1].Name != "ice" || got[1].Level != 7 || rtpengine.LevelName(got[1].Level) != "debug" {
		t.Fatalf("mr9.5+: %+v", got)
	}
	// before mr9.5: one number, no subsystems
	if got := rtpengine.ParseLogLevels("6\n", ""); len(got) != 1 || got[0].Name != "" || got[0].Level != 6 {
		t.Fatalf("before mr9.5: %+v", got)
	}
}

func TestParseSetLogLevel(t *testing.T) {
	if sub, n, err := rtpengine.ParseSetLogLevel("7"); err != nil || sub != "" || n != 7 {
		t.Fatalf("all: %q %d %v", sub, n, err)
	}
	if sub, n, err := rtpengine.ParseSetLogLevel("ice 5"); err != nil || sub != "ice" || n != 5 {
		t.Fatalf("one: %q %d %v", sub, n, err)
	}
	for _, bad := range []string{"", "ice", "ice x", "a b c"} {
		if _, _, err := rtpengine.ParseSetLogLevel(bad); err == nil {
			t.Fatalf("%q should fail", bad)
		}
	}
}
