package opensips

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/opensips/config"
)

// MI over JSON-RPC 2.0: what mi_fifo and mi_http speak in OpenSIPS 3.x, and
// what opensips-cli itself sends. Named params go as an object, positional
// ones as an array, all as strings: MI converts numbers itself.

var rpcID atomic.Int64

// rpcRequest is a JSON-RPC request for an MI command.
func rpcRequest(command string, p Params) []byte {
	req := map[string]any{"jsonrpc": "2.0", "method": command, "id": rpcID.Add(1)}
	switch v := p.(type) {
	case nil:
	case []string:
		if len(v) > 0 {
			req["params"] = v
		}
	case map[string]string:
		if len(v) > 0 {
			req["params"] = v
		}
	default:
		req["params"] = []string{fmt.Sprint(p)}
	}
	b, _ := json.Marshal(req)
	return b
}

// ParseRPC reads a JSON-RPC reply: the result decoded as opensips-cli prints
// it (numbers as json.Number), or the MI error.
func ParseRPC(body []byte) (any, error) {
	var rep struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &rep); err != nil {
		return nil, fmt.Errorf(i18n.T("not a JSON-RPC reply: %s"), firstLine(string(body)))
	}
	if rep.Error != nil {
		return nil, &MIError{Code: rep.Error.Code, Message: rep.Error.Message}
	}
	if len(rep.Result) == 0 {
		return nil, nil
	}
	v, ok := decodeJSON(string(rep.Result))
	if !ok {
		return nil, errors.New(i18n.T("bad JSON-RPC result"))
	}
	return v, nil
}

// FIFOScript talks to mi_fifo with plain shell tools: a reply fifo in the
// reply_dir, the request ":<reply name>:<json>" into the MI fifo, then the
// reply. Arguments: $1 MI fifo, $2 reply dir, $3 request. FIFOTag is $0, so
// the emulator recognises the call.
const FIFOScript = `f=$1; d=${2%/}; r=$d/opensips-tui.$$
[ -p "$f" ] || { echo "no MI fifo at $f: is mi_fifo loaded with this fifo_name?" >&2; exit 3; }
mkfifo -m 666 "$r" || exit 1
trap 'rm -f "$r"' EXIT HUP INT TERM
printf ':%s:%s\n' "${r##*/}" "$3" > "$f" || exit 1
cat "$r"`

// FIFOTag names the fifo script in ps and for the emulator.
const FIFOTag = "opensips-tui-mi"

// fifoArgv is the remote command for one request over mi_fifo.
func (c *Client) fifoArgv(req []byte) []string {
	return append(c.sudo(false), "sh", "-c", FIFOScript, FIFOTag, c.Host.FIFO, c.Host.ReplyDir, string(req))
}

// Dialer opens a TCP connection to addr as seen from the host (ssh -W).
type Dialer interface {
	Dial(ctx context.Context, addr string) (net.Conn, error)
}

// DirectDialer replaces the network for via: direct (the emulator).
type DirectDialer interface {
	DialDirect(ctx context.Context, addr string) (net.Conn, error)
}

// httpCall posts a request to mi_http, through the host (via: http) or
// straight from here (via: direct).
func (c *Client) httpCall(ctx context.Context, req []byte) (any, error) {
	u, err := url.Parse(c.Host.MI)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf(i18n.T("mi: %q is not a URL like %s"), c.Host.MI, config.DefaultMI)
	}
	addr := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		addr = net.JoinHostPort(u.Hostname(), port)
	}
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		if c.Host.Via == config.ViaDirect {
			if d, ok := c.Transport.(DirectDialer); ok {
				return d.DialDirect(ctx, addr)
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}
		d, ok := c.Transport.(Dialer)
		if !ok {
			return nil, fmt.Errorf(i18n.T("this transport cannot tunnel to %s"), addr)
		}
		return d.Dial(ctx, addr)
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true}}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host.MI, bytes.NewReader(req))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(hreq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &Unreachable{Msg: "no answer: " + ctx.Err().Error()}
		}
		var u *Unreachable
		if errors.As(err, &u) {
			return nil, u
		}
		var ue *url.Error // drop Go's `Post "<url>":`, keep what went wrong
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("mi_http: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && !bytes.Contains(body, []byte(`"jsonrpc"`)) {
		return nil, fmt.Errorf(i18n.T("mi_http at %s: %s"), c.Host.MI, resp.Status) // e.g. another root
	}
	return ParseRPC(body)
}

// Dial opens a direct-tcpip channel to addr as seen from the host (ssh -W).
func (s *SSH) Dial(ctx context.Context, addr string) (net.Conn, error) {
	argv := append(append([]string{}, s.options...), "-W", addr, "--", s.target)
	cmd := exec.Command(s.command, argv...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var errb strings.Builder
	cmd.Stderr = &errb
	cmd.WaitDelay = 2 * time.Second // a child left behind would keep stderr open and Wait waiting
	if err := cmd.Start(); err != nil {
		return nil, &Unreachable{Msg: fmt.Sprintf("%s: %v", s.command, err)}
	}
	return &pipeConn{cmd: cmd, in: in, out: out, stderr: &errb, addr: addr}, nil
}

// pipeConn is ssh -W as a net.Conn.
type pipeConn struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    io.ReadCloser
	stderr *strings.Builder
	addr   string
}

func (p *pipeConn) Read(b []byte) (int, error) {
	n, err := p.out.Read(b)
	if err == io.EOF && n == 0 {
		if msg := strings.TrimSpace(p.stderr.String()); msg != "" {
			// ssh is up but nothing listens on addr: an MI problem, not an unreachable host
			for _, l := range strings.Split(msg, "\n") {
				if strings.Contains(l, "open failed") {
					return 0, fmt.Errorf(i18n.T("nothing answers on %s on the host (%s)"), p.addr, strings.TrimSpace(l))
				}
			}
			if sshFailure.MatchString(msg) {
				return 0, &Unreachable{Msg: lastLine(msg, "ssh failed")}
			}
			return 0, errors.New(lastLine(msg, "tunnel closed"))
		}
	}
	return n, err
}
func (p *pipeConn) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *pipeConn) Close() error {
	p.in.Close()
	p.out.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
	return nil
}
func (p *pipeConn) LocalAddr() net.Addr              { return pipeAddr("ssh") }
func (p *pipeConn) RemoteAddr() net.Addr             { return pipeAddr(p.addr) }
func (p *pipeConn) SetDeadline(time.Time) error      { return nil }
func (p *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (p *pipeConn) SetWriteDeadline(time.Time) error { return nil }

type pipeAddr string

func (a pipeAddr) Network() string { return "ssh" }
func (a pipeAddr) String() string  { return string(a) }

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
