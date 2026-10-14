package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/internal/opensips/config"
)

// Respond answers a remote argv the way a host with OpenSIPS would.
func Respond(h *Host, argv []string) opensips.Result {
	if len(argv) > 0 && argv[0] == "sudo" {
		argv = argv[1:]
		if len(argv) > 0 && argv[0] == "-n" {
			argv = argv[1:]
		}
	}
	legacy := h.version().Less(opensips.MinCLIVersion)
	if len(argv) >= 7 && argv[0] == "sh" && argv[1] == "-c" && argv[3] == opensips.FIFOTag {
		return h.fifo(argv[4], argv[5], argv[6])
	}
	if len(argv) >= 2 && argv[0] == "sh" && argv[1] == "-c" {
		cli, cliVer, ctl := "/usr/bin/opensips-cli", "OpenSIPS CLI 0.3.1", ""
		if legacy {
			cli, cliVer, ctl = "", "", "/usr/sbin/opensipsctl"
		}
		pkgs := "opensips " + h.Binary + "-1;opensips-http-modules " + h.Binary + "-1;"
		if cli != "" {
			pkgs += "opensips-cli 0.3.1-1;"
		}
		return opensips.Result{Out: fmt.Sprintf("binary=version: opensips %s (x86_64/linux)\ncli=%s\ncli_version=%s\nctl=%s\n"+
			"pid=%d\ncmdline=/usr/sbin/opensips -P /run/opensips/opensips.pid -f /etc/opensips/opensips.cfg -m 256 -M 16\npkg=%s\n",
			h.Binary, cli, cliVer, ctl, h.pid(), pkgs)}
	}
	for i, a := range argv {
		if a == "-x" && i+2 < len(argv)+1 && i+1 < len(argv) && argv[i+1] == "mi" && i+2 < len(argv) {
			if legacy {
				return opensips.Result{RC: 255, Err: "ERROR: cannot parse MI reply: not a JSON-RPC response\n"}
			}
			ans, err := h.Call(argv[i+2], argv[i+3:])
			if err != nil {
				return opensips.Result{RC: 255, Err: fmt.Sprintf("ERROR: command '%s' returned error: %v\n", argv[i+2], err)}
			}
			out, _ := json.MarshalIndent(ans, "", "    ")
			return opensips.Result{Out: string(out) + "\n"}
		}
	}
	if len(argv) >= 3 && argv[1] == "fifo" {
		out, err := h.Fifo(argv[2])
		if err != nil {
			return opensips.Result{RC: 1, Out: err.Error()}
		}
		return opensips.Result{Out: out + "\n"}
	}
	name := ""
	if len(argv) > 0 {
		name = argv[0]
	}
	return opensips.Result{RC: 127, Err: "sh: " + name + ": not found\n"}
}

// Transport is the in-process emulator for tests.
type Transport struct {
	host   *Host
	spec   Spec
	target string
}

var (
	hostsMu sync.Mutex
	hosts   = map[string]*Host{}
)

// Reset forgets all emulated hosts (tests).
func Reset() {
	hostsMu.Lock()
	hosts = map[string]*Host{}
	hostsMu.Unlock()
}

// NewTransport returns the emulator for a host, as its ssh target names it
// (see ParseTarget); hosts keep state by name.
func NewTransport(c *config.Host) opensips.Transport {
	hostsMu.Lock()
	defer hostsMu.Unlock()
	spec := ParseTarget(c.Target())
	h, ok := hosts[c.Name]
	if !ok {
		h = NewHost(c.Name, spec.Version, spec.Binary, spec.Without)
		hosts[c.Name] = h
	}
	return &Transport{host: h, spec: spec, target: c.Target()}
}

// Run answers like ssh would.
func (t *Transport) Run(ctx context.Context, argv []string) (opensips.Result, error) {
	if t.spec.Unreachable {
		return opensips.Result{}, &opensips.Unreachable{Msg: "ssh: connect to host " + t.target + " port 22: Connection refused"}
	}
	return Respond(t.host, argv), nil
}

// Interactive is not possible on an emulated host.
func (t *Transport) Interactive([]string) []string { return nil }

// Close does nothing.
func (t *Transport) Close() {}

// SSHMain is opensips-tui-mock: it takes ssh arguments and answers as the
// host the target names (see ParseTarget). State is kept in stateDir between
// runs.
func SSHMain(args []string, stateDir string) (opensips.Result, error) {
	target, remote := "", ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) {
				target = args[i+1]
			}
			if i+2 < len(args) {
				remote = strings.Join(args[i+2:], " ")
			}
			i = len(args)
		case a == "-O":
			return opensips.Result{}, nil // control command: exit, check
		case a == "-o" || a == "-p" || a == "-l" || a == "-i" || a == "-F" || a == "-J":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			target = a
			remote = strings.Join(args[i+1:], " ")
			i = len(args)
		}
	}
	if target == "" {
		return opensips.Result{RC: 255, Err: "usage: opensips-tui-mock [ssh options] -- target command\n"}, nil
	}
	if ParseTarget(target).Unreachable {
		return opensips.Result{RC: 255, Err: "ssh: connect to host " + target + " port 22: Connection refused\n"}, nil
	}
	argv, err := SplitShell(remote)
	if err != nil {
		return opensips.Result{RC: 2, Err: err.Error() + "\n"}, nil
	}
	if len(argv) == 0 {
		return opensips.Result{RC: 1, Err: "opensips-tui-mock has no interactive shell\n"}, nil
	}
	h, save, err := loadHost(target, stateDir)
	if err != nil {
		return opensips.Result{}, err
	}
	res := Respond(h, argv)
	save()
	return res, nil
}

// loadHost reads the emulated host of a target from stateDir; save writes it back.
func loadHost(target, stateDir string) (h *Host, save func(), err error) {
	spec := ParseTarget(target)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(stateDir, regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(target, "_")+".json")
	h = NewHost(target, spec.Version, spec.Binary, spec.Without)
	if data, err := os.ReadFile(path); err == nil {
		if loaded, err := LoadHost(data); err == nil {
			h = loaded
		}
	}
	return h, func() {
		if data, err := h.MarshalState(); err == nil {
			tmp := fmt.Sprintf("%s.%d", path, os.Getpid())
			if os.WriteFile(tmp, data, 0o600) == nil {
				_ = os.Rename(tmp, path)
			}
		}
	}, nil
}

// TunnelMain is opensips-tui-mock -W <addr> <target>: ssh's direct-tcpip
// channel to mi_http on the host, over stdin and stdout. It reports whether
// the arguments asked for a tunnel, and the exit code.
func TunnelMain(args []string, stateDir string, in io.Reader, out io.Writer, errOut io.Writer) (bool, int) {
	addr, target := "", ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-W" && i+1 < len(args):
			addr = args[i+1]
			i++
		case a == "--":
			if i+1 < len(args) {
				target = args[i+1]
			}
			i = len(args)
		case a == "-o" || a == "-p" || a == "-l" || a == "-i" || a == "-F" || a == "-J":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			target = a
		}
	}
	if addr == "" {
		return false, 0
	}
	if ParseTarget(target).Unreachable {
		fmt.Fprintf(errOut, "ssh: connect to host %s port 22: Connection refused\n", target)
		return true, 255
	}
	h, save, err := loadHost(target, stateDir)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return true, 255
	}
	if (addr != "127.0.0.1:8888" && addr != "localhost:8888") || !h.loaded("mi_http") {
		fmt.Fprintf(errOut, "channel 0: open failed: connect failed: Connection refused\nstdio forwarding failed\n")
		return true, 255
	}
	h.serveHTTP(stdioConn{in, out})
	save()
	return true, 0
}

// stdioConn is stdin and stdout as a net.Conn, for serveHTTP.
type stdioConn struct {
	io.Reader
	io.Writer
}

func (stdioConn) Close() error                     { return nil }
func (stdioConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (stdioConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (stdioConn) SetDeadline(time.Time) error      { return nil }
func (stdioConn) SetReadDeadline(time.Time) error  { return nil }
func (stdioConn) SetWriteDeadline(time.Time) error { return nil }

// SplitShell splits a POSIX shell command line (quotes and backslashes, no expansion).
func SplitShell(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, fmt.Errorf("unterminated quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case ch == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated quote")
			}
			inWord = true
		case ch == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		case ch == ' ' || ch == '\t' || ch == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}
