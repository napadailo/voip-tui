package opensips

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/opensips/config"
)

// Backends.
const (
	BackendCLI    = "cli"    // opensips-cli -x mi
	BackendFIFO   = "fifo"   // JSON-RPC into mi_fifo over ssh
	BackendHTTP   = "http"   // JSON-RPC to mi_http, tunneled or direct
	BackendLegacy = "legacy" // opensipsctl fifo (2.x)
)

// HostInfo is what the probe found out about a host.
type HostInfo struct {
	Backend    string // BackendCLI, BackendLegacy or "" (nothing to work with)
	Running    Version
	Binary     Version
	Server     string // MI version: "OpenSIPS (3.4.3 (x86_64/linux))"
	BinaryLine string
	CLIPath    string
	CLIVersion string
	CtlPath    string
	Commands   map[string]bool // nil = unknown
	Uptime     int64           // seconds at ProbedAt, -1 = unknown
	ProbedAt   time.Time
	Latency    time.Duration // how long the probe took
	// PID and CmdLine are those of the main OpenSIPS process (the oldest
	// one), Packages the installed opensips packages.
	PID      string
	CmdLine  string
	Packages []string
	Error    string
	Warnings []string
}

// JSON reports a backend that answers MI in JSON (3.0+): the views work.
func (h *HostInfo) JSON() bool {
	return h.Backend == BackendCLI || h.Backend == BackendFIFO || h.Backend == BackendHTTP
}

// Version prefers the running process over the installed binary.
func (h *HostInfo) Version() Version {
	if !h.Running.IsZero() {
		return h.Running
	}
	return h.Binary
}

// RestartPending: the package was upgraded but OpenSIPS was not restarted.
func (h *HostInfo) RestartPending() bool {
	return !h.Running.IsZero() && !h.Binary.IsZero() && h.Running != h.Binary
}

// MIError is an error answer from OpenSIPS.
type MIError struct {
	Code    int
	Message string
}

func (e *MIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
	}
	return e.Message
}

// Client is an OpenSIPS host: MI through opensips-cli, mi_fifo or mi_http.
type Client struct {
	Host      *config.Host
	Transport Transport
	ReadOnly  bool

	mu   sync.Mutex
	info *HostInfo
}

// NewClient builds a client.
func NewClient(c *config.Host, t Transport, readOnly bool) *Client {
	return &Client{Host: c, Transport: t, ReadOnly: readOnly || c.ReadOnly}
}

// Info returns the last probe result or nil.
func (c *Client) Info() *HostInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// ConfigPath is the -f of the running OpenSIPS, "" when it is not known
// or not given (then OpenSIPS reads the opensips.cfg of its install prefix).
func (h *HostInfo) ConfigPath() string {
	f := strings.Fields(h.CmdLine)
	for i, a := range f {
		switch {
		case a == "-f" && i+1 < len(f):
			return f[i+1]
		case strings.HasPrefix(a, "-f") && len(a) > 2:
			return a[2:]
		}
	}
	return ""
}

// UptimeNow is the uptime counted on from the probe, -1 when unknown.
func (h *HostInfo) UptimeNow() int64 {
	if h.Uptime < 0 || h.ProbedAt.IsZero() {
		return h.Uptime
	}
	return h.Uptime + int64(time.Since(h.ProbedAt)/time.Second)
}

func (c *Client) setInfo(h *HostInfo) {
	c.mu.Lock()
	c.info = h
	c.mu.Unlock()
}

func (c *Client) timeout() time.Duration { return time.Duration(c.Host.TimeoutSeconds) * time.Second }

func (c *Client) sudo(interactive bool) []string {
	if !c.Host.Sudo {
		return nil
	}
	if interactive {
		return []string{"sudo"}
	}
	return []string{"sudo", "-n"}
}

// CLIArgv is the remote command line for opensips-cli -x.
func (c *Client) CLIArgv(args ...string) []string {
	argv := append(c.sudo(false), c.Host.CLI)
	argv = append(argv, c.Host.CLIArgs...)
	if c.Host.Instance != "" {
		argv = append(argv, "-i", c.Host.Instance)
	}
	// A custom output_type in /etc/opensips-cli.cfg (yaml, lines) would break parsing
	argv = append(argv, "-o", "output_type=pretty-print", "-x")
	return append(argv, args...)
}

// InteractiveCLI is opensips-cli in its own interactive shell.
func (c *Client) InteractiveCLI() []string {
	argv := append(c.sudo(true), c.Host.CLI)
	argv = append(argv, c.Host.CLIArgs...)
	if c.Host.Instance != "" {
		argv = append(argv, "-i", c.Host.Instance)
	}
	return argv
}

func (c *Client) ctlArgv(args ...string) []string {
	ctl := "opensipsctl"
	if h := c.Info(); h != nil && h.CtlPath != "" {
		ctl = h.CtlPath
	}
	return append(append(c.sudo(false), ctl, "fifo"), args...)
}

// ParamArgs renders MI params as command-line words. Named params are k=v: opensips-cli splits
// on the first "=", so URIs like sip:a@b;transport=tcp survive.
func ParamArgs(p Params) []string {
	switch v := p.(type) {
	case nil:
		return nil
	case []string:
		return v
	case map[string]string:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			out = append(out, k+"="+v[k])
		}
		return out
	}
	return []string{fmt.Sprint(p)}
}

// Render is the command as one would type it: for confirmations and messages.
func (c *Client) Render(command string, p Params) string {
	argv := []string{"opensips-cli", "-x", "mi", command}
	if h := c.Info(); h != nil {
		switch h.Backend {
		case BackendLegacy:
			argv = []string{"opensipsctl", "fifo", command}
		case BackendFIFO, BackendHTTP:
			return "MI " + RemoteCommand(append([]string{command}, ParamArgs(p)...)) + " (via " + c.Host.Via + ")"
		}
	}
	return RemoteCommand(append(argv, ParamArgs(p)...))
}

// MI runs an MI command. The answer is decoded JSON (numbers as json.Number), except with
// opensipsctl fifo (2.x), where it is text.
func (c *Client) MI(ctx context.Context, command string, p Params) (any, error) {
	h := c.Info()
	if h == nil {
		var err error
		if h, err = c.Probe(ctx); err != nil {
			return nil, err
		}
	}
	return c.mi(ctx, h, command, p)
}

func (c *Client) mi(ctx context.Context, h *HostInfo, command string, p Params) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	switch h.Backend {
	case BackendLegacy:
		res, err := c.Transport.Run(ctx, c.ctlArgv(append([]string{command}, ParamArgs(p)...)...))
		if err != nil {
			return nil, err
		}
		if res.RC != 0 {
			return nil, miError(res)
		}
		return strings.TrimRight(res.Out, "\n"), nil
	case BackendCLI:
		res, err := c.Transport.Run(ctx, c.CLIArgv(append([]string{"mi", command}, ParamArgs(p)...)...))
		if err != nil {
			return nil, err
		}
		if res.RC != 0 {
			return nil, miError(res)
		}
		return ParseOutput(res.Out), nil
	case BackendFIFO:
		res, err := c.Transport.Run(ctx, c.fifoArgv(rpcRequest(command, p)))
		if err != nil {
			return nil, err
		}
		if res.RC != 0 {
			return nil, miError(res)
		}
		return ParseRPC([]byte(res.Out))
	case BackendHTTP:
		return c.httpCall(ctx, rpcRequest(command, p))
	}
	if h.Error != "" {
		return nil, errors.New(h.Error)
	}
	return nil, errors.New(i18n.T("no opensips-cli on the host"))
}

// Supports checks an operation against the host's version, MI commands and read-only mode.
func (c *Client) Supports(op string) Support {
	h := c.Info()
	if h == nil {
		return Support{Reason: i18n.T("host not probed yet")}
	}
	if !h.JSON() {
		if h.Error != "" {
			return Support{Reason: h.Error}
		}
		return Support{Reason: i18n.T("needs MI in JSON: OpenSIPS 3.0+")}
	}
	return Check(op, h.Version(), h.Commands, c.ReadOnly)
}

// CommandFor resolves an operation to an MI command and params for this host.
func (c *Client) CommandFor(op string, a Args) (string, Params, error) {
	s := c.Supports(op)
	if !s.OK {
		return "", nil, errors.New(s.Reason)
	}
	return s.Variant.Command, s.Variant.Params(a), nil
}

// Op runs an operation.
func (c *Client) Op(ctx context.Context, op string, a Args) (any, error) {
	cmd, p, err := c.CommandFor(op, a)
	if err != nil {
		return nil, err
	}
	return c.MI(ctx, cmd, p)
}

// Probe finds out the installed and running versions, the backend, MI commands and uptime.
func (c *Client) Probe(ctx context.Context) (*HostInfo, error) {
	start := time.Now()
	h, err := c.probe(ctx)
	if err != nil {
		return nil, err
	}
	h.Latency = time.Since(start)
	c.setInfo(h)
	return h, nil
}

func (c *Client) probeScript() string {
	cli := ShellQuote(c.Host.CLI)
	return strings.Join([]string{
		// a non-interactive ssh session often has no sbin in PATH
		`PATH="$PATH:/usr/sbin:/usr/local/sbin:/usr/local/bin"`,
		`echo "binary=$(opensips -V 2>/dev/null | head -n1)"`,
		`echo "cli=$(command -v ` + cli + `)"`,
		`echo "cli_version=$(` + cli + ` -V </dev/null 2>&1 | head -n1)"`,
		`echo "ctl=$(command -v opensipsctl)"`,
		`p=$(pgrep -o -x opensips 2>/dev/null); echo "pid=$p"`,
		`[ -n "$p" ] && echo "cmdline=$(tr '\000' ' ' </proc/$p/cmdline 2>/dev/null)"`,
		`echo "pkg=$( (dpkg-query -W -f='${Package} ${Version}\n' 'opensips' 'opensips-*' 2>/dev/null | awk 'NF==2'; rpm -qa 2>/dev/null | grep '^opensips') | head -n 12 | tr '\n' ';')"`,
	}, "; ")
}

func (c *Client) probe(ctx context.Context) (*HostInfo, error) {
	h := &HostInfo{Uptime: -1, ProbedAt: time.Now()}
	via := c.Host.Via
	if via != config.ViaDirect { // direct has no ssh to look around with
		pctx, cancel := context.WithTimeout(ctx, c.timeout())
		res, err := c.Transport.Run(pctx, []string{"sh", "-c", c.probeScript()})
		cancel()
		if err != nil {
			return nil, err
		}
		vals := parseProbe(res.Out)
		if _, ran := vals["binary"]; !ran {
			// the shell on the host did not run the probe: a forced command,
			// a restricted shell, an ssh error ssh does not mark as such
			h.Error = i18n.Tf("the probe did not run on the host (exit code %d): %s",
				res.RC, lastLine(res.Err+"\n"+res.Out, i18n.T("no output")))
			return h, nil
		}
		h.BinaryLine = vals["binary"]
		h.PID, h.CmdLine = vals["pid"], strings.TrimSpace(vals["cmdline"])
		for _, p := range strings.Split(vals["pkg"], ";") {
			if p = strings.TrimSpace(p); p != "" {
				h.Packages = append(h.Packages, p)
			}
		}
		h.Binary, _ = ParseVersion(h.BinaryLine)
		h.CLIPath, h.CtlPath = vals["cli"], vals["ctl"]
		if h.CLIPath != "" {
			h.CLIVersion = vals["cli_version"]
		}
	}
	tooOld := func(v Version) string {
		if via == config.ViaCLI {
			return i18n.Tf("OpenSIPS %s: opensips-cli needs %s+", v, MinCLIVersion.Branch())
		}
		return i18n.Tf("OpenSIPS %s: MI in JSON (via %s) needs %s+", v, via, MinCLIVersion.Branch())
	}
	if !h.Binary.IsZero() && h.Binary.Less(MinCLIVersion) {
		return c.probeLegacy(ctx, h, tooOld(h.Binary))
	}
	reach := func(err error) string { return i18n.Tf("opensips-cli cannot reach MI: %v", err) }
	switch via {
	case config.ViaFIFO:
		h.Backend = BackendFIFO
		reach = func(err error) string { return i18n.Tf("mi_fifo at %s: %v", c.Host.FIFO, err) }
	case config.ViaHTTP, config.ViaDirect:
		h.Backend = BackendHTTP
		reach = func(err error) string { return err.Error() }
	default:
		if h.CLIPath == "" {
			if h.CtlPath != "" {
				return c.probeLegacy(ctx, h, i18n.T("no opensips-cli"))
			}
			h.Error = i18n.Tf("no %s on the host", c.Host.CLI)
			if h.Binary.IsZero() {
				h.Error = i18n.Tf("no %s on the host, no opensips binary", c.Host.CLI)
			}
			return h, nil
		}
		h.Backend = BackendCLI
	}
	ans, err := c.mi(ctx, h, "version", nil)
	if err != nil {
		if IsUnreachable(err) {
			return nil, err
		}
		h.Backend = ""
		h.Error = reach(err)
		return h, nil
	}
	h.Server = str(asMap(ans)["Server"])
	if h.Server == "" {
		h.Server = str(ans)
	}
	h.Running, _ = ParseVersion(h.Server)
	if !h.Running.IsZero() && h.Running.Less(MinCLIVersion) {
		return c.probeLegacy(ctx, h, tooOld(h.Running))
	}
	var wg sync.WaitGroup
	var which, uptime any
	var whichErr error
	wg.Add(2)
	go func() { defer wg.Done(); which, whichErr = c.mi(ctx, h, "which", nil) }()
	go func() { defer wg.Done(); uptime, _ = c.mi(ctx, h, "uptime", nil) }()
	wg.Wait()
	if list := asList(which); list != nil {
		h.Commands = map[string]bool{}
		for _, x := range list {
			h.Commands[str(x)] = true
		}
	} else if whichErr != nil {
		h.Warnings = append(h.Warnings, "MI which: "+whichErr.Error())
	}
	h.Uptime = ParseUptime(uptime)
	return h, nil
}

func (c *Client) probeLegacy(ctx context.Context, h *HostInfo, why string) (*HostInfo, error) {
	h.Warnings = append(h.Warnings, why)
	if h.CtlPath == "" {
		h.Backend = ""
		h.Error = i18n.Tf("%s; no opensipsctl either", why)
		return h, nil
	}
	h.Backend = BackendLegacy
	if v, err := c.mi(ctx, h, "version", nil); err == nil {
		h.Server = strings.SplitN(str(v), "\n", 2)[0]
		if r, ok := ParseVersion(h.Server); ok {
			h.Running = r
		}
		if w, err := c.mi(ctx, h, "which", nil); err == nil {
			h.Commands = map[string]bool{}
			for _, l := range strings.Split(str(w), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					h.Commands[l] = true
				}
			}
		}
		if u, err := c.mi(ctx, h, "uptime", nil); err == nil {
			h.Uptime = ParseUptime(u)
		}
	} else if IsUnreachable(err) {
		return nil, err
	} else {
		h.Warnings = append(h.Warnings, "opensipsctl fifo: "+err.Error())
	}
	h.Error = i18n.Tf("%s: only :mi through opensipsctl fifo", why)
	return h, nil
}

// Close stops the transport.
func (c *Client) Close() { c.Transport.Close() }

func parseProbe(out string) map[string]string {
	vals := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			vals[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return vals
}

var uptimeRe = regexp.MustCompile(`(?:Up time:*\s*)?(\d+)`)

// ParseUptime reads MI uptime: {"Up time": "1234 [sec]"} in 3.x, "Up time:: 1234 [sec]" text in 2.x.
func ParseUptime(ans any) int64 {
	raw := str(asMap(ans)["Up time"])
	if raw == "" {
		raw = str(ans)
		if i := strings.Index(raw, "Up time"); i >= 0 {
			raw = raw[i:]
		} else {
			return -1
		}
	}
	m := uptimeRe.FindStringSubmatch(raw)
	if m == nil {
		return -1
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	return n
}

var errorCodeRe = regexp.MustCompile(`(-?\d{3,5})\s*[:,]?\s*(.+)$`)

func miError(res Result) *MIError {
	text := strings.TrimSpace(strings.TrimSpace(res.Err) + "\n" + strings.TrimSpace(res.Out))
	line := lastLine(text, fmt.Sprintf("exit code %d", res.RC))
	line = regexp.MustCompile(`^(ERROR|CRITICAL|WARNING)\s*:?\s*`).ReplaceAllString(line, "")
	if m := errorCodeRe.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, "{") {
		code, _ := strconv.Atoi(m[1])
		return &MIError{Code: code, Message: strings.TrimSpace(m[2])}
	}
	return &MIError{Message: line}
}

// ParseOutput decodes opensips-cli pretty-print output; a warning before the JSON is skipped.
func ParseOutput(out string) any {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	if v, ok := decodeJSON(out); ok {
		return v
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") || strings.HasPrefix(t, `"`) {
			if v, ok := decodeJSON(strings.Join(lines[i:], "\n")); ok {
				return v
			}
			break
		}
	}
	return out
}

func decodeJSON(s string) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return nil, false
	}
	return v, true
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

func num(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			f, _ := x.Float64()
			return int64(f)
		}
		return n
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	case bool:
		if x {
			return 1
		}
	}
	return 0
}
