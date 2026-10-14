package opensips_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	. "github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/internal/opensips/config"
	"github.com/napadailo/voip-tui/internal/opensips/mock"
)

func TestParseRPC(t *testing.T) {
	v, err := ParseRPC([]byte(`{"jsonrpc":"2.0","result":{"Server":"OpenSIPS (3.4.3 (x86_64/linux))","n":5},"id":1}`))
	if err != nil || v.(map[string]any)["n"] != json.Number("5") {
		t.Fatal(v, err)
	}
	_, err = ParseRPC([]byte(`{"jsonrpc":"2.0","error":{"code":404,"message":"Dialog not found"},"id":2}`))
	var mi *MIError
	if !errors.As(err, &mi) || mi.Code != 404 || mi.Message != "Dialog not found" {
		t.Fatal(err)
	}
	if _, err := ParseRPC([]byte("ERROR: bad")); err == nil || !strings.Contains(err.Error(), "not a JSON-RPC reply") {
		t.Fatal(err)
	}
}

// The same MI answers through every via: the views do not care how MI is reached.
func TestEveryVia(t *testing.T) {
	ctx := context.Background()
	want := map[string]string{"edge-n1": BackendCLI, "core-fifo": BackendFIFO, "core-http": BackendHTTP, "edge-direct": BackendHTTP}
	var first []string
	for _, name := range []string{"edge-n1", "core-fifo", "core-http", "edge-direct"} {
		c := demoClient(t, name)
		h, err := c.Probe(ctx)
		if err != nil || h.Backend != want[name] || h.Error != "" || h.Commands == nil || h.Uptime <= 0 || h.Running.IsZero() {
			t.Fatalf("%s: %+v %v", name, h, err)
		}
		if !c.Supports("dialog.list").OK {
			t.Fatalf("%s: dialogs not supported", name)
		}
		got, err := FindResource("dlg").Fetch(ctx, c, map[string]any{})
		if err != nil || len(got.Rows) == 0 {
			t.Fatalf("%s: %v", name, err)
		}
		// named params and the MI error code arrive through each transport
		_, err = c.Op(ctx, "dialog.end", Args{"id": "nope"})
		var mi *MIError
		if !errors.As(err, &mi) || mi.Code != 404 {
			t.Fatalf("%s: %v", name, err)
		}
		cols := got.Columns
		if first == nil {
			first = cols
		} else if !reflect.DeepEqual(cols, first) {
			t.Fatalf("%s: columns %v, want %v", name, cols, first)
		}
	}
	c := demoClient(t, "core-fifo")
	c.Probe(ctx)
	cmd, p, _ := c.CommandFor("dialog.end", Args{"id": "42"})
	if got := c.Render(cmd, p); got != "MI dlg_end_dlg dialog_id=42 (via fifo)" {
		t.Fatal(got)
	}
}

func TestViaErrors(t *testing.T) {
	ctx := context.Background()
	h, err := demoClient(t, "lab-nohttp").Probe(ctx)
	if err != nil || h.Backend != "" || !strings.Contains(h.Error, "connection refused") {
		t.Fatalf("mi_http not loaded: %+v %v", h, err)
	}
	mock.Reset()
	cfg := mock.Demo()
	c, _ := cfg.Find("core-fifo")
	c.FIFO = "/run/opensips/fifo"
	h, err = NewClient(c, mock.NewTransport(c), false).Probe(ctx)
	if err != nil || h.Backend != "" || !strings.Contains(h.Error, "no MI fifo at /run/opensips/fifo") {
		t.Fatalf("wrong fifo: %+v %v", h, err)
	}
	if _, err := config.Load(writeConfig(t, "hosts:\n  - name: x\n    via: tunnel\n")); err == nil || !strings.Contains(err.Error(), "want cli, fifo, http or direct") {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// FIFOScript in a real sh against a stand-in for OpenSIPS that reads the MI
// fifo and answers into the reply fifo the way mi_fifo does.
func TestFIFOScriptInShell(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "opensips_fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no mkfifo here:", err)
	}
	served := make(chan string, 1)
	go func() {
		f, err := os.Open(fifo) // blocks until the script writes
		if err != nil {
			served <- err.Error()
			return
		}
		line, _ := bufio.NewReader(f).ReadString('\n')
		f.Close()
		// ":<reply name>:<json>"
		parts := strings.SplitN(strings.TrimPrefix(line, ":"), ":", 2)
		served <- strings.TrimSpace(parts[1])
		rep, err := os.OpenFile(filepath.Join(dir, parts[0]), os.O_WRONLY, 0)
		if err != nil {
			return
		}
		rep.WriteString(`{"jsonrpc":"2.0","result":{"Server":"OpenSIPS (3.6.1 (x86_64/linux))"},"id":1}` + "\n")
		rep.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := `{"jsonrpc":"2.0","method":"version","id":1,"params":{"a":"it's"}}`
	out, err := exec.CommandContext(ctx, "sh", "-c", FIFOScript, FIFOTag, fifo, dir+"/", req).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := <-served; got != req {
		t.Fatalf("OpenSIPS read %q", got)
	}
	v, err := ParseRPC(out)
	if err != nil || v.(map[string]any)["Server"] != "OpenSIPS (3.6.1 (x86_64/linux))" {
		t.Fatal(v, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "opensips-tui.*")); len(left) != 0 {
		t.Fatalf("reply fifo left behind: %v", left)
	}
	// no MI fifo: a clear message, no hang
	out, err = exec.CommandContext(ctx, "sh", "-c", FIFOScript, FIFOTag, filepath.Join(dir, "none"), dir, req).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "is mi_fifo loaded") {
		t.Fatalf("%v: %s", err, out)
	}
}
