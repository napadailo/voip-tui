// Package rtpengine talks to rtpengine machines: the CLI (listen-cli, the
// protocol behind rtpengine-ctl) for runtime state and SSH for version
// detection and config files.
package rtpengine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/rtpengine/config"
)

// Info is what probing a host found out.
type Info struct {
	Version    Version
	VersionErr error
	Caps       Caps
	CLI        string // how the CLI is reached
	CLIErr     error  // CLI unreachable

	PID          string
	CmdLine      string
	RecPID       string
	RecCmdLine   string
	Packages     []string
	Kernel       []string // loaded kernel forwarding modules
	ConfigPath   string
	Section      string
	RecConfPath  string
	RecSection   string
	Conf         *ConfFile
	ConfErr      error
	ProbedAt     time.Time
	ProbeLatency time.Duration
}

// Running reports whether an rtpengine process was found (unknown without a shell).
func (i Info) Running() bool { return i.PID != "" }

// Client is one rtpengine host.
type Client struct {
	Host  *config.Host
	Shell Shell // nil for "via: direct" to a remote host

	mu   sync.Mutex
	cli  CLI
	info Info
}

// New builds a client for a configured host.
func New(cfg *config.Config, h *config.Host) *Client {
	c := &Client{Host: h}
	switch {
	case h.Address == "local":
		c.Shell = &Local{Sudo: h.Sudo}
	case h.Via != config.ViaDirect:
		c.Shell = c.ssh(cfg)
	}
	return c
}

func (c *Client) ssh(cfg *config.Config) *SSH {
	h := c.Host
	user := h.User
	if user == "" {
		user = cfg.SSH.User
	}
	key := h.IdentityFile
	if key == "" {
		key = cfg.SSH.IdentityFile
	}
	return &SSH{
		Dest: h.Address, User: user, Port: h.Port, IdentityFile: key,
		Options:        append([]string(nil), cfg.SSH.Options...),
		ConnectTimeout: time.Duration(cfg.SSH.ConnectTimeout) * time.Second,
		Multiplex:      cfg.SSH.Multiplex == nil || *cfg.SSH.Multiplex,
		Sudo:           h.Sudo,
	}
}

// Target is where the client connects, for display.
func (c *Client) Target() string {
	if c.Shell != nil && c.Host.Via != config.ViaDirect {
		return c.Shell.Target()
	}
	return c.Host.CLI
}

// Info returns the last probe result.
func (c *Client) Info() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Probed reports whether Probe ran at least once.
func (c *Client) Probed() bool { return !c.Info().ProbedAt.IsZero() }

var cfgFlagRe = regexp.MustCompile(`--config-(file|section)[= ](\S+)`)

// Probe detects version, process, config files and CLI commands.
func (c *Client) Probe(ctx context.Context) (Info, error) {
	start := time.Now()
	var info Info
	if c.Shell != nil {
		if err := c.probeShell(ctx, &info); err != nil {
			info.ProbedAt = time.Now()
			c.setInfo(info)
			return info, err
		}
	}
	if c.Host.Version != "" && c.Host.Version != "auto" {
		v, err := ParseVersion(c.Host.Version)
		v.Source = "config"
		info.Version, info.VersionErr = v, err
	}

	addr := c.Host.CLI
	if addr == "" && info.Conf != nil {
		if v, ok := info.Conf.Get(info.Section, "listen-cli"); ok {
			addr = ListenCLI(v)
		}
	}
	if addr == "" && c.Host.Via != config.ViaCtl {
		addr = "127.0.0.1:9900"
	}
	cli := c.newCLI(addr)
	info.CLI = cli.Describe()
	info.Caps, info.CLIErr = c.probeCaps(ctx, cli, info.Version)
	if !info.Version.Known() && info.CLIErr == nil {
		if fl, ok := info.Caps.Floor(); ok {
			info.Version = fl
		}
	}
	info.ProbedAt = time.Now()
	info.ProbeLatency = time.Since(start)
	c.mu.Lock()
	c.cli = cli
	c.info = info
	c.mu.Unlock()
	return info, info.CLIErr
}

func (c *Client) setInfo(i Info) {
	c.mu.Lock()
	c.info = i
	c.mu.Unlock()
}

func (c *Client) newCLI(addr string) CLI {
	switch {
	case c.Host.Via == config.ViaCtl:
		return &CtlCLI{Shell: c.Shell, Bin: c.Host.CtlBin, Addr: addr}
	case c.Host.Via == config.ViaTunnel:
		if s, ok := c.Shell.(*SSH); ok {
			return &TunnelCLI{SSH: s, Addr: addr}
		}
	}
	return &DirectCLI{Addr: addr}
}

func (c *Client) probeShell(ctx context.Context, info *Info) error {
	script := pathFix + strings.Join([]string{
		`echo @@version; (rtpengine --version 2>&1 || true) | head -n 2`,
		`echo @@pkg; (dpkg-query -W -f='${Package} ${Version}\n' 'ngcp-rtpengine*' 'rtpengine*' 2>/dev/null | awk 'NF==2'; rpm -qa 2>/dev/null | grep -i rtpengine) | head -n 12`,
		`echo @@proc; for p in $(pgrep -x rtpengine 2>/dev/null | head -n 1); do echo "$p $(tr '\0' ' ' < /proc/$p/cmdline 2>/dev/null)"; done`,
		`echo @@rec; for p in $(pgrep -x rtpengine-recording 2>/dev/null | head -n 1); do echo "$p $(tr '\0' ' ' < /proc/$p/cmdline 2>/dev/null)"; done`,
		`echo @@kernel; (lsmod 2>/dev/null | awk '$1 ~ /RTPENGINE|rtpengine/ {print $1}'; [ -e /proc/rtpengine/control ] && echo proc) | head -n 3`,
		`exit 0`,
	}, "\n")
	out, err := c.Shell.Run(ctx, script)
	if err != nil {
		return err
	}
	sec := sections(out)
	if v, err := ParseVersion(strings.Join(sec["version"], " ")); err == nil {
		v.Source = "rtpengine --version"
		info.Version = v
	}
	info.Packages = sec["pkg"]
	if !info.Version.Known() {
		for _, p := range info.Packages {
			if strings.Contains(p, "rtpengine-daemon") || strings.HasPrefix(p, "ngcp-rtpengine ") || strings.HasPrefix(p, "rtpengine ") {
				if v, err := ParseVersion(strings.Fields(p + " x")[1]); err == nil {
					v.Source = "package " + strings.Fields(p)[0]
					info.Version = v
					break
				}
			}
		}
	}
	if !info.Version.Known() {
		info.VersionErr = errors.New(i18n.T("rtpengine --version and the package database gave no version"))
	}
	info.Kernel = sec["kernel"]
	info.PID, info.CmdLine = splitProc(sec["proc"])
	info.RecPID, info.RecCmdLine = splitProc(sec["rec"])
	info.ConfigPath, info.Section = confFromCmdline(info.CmdLine, c.Host.ConfigFile, DaemonRTPEngine)
	info.RecConfPath, info.RecSection = confFromCmdline(info.RecCmdLine, c.Host.RecordingConfigFile, DaemonRecording)
	if text, err := c.ReadFile(ctx, info.ConfigPath); err == nil {
		info.Conf, info.ConfErr = ParseConf(info.ConfigPath, text)
	} else {
		info.ConfErr = err
	}
	return nil
}

func sections(out string) map[string][]string {
	sec := map[string][]string{}
	cur := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "@@") {
			cur = l[2:]
			continue
		}
		if l != "" {
			sec[cur] = append(sec[cur], l)
		}
	}
	return sec
}

func splitProc(lines []string) (pid, cmdline string) {
	if len(lines) == 0 {
		return "", ""
	}
	pid, cmdline, _ = strings.Cut(lines[0], " ")
	return pid, strings.TrimSpace(cmdline)
}

// confFromCmdline honours --config-file / --config-section of the running
// daemon, then the host config, then the package default.
func confFromCmdline(cmdline, configured string, d Daemon) (path, section string) {
	path, section = configured, d.String()
	for _, m := range cfgFlagRe.FindAllStringSubmatch(cmdline, -1) {
		if m[1] == "file" && configured == "" {
			path = m[2]
		} else if m[1] == "section" {
			section = m[2]
		}
	}
	if path == "" {
		path = d.DefaultPath()
	}
	return path, section
}

// probeCaps asks the CLI for its commands. `help` exists since mr9.5; for
// older releases the release table is used.
func (c *Client) probeCaps(ctx context.Context, cli CLI, v Version) (Caps, error) {
	top, err := cli.Do(ctx, "help")
	if err != nil {
		return CapsForVersion(v), err
	}
	if isUnknown(top) || strings.TrimSpace(top) == "" {
		caps := CapsForVersion(v)
		if !v.Known() {
			// pre-mr9.5 without a version: probe the read-only additions
			for _, p := range []string{"list interfaces", "list jsonstats"} {
				if out, err := cli.Do(ctx, p); err == nil && !isUnknown(out) {
					caps.cmds[p] = true
					if p == "list jsonstats" {
						caps.cmds["active"], caps.cmds["standby"], caps.cmds["list transcoders"] = true, true, true
					}
				}
			}
		}
		return caps, nil
	}
	list, _ := cli.Do(ctx, "list help")
	set, _ := cli.Do(ctx, "set help")
	return capsFromHelp(parseHelp(top), parseHelp(list), parseHelp(set)), nil
}

// ReadFile returns a file from the host.
func (c *Client) ReadFile(ctx context.Context, path string) (string, error) {
	if c.Shell == nil {
		return "", errors.New(i18n.T("no shell access (via: direct): set address to an ssh host or local"))
	}
	return c.Shell.Run(ctx, "cat "+Quote(path))
}

// CLI returns the transport, probing first if needed.
func (c *Client) CLI(ctx context.Context) (CLI, error) {
	c.mu.Lock()
	cli := c.cli
	c.mu.Unlock()
	if cli != nil {
		return cli, nil
	}
	if _, err := c.Probe(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cli, nil
}

// Do runs a raw CLI command.
func (c *Client) Do(ctx context.Context, cmd string) (string, error) {
	cli, err := c.CLI(ctx)
	if err != nil {
		return "", err
	}
	out, err := cli.Do(ctx, cmd)
	if err != nil {
		return out, err
	}
	if isUnknown(out) {
		return out, fmt.Errorf("rtpengine %s: %s", c.Info().Version, strings.TrimSpace(out))
	}
	return out, nil
}

// need checks a command against the host capabilities.
func (c *Client) need(path string) error {
	i := c.Info()
	return i.Caps.Check(path, i.Version)
}

// Has reports whether the host supports a CLI command.
func (c *Client) Has(path string) bool { return c.Info().Caps.Has(path) }

// NumSessions runs `list numsessions`.
func (c *Client) NumSessions(ctx context.Context) (NumSessions, error) {
	out, err := c.Do(ctx, "list numsessions")
	return ParseNumSessions(out), err
}

// Sessions runs `list sessions all|own|foreign`.
func (c *Client) Sessions(ctx context.Context, which string) ([]Session, error) {
	if which == "" {
		which = "all"
	}
	out, err := c.Do(ctx, "list sessions "+which)
	if err != nil {
		return nil, err
	}
	return ParseSessions(out), nil
}

// SessionInfo returns call details: `call <id> info` on mr10.5+, else `list sessions <id>`.
func (c *Client) SessionInfo(ctx context.Context, callid string) (string, error) {
	if c.Has("call") {
		return c.Do(ctx, "call "+callid+" info")
	}
	return c.Do(ctx, "list sessions "+callid)
}

// Terminate ends one call (by Call-ID) or all/own/foreign calls.
func (c *Client) Terminate(ctx context.Context, what string) (string, error) {
	return c.Do(ctx, "terminate "+what)
}

// Debug toggles per-call debug logging (mr9.5+).
func (c *Client) Debug(ctx context.Context, callid string, on bool) (string, error) {
	if err := c.need("debug"); err != nil {
		return "", err
	}
	flag := "off"
	if on {
		flag = "on"
	}
	return c.Do(ctx, "debug "+callid+" "+flag)
}

// DefaultLogCommand reads a call's lines from journald: rtpengine logs with
// the identifier "rtpengine" both through syslog and on stderr under systemd.
const DefaultLogCommand = "journalctl -t rtpengine --since {since} --no-pager -o short-iso-precise | grep -F -- {callid}"

// LogCommand is the host's command for the log of a call from since on
// (unix seconds; 0: the last day).
func (c *Client) LogCommand(callid string, since int64) string {
	cmd := c.Host.LogCommand
	if cmd == "" {
		cmd = DefaultLogCommand
	}
	from := "-24h"
	if since > 0 {
		from = "@" + strconv.FormatInt(since, 10)
	}
	return strings.NewReplacer("{callid}", Quote(callid), "{since}", from).Replace(cmd)
}

// CallLog returns rtpengine's log lines about a call, the last limit of
// them (0: all), and how many there are; "" and 0 if there are none.
func (c *Client) CallLog(ctx context.Context, callid string, since int64, limit int) (string, int, error) {
	if c.Shell == nil {
		return "", 0, errors.New(i18n.T("no shell access (via: direct): set address to an ssh host or local"))
	}
	out, err := c.Shell.Run(ctx, c.LogCommand(callid, since))
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == 1 && strings.TrimSpace(out) == "" {
		// grep found nothing; why journalctl gave it nothing, if it said so, is on stderr
		if ee.Stderr != "" {
			return "", 0, errors.New(firstLine(ee.Stderr))
		}
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "", 0, nil
	}
	lines := strings.Split(out, "\n")
	total := len(lines)
	if limit > 0 && total > limit {
		lines = lines[total-limit:]
	}
	return strings.Join(lines, "\n"), total, nil
}

// SetActive switches all calls to owned (active) or foreign (standby), mr8.5+.
func (c *Client) SetActive(ctx context.Context, active bool) (string, error) {
	cmd := "standby"
	if active {
		cmd = "active"
	}
	if err := c.need(cmd); err != nil {
		return "", err
	}
	return c.Do(ctx, cmd)
}

// Text runs a read-only command whose output is shown as is.
func (c *Client) Text(ctx context.Context, path string) (string, error) {
	if err := c.need(path); err != nil {
		return "", err
	}
	return c.Do(ctx, path)
}

// Params runs `params start|current|diff`.
func (c *Client) Params(ctx context.Context, which string) ([]KV, string, error) {
	out, err := c.Do(ctx, "params "+which)
	return ParseParams(out), out, err
}

// Tunable is a runtime value readable with `list X` and settable with `set X`.
type Tunable struct {
	Name     string // CLI name, e.g. maxsessions
	Option   string // rtpengine.conf option
	Unit     string
	Value    string
	Err      error
	Settable bool
}

var tunables = []struct{ name, option, unit string }{
	{"maxsessions", "max-sessions", i18n.N("sessions, -1 = unlimited")},
	{"maxcpu", "max-cpu", i18n.N("% CPU, 0 = off")},
	{"maxload", "max-load", i18n.N("load average, 0 = off")},
	{"maxbw", "max-bandwidth", i18n.N("bytes/s, 0 = off")},
	{"maxopenfiles", "", i18n.N("open files (RLIMIT_NOFILE)")},
	{"timeout", "timeout", i18n.N("s without RTP")},
	{"silenttimeout", "silent-timeout", i18n.N("s of one-way/silent RTP")},
	{"offertimeout", "offer-timeout", i18n.N("s without answer")},
	{"finaltimeout", "final-timeout", i18n.N("s call lifetime, 0 = off")},
	{"deletedelay", "delete-delay", i18n.N("s before a deleted call is freed")},
	{"controltos", "control-tos", i18n.N("TOS of NG replies")},
	{"redisallowederrors", "redis-allowed-errors", i18n.N("errors before redis is disabled")},
	{"redisdisabletime", "redis-disable-time", i18n.N("s redis stays disabled")},
	{"redisconnecttimeout", "redis-connect-timeout", i18n.N("ms")},
	{"rediscmdtimeout", "redis-cmd-timeout", i18n.N("ms")},
}

// Tunables reads all runtime limits the host supports, concurrently.
func (c *Client) Tunables(ctx context.Context) []Tunable {
	var (
		out []Tunable
		wg  sync.WaitGroup
		mu  sync.Mutex
	)
	for _, t := range tunables {
		if !c.Has("list " + t.name) {
			continue
		}
		t := t
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := c.Do(ctx, "list "+t.name)
			tu := Tunable{Name: t.name, Option: t.option, Unit: t.unit, Value: ParseValue(raw), Err: err, Settable: c.Has("set " + t.name)}
			mu.Lock()
			out = append(out, tu)
			mu.Unlock()
		}()
	}
	wg.Wait()
	order := map[string]int{}
	for i, t := range tunables {
		order[t.name] = i
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i].Name] < order[out[j].Name] })
	return out
}

// Set runs `set <name> <value>` and returns rtpengine's reply.
func (c *Client) Set(ctx context.Context, name, value string) (string, error) {
	if err := c.need("set " + name); err != nil {
		return "", err
	}
	out, err := c.Do(ctx, "set "+name+" "+value)
	if err != nil {
		return out, err
	}
	t := strings.TrimSpace(out)
	if strings.HasPrefix(t, "Fail") || strings.HasPrefix(t, "Invalid") {
		return out, errors.New(t)
	}
	return t, nil
}

// Settings parses both config files of the host and compares them with the
// running configuration.
func (c *Client) Settings(ctx context.Context) ([]Setting, []error) {
	info := c.Info()
	var errs []error
	var runtime map[string]string
	if params, _, err := c.Params(ctx, "current"); err == nil {
		runtime = RuntimeOptions(params)
	}
	var out []Setting
	for _, f := range []struct {
		d       Daemon
		path    string
		section string
		rt      map[string]string
	}{
		{DaemonRTPEngine, info.ConfigPath, info.Section, runtime},
		{DaemonRecording, info.RecConfPath, info.RecSection, nil},
	} {
		if f.path == "" {
			f.path = f.d.DefaultPath()
		}
		text, err := c.ReadFile(ctx, f.path)
		if err != nil {
			// recording is optional: stay quiet unless it runs or is configured
			if f.d == DaemonRecording && info.RecPID == "" && c.Host.RecordingConfigFile == "" {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", f.path, err))
			continue
		}
		cf, err := ParseConf(f.path, text)
		if err != nil {
			errs = append(errs, err)
		}
		out = append(out, Analyze(cf, f.d, f.section, info.Version, f.rt)...)
	}
	return out, errs
}
