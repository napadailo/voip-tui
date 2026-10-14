package opensips_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/internal/opensips/config"
	"github.com/napadailo/voip-tui/internal/opensips/mock"
)

func demoClient(t *testing.T, name string) *Client {
	t.Helper()
	mock.Reset()
	cfg := mock.Demo()
	c, ok := cfg.Find(name)
	if !ok {
		t.Fatalf("no demo host %s", name)
	}
	return NewClient(c, mock.NewTransport(c), false)
}

func TestParseVersion(t *testing.T) {
	v, _ := ParseVersion("OpenSIPS (3.4.3 (x86_64/linux))")
	if v != V("3.4.3") {
		t.Fatal(v)
	}
	if v, _ := ParseVersion("version: opensips 2.4.11 (x86_64/linux)"); v != V("2.4.11") {
		t.Fatal(v)
	}
	if !V("3.1.10").Less(V("3.2")) || !V("3.2").Less(V("3.10")) {
		t.Fatal("order")
	}
}

func TestOperationVariants(t *testing.T) {
	op := Operations["dialog.end"]
	if p := op.Resolve(V("3.4.1")).Params(Args{"id": "42"}); p.(map[string]string)["dialog_id"] != "42" {
		t.Fatal(p)
	}
	if p := op.Resolve(V("2.4.11")).Params(Args{"h_entry": 1, "h_id": 2}); strings.Join(p.([]string), ",") != "1,2" {
		t.Fatal(p)
	}
}

func TestCheck(t *testing.T) {
	if s := Check("sr.list_status", V("3.1.10"), nil, false); s.OK || !strings.Contains(s.Reason, "3.2+") {
		t.Fatal(s)
	}
	if s := Check("sr.list_status", V("3.2.0"), map[string]bool{"sr_list_status": true}, false); !s.OK {
		t.Fatal(s)
	}
	if s := Check("ds.list", V("3.4.0"), map[string]bool{"dlg_list": true}, false); s.OK || !strings.Contains(s.Reason, "ds_list") {
		t.Fatal(s)
	}
	if s := Check("dialog.end", V("3.4.0"), map[string]bool{"dlg_end_dlg": true}, true); s.OK {
		t.Fatal("read-only must block")
	}
}

func TestSSHArgvQuoting(t *testing.T) {
	ctx := &config.Host{Name: "gw", SSH: "gw.example", Port: 2222, User: "ops"}
	s := NewSSH(ctx, config.SSHConfig{Command: "ssh", ConnectTimeoutSeconds: 5, ControlPersistSeconds: 60})
	argv := s.Argv([]string{"opensips-cli", "-x", "mi", "ul_rm_contact", "contact=sip:a@b;transport=tcp"}, false)
	if argv[len(argv)-2] != "gw.example" || argv[len(argv)-3] != "--" {
		t.Fatal(argv)
	}
	if got := argv[len(argv)-1]; got != "opensips-cli -x mi ul_rm_contact 'contact=sip:a@b;transport=tcp'" {
		t.Fatal(got)
	}
	words, _ := mock.SplitShell(argv[len(argv)-1])
	if words[4] != "contact=sip:a@b;transport=tcp" {
		t.Fatal(words)
	}
	if got := ShellQuote("it's"); got != `'it'"'"'s'` {
		t.Fatal(got)
	}
}

func TestCLIArgv(t *testing.T) {
	c := NewClient(&config.Host{Name: "gw", CLI: "opensips-cli", Sudo: true, Instance: "edge", CLIArgs: []string{"-f", "/etc/o.cfg"}}, nil, false)
	got := strings.Join(c.CLIArgv("mi", "version"), " ")
	if got != "sudo -n opensips-cli -f /etc/o.cfg -i edge -o output_type=pretty-print -x mi version" {
		t.Fatal(got)
	}
}

func TestParseOutput(t *testing.T) {
	if ParseOutput(`"OK"`) != "OK" {
		t.Fatal("string")
	}
	m, _ := ParseOutput("WARNING: x\n{\n \"a\": 1\n}").(map[string]any)
	if m == nil || m["a"] == nil {
		t.Fatal(m)
	}
}

func TestProbeVersions(t *testing.T) {
	ctx := context.Background()
	h, err := demoClient(t, "edge-n2").Probe(ctx)
	if err != nil || h.Backend != BackendCLI || !h.RestartPending() || len(h.Warnings) != 0 {
		t.Fatal(h, err)
	}
	legacy := demoClient(t, "legacy-24")
	h, err = legacy.Probe(ctx)
	if err != nil || h.Backend != BackendLegacy || h.Running != V("2.4.11") || legacy.Supports("dialog.list").OK {
		t.Fatal(h, err)
	}
	if _, err := demoClient(t, "down").Probe(ctx); !IsUnreachable(err) {
		t.Fatal(err)
	}
}

func TestEndDialogRoundtrip(t *testing.T) {
	ctx := context.Background()
	c := demoClient(t, "edge-n1")
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	res := FindResource("dlg")
	before, err := res.Fetch(ctx, c, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	id := before.Rows[0].Key
	cmd, p, _ := c.CommandFor("dialog.end", Args{"id": id})
	if got := c.Render(cmd, p); got != "opensips-cli -x mi dlg_end_dlg dialog_id="+id {
		t.Fatal(got)
	}
	if _, err := c.Op(ctx, "dialog.end", Args{"id": id}); err != nil {
		t.Fatal(err)
	}
	after, _ := res.Fetch(ctx, c, map[string]any{})
	if len(after.Rows) != len(before.Rows)-1 {
		t.Fatal(len(after.Rows), len(before.Rows))
	}
	_, err = c.Op(ctx, "dialog.end", Args{"id": id})
	var mi *MIError
	if !errors.As(err, &mi) || mi.Code != 404 {
		t.Fatal(err)
	}
}

func TestAllResourcesAndActions(t *testing.T) {
	ctx := context.Background()
	c := demoClient(t, "core-36")
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	for _, r := range Resources {
		tbl, err := r.Fetch(ctx, c, map[string]any{})
		if err != nil {
			t.Fatalf("%s: %v", r.Name, err)
		}
		for _, row := range tbl.Rows {
			if len(row.Cells) != len(tbl.Columns) {
				t.Fatalf("%s: row %s has %d cells", r.Name, row.Key, len(row.Cells))
			}
		}
		// every action must be accepted by the emulator (named params match the capability table)
		for _, a := range r.Actions {
			var args Args
			if a.Args != nil {
				// read again: the action before may have removed the row
				if tbl, err = r.Fetch(ctx, c, map[string]any{}); err != nil {
					t.Fatalf("%s: %v", r.Name, err)
				}
				if len(tbl.Rows) == 0 {
					continue
				}
				args = a.Args(&tbl.Rows[0])
			}
			if _, err := c.Op(ctx, a.Op, args); err != nil {
				t.Fatalf("%s/%s: %v", r.Name, a.Label, err)
			}
		}
	}
}

func TestRecords(t *testing.T) {
	recs := Records([]any{map[string]any{"Group": "dr", "Identifiers": []any{map[string]any{"Name": "a"}, map[string]any{"Name": "b"}}}}, nil)
	if len(recs) != 2 || recs[1]["Group"] != "dr" || recs[1]["Name"] != "b" {
		t.Fatal(recs)
	}
}

func buildMock(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "opensips-tui-mock")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/opensips-tui-mock")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mock: %v\n%s", err, out)
	}
	return bin
}

// opensips-tui-mock -confdir writes a config whose hosts answer through the
// emulator as ssh and, with via: direct, through its mi_http.
func TestMockDemoConfig(t *testing.T) {
	bin := buildMock(t)
	dir := t.TempDir()
	t.Setenv("OPENSIPS_TUI_MOCK_STATE", t.TempDir())
	srv := exec.Command(bin, "-confdir", dir, "-listen", "127.0.0.1:0")
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })
	path := filepath.Join(dir, "opensips-tui.yaml")
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 10 || cfg.SSH.Command != bin || cfg.CurrentHost != "edge-n1" {
		t.Fatalf("%d hosts, ssh %q, current %q", len(cfg.Hosts), cfg.SSH.Command, cfg.CurrentHost)
	}
	ctx := context.Background()
	probe := func(name string) (*Client, *HostInfo, error) {
		h, ok := cfg.Find(name)
		if !ok {
			t.Fatalf("no host %s", name)
		}
		c := NewClient(h, NewSSH(h, cfg.SSH), false)
		info, err := c.Probe(ctx)
		return c, info, err
	}
	for name, want := range map[string]struct{ backend, running string }{
		"edge-n1": {BackendCLI, "3.4.7"}, "core-fifo": {BackendFIFO, "3.6.1"}, "core-http": {BackendHTTP, "3.6.1"},
		"edge-direct": {BackendHTTP, "3.4.7"}, "legacy-24": {BackendLegacy, "2.4.11"},
	} {
		if _, h, err := probe(name); err != nil || h.Backend != want.backend || h.Running != V(want.running) {
			t.Fatalf("%s: %+v %v", name, h, err)
		}
	}
	if _, h, err := probe("edge-n2"); err != nil || !h.RestartPending() || h.Binary != V("3.4.9") {
		t.Fatalf("edge-n2: %+v %v", h, err)
	}
	if c, h, err := probe("lab-31"); err != nil || h.Running != V("3.1.10") || c.Supports("ds.list").OK || c.Supports("rtpengine.show").OK {
		t.Fatalf("lab-31: %+v %v", h, err)
	}
	if _, h, err := probe("lab-nohttp"); err != nil || h.Backend != "" || !strings.Contains(h.Error, "Connection refused") {
		t.Fatalf("lab-nohttp: %+v %v", h, err)
	}
	if _, _, err := probe("down"); !IsUnreachable(err) {
		t.Fatalf("down: %v", err)
	}
}

// The real ssh transport against the opensips-tui-mock binary: argv building, quoting, exit codes.
func TestSSHTransportWithMockBinary(t *testing.T) {
	bin := buildMock(t)
	t.Setenv("OPENSIPS_TUI_MOCK_STATE", t.TempDir())
	ssh := config.SSHConfig{Command: bin, ConnectTimeoutSeconds: 5, ControlPersistSeconds: 60}
	ctx := context.Background()

	up := &config.Host{Name: "lab", SSH: "mock-3.1.10", CLI: "opensips-cli", TimeoutSeconds: 10}
	c := NewClient(up, NewSSH(up, ssh), false)
	h, err := c.Probe(ctx)
	if err != nil || h.Running != V("3.1.10") || c.Supports("sr.list_status").OK {
		t.Fatal(h, err)
	}
	contacts, err := FindResource("contacts").Fetch(ctx, c, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	row := contacts.Rows[0]
	if _, err := c.Op(ctx, "ul.rm_contact", FindResource("contacts").Actions[0].Args(&row)); err != nil {
		t.Fatal(err) // contact URI with ;transport= survives ssh quoting
	}
	again, _ := FindResource("contacts").Fetch(ctx, c, map[string]any{})
	if len(again.Rows) != len(contacts.Rows)-1 {
		t.Fatal("state not kept between mock runs")
	}

	down := &config.Host{Name: "down", SSH: "mock-down", CLI: "opensips-cli", TimeoutSeconds: 10}
	if _, err := NewClient(down, NewSSH(down, ssh), false).Probe(ctx); !IsUnreachable(err) {
		t.Fatal(err)
	}

	// mi_http through a real ssh -W process, and mi_fifo through ssh
	for _, via := range []string{config.ViaHTTP, config.ViaFIFO} {
		cc := &config.Host{Name: "lab-" + via, SSH: "mock-3.4.7", Via: via, CLI: "opensips-cli", TimeoutSeconds: 10,
			FIFO: config.DefaultFIFO, ReplyDir: config.DefaultReplyDir, MI: config.DefaultMI}
		c := NewClient(cc, NewSSH(cc, ssh), false)
		h, err := c.Probe(ctx)
		if err != nil || h.Backend != via || h.Running != V("3.4.7") || h.Commands == nil {
			t.Fatalf("via %s: %+v %v", via, h, err)
		}
		if got, err := FindResource("dialogs").Fetch(ctx, c, map[string]any{}); err != nil || len(got.Rows) == 0 {
			t.Fatalf("via %s: %v", via, err)
		}
	}
	noHTTP := &config.Host{Name: "nohttp", SSH: "mock-3.4.7-nohttp", Via: config.ViaHTTP, TimeoutSeconds: 10, MI: config.DefaultMI}
	h, err = NewClient(noHTTP, NewSSH(noHTTP, ssh), false).Probe(ctx)
	if err != nil || h.Backend != "" || !strings.Contains(h.Error, "nothing answers on 127.0.0.1:8888") || !strings.Contains(h.Error, "Connection refused") {
		t.Fatalf("mi_http not loaded: %+v %v", h, err)
	}
}

// stubTransport answers every command with one result.
type stubTransport struct{ res Result }

func (s stubTransport) Run(context.Context, []string) (Result, error) { return s.res, nil }
func (s stubTransport) Interactive([]string) []string                 { return nil }
func (s stubTransport) Close()                                        {}

// When the host does not run the probe script (a forced command, a
// restricted shell), the error says so instead of "no opensips-cli".
func TestProbeThatDidNotRun(t *testing.T) {
	h := &config.Host{Name: "x", CLI: "opensips-cli", TimeoutSeconds: 5}
	res := Result{RC: 1, Err: "Banner\nThis account is restricted to interactive menus\n"}
	info, err := NewClient(h, stubTransport{res}, false).Probe(context.Background())
	if err != nil || !strings.Contains(info.Error, "exit code 1") || !strings.Contains(info.Error, "restricted to interactive menus") {
		t.Fatalf("%+v %v", info, err)
	}
}
