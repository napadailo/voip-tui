package rtpengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/napadailo/voip-tui/i18n"
)

// Shell runs POSIX shell commands on an rtpengine machine.
type Shell interface {
	Run(ctx context.Context, cmd string) (string, error)
	Target() string
	// Interactive returns a command opening a login shell.
	Interactive() *exec.Cmd
}

// SSH runs commands through the system OpenSSH client, so ~/.ssh/config
// (aliases, ProxyJump, agent, known_hosts) applies exactly as in a terminal.
type SSH struct {
	Dest           string
	User           string
	Port           int
	IdentityFile   string
	Options        []string
	ConnectTimeout time.Duration
	Multiplex      bool
	Sudo           bool
}

// Target returns user@host[:port].
func (s *SSH) Target() string {
	t := s.Dest
	if s.User != "" && !strings.Contains(t, "@") {
		t = s.User + "@" + t
	}
	if s.Port != 0 && s.Port != 22 {
		t += ":" + strconv.Itoa(s.Port)
	}
	return t
}

func (s *SSH) args(batch bool) []string {
	var a []string
	if batch {
		a = append(a, "-o", "BatchMode=yes")
	}
	if s.ConnectTimeout > 0 {
		a = append(a, "-o", "ConnectTimeout="+strconv.Itoa(int(s.ConnectTimeout.Seconds())))
	}
	if s.Multiplex {
		if dir := controlDir(); dir != "" {
			a = append(a, "-o", "ControlMaster=auto", "-o", "ControlPath="+filepath.Join(dir, "%C"), "-o", "ControlPersist=10m")
		}
	}
	if s.Port != 0 {
		a = append(a, "-p", strconv.Itoa(s.Port))
	}
	if s.User != "" && !strings.Contains(s.Dest, "@") {
		a = append(a, "-l", s.User)
	}
	if s.IdentityFile != "" {
		a = append(a, "-i", expandHome(s.IdentityFile))
	}
	return append(a, s.Options...)
}

// Run executes cmd on the host.
func (s *SSH) Run(ctx context.Context, cmd string) (string, error) {
	if s.Sudo {
		cmd = "sudo -n sh -c " + Quote(cmd)
	}
	argv := append(s.args(true), s.Dest, "--", cmd)
	return runCmd(exec.CommandContext(ctx, "ssh", argv...), true)
}

// Interactive opens a login shell.
func (s *SSH) Interactive() *exec.Cmd {
	return exec.Command("ssh", append(s.args(false), "-t", s.Dest)...)
}

// Dial opens a direct-tcpip channel to addr as seen from the host (ssh -W).
func (s *SSH) Dial(ctx context.Context, addr string) (io.ReadWriteCloser, error) {
	argv := append(s.args(true), "-W", addr, s.Dest)
	c := exec.CommandContext(ctx, "ssh", argv...)
	return startPipe(c)
}

// Local runs commands on this machine (address: local).
type Local struct{ Sudo bool }

// Target returns "local".
func (l *Local) Target() string { return "local" }

// Run executes cmd with sh -c.
func (l *Local) Run(ctx context.Context, cmd string) (string, error) {
	if l.Sudo {
		cmd = "sudo -n sh -c " + Quote(cmd)
	}
	return runCmd(exec.CommandContext(ctx, "sh", "-c", cmd), false)
}

// Interactive opens $SHELL.
func (l *Local) Interactive() *exec.Cmd {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "sh"
	}
	return exec.Command(sh)
}

func runCmd(c *exec.Cmd, ssh bool) (string, error) {
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(errb.String())
		if ssh && ee.ExitCode() == 255 {
			return out.String(), fmt.Errorf("ssh: %s", lastLine(msg))
		}
		stderr := msg
		if msg == "" {
			msg = lastLine(strings.TrimSpace(out.String()))
		}
		return out.String(), &ExitError{Code: ee.ExitCode(), Stderr: stderr, msg: firstLine(msg)}
	}
	return out.String(), err
}

// ExitError is a command that ran and exited non-zero.
type ExitError struct {
	Code   int
	Stderr string // trimmed, may be empty
	msg    string
}

func (e *ExitError) Error() string { return fmt.Sprintf(i18n.T("exit %d: %s"), e.Code, e.msg) }

// pipeConn adapts a child process to io.ReadWriteCloser.
type pipeConn struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out io.ReadCloser
	err *bytes.Buffer
}

func startPipe(c *exec.Cmd) (*pipeConn, error) {
	in, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &pipeConn{cmd: c, in: in, out: out, err: &bytes.Buffer{}}
	c.Stderr = p.err
	c.WaitDelay = pipeCloseGrace // a child left behind would keep stderr open and Wait waiting
	if err := c.Start(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *pipeConn) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.in.Write(b) }

// pipeCloseGrace is how long Close waits for the process after closing its
// stdin: ssh -W exits then, a process that does not is killed.
const pipeCloseGrace = 2 * time.Second

func (p *pipeConn) Close() error {
	p.in.Close()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(pipeCloseGrace):
		_ = p.cmd.Process.Kill()
		<-done
		return nil
	}
	if err != nil && p.err.Len() > 0 {
		return fmt.Errorf("ssh -W: %s", lastLine(strings.TrimSpace(p.err.String())))
	}
	return nil
}

// CLI sends one command to rtpengine's listen-cli and returns the reply.
type CLI interface {
	Do(ctx context.Context, cmd string) (string, error)
	// Describe tells how the CLI is reached, for the UI.
	Describe() string
}

// listen-cli takes one line per connection and closes after the reply.
func exchange(ctx context.Context, rw io.ReadWriteCloser, cmd string) (string, error) {
	return roundTrip(ctx, rw, cmd+"\n")
}

// roundTrip writes a request and reads until the peer closes the connection.
func roundTrip(ctx context.Context, rw io.ReadWriteCloser, req string) (string, error) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			rw.Close()
		case <-done:
		}
	}()
	if _, err := io.WriteString(rw, req); err != nil {
		rw.Close()
		return "", err
	}
	b, rerr := io.ReadAll(rw)
	cerr := rw.Close()
	if ctx.Err() != nil {
		return string(b), ctx.Err()
	}
	if rerr != nil {
		return string(b), rerr
	}
	if len(b) == 0 && cerr != nil {
		return "", cerr
	}
	return string(b), nil
}

// DirectCLI connects to listen-cli over TCP from this machine.
type DirectCLI struct{ Addr string }

// Do runs a command.
func (d *DirectCLI) Do(ctx context.Context, cmd string) (string, error) {
	var dl net.Dialer
	c, err := dl.DialContext(ctx, "tcp", d.Addr)
	if err != nil {
		return "", fmt.Errorf("listen-cli %s: %w", d.Addr, err)
	}
	return exchange(ctx, c, cmd)
}

// Describe says how the CLI is reached.
func (d *DirectCLI) Describe() string { return "tcp " + d.Addr }

// TunnelCLI reaches listen-cli through an SSH channel; nothing has to be
// installed on the host and the CLI may stay bound to localhost.
type TunnelCLI struct {
	SSH  *SSH
	Addr string
}

// Do runs a command.
func (t *TunnelCLI) Do(ctx context.Context, cmd string) (string, error) {
	c, err := t.SSH.Dial(ctx, t.Addr)
	if err != nil {
		return "", err
	}
	return exchange(ctx, c, cmd)
}

// Describe says how the CLI is reached.
func (t *TunnelCLI) Describe() string { return "ssh -W " + t.Addr }

// CtlCLI runs rtpengine-ctl on the host.
type CtlCLI struct {
	Shell Shell
	Bin   string
	// Addr is passed as -ip/-port; empty lets rtpengine-ctl read listen-cli
	// from /etc/rtpengine/rtpengine.conf (newer releases) or use 127.0.0.1:9900.
	Addr string
}

// Do runs a command.
func (c *CtlCLI) Do(ctx context.Context, cmd string) (string, error) {
	argv := []string{c.Bin}
	if host, port, err := net.SplitHostPort(c.Addr); err == nil {
		argv = append(argv, "-ip", host, "-port", port)
	}
	argv = append(argv, strings.Fields(cmd)...)
	return c.Shell.Run(ctx, pathFix+Join(argv...))
}

// Describe says how the CLI is reached.
func (c *CtlCLI) Describe() string {
	if c.Addr == "" {
		return c.Bin
	}
	return c.Bin + " " + c.Addr
}

// pathFix exposes sbin tools in non-interactive ssh sessions (Debian gives
// a PATH without /usr/sbin there).
const pathFix = `PATH="$PATH:/usr/local/sbin:/usr/sbin:/sbin"; export PATH; `

// Quote returns s as one POSIX shell word.
func Quote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+%", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Join quotes argv into a command line.
func Join(argv ...string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = Quote(a)
	}
	return strings.Join(q, " ")
}

// controlDir returns a directory for ControlMaster sockets, or "" to run
// without multiplexing.
//
// Unix socket paths are limited to 104 bytes on macOS (108 on Linux), and ssh
// binds the master to "<ControlPath>.<16 random chars>" before renaming it;
// %C is a 40 char hash. ~/Library/Caches does not fit, so a short per-user
// directory under /tmp is used.
func controlDir() string {
	dir := fmt.Sprintf("/tmp/rtpengine-tui-%d", os.Getuid())
	if !controlPathFits(dir) {
		return ""
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return ""
	}
	// /tmp is shared: refuse a symlink or a directory someone else owns.
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 || !ownedByMe(st) {
		return ""
	}
	return dir
}

// controlPathFits reports whether dir/%C plus ssh's temporary suffix stays
// within the smallest sun_path (104 bytes, macOS, including the NUL).
func controlPathFits(dir string) bool {
	return len(dir)+1+40+17 < 104
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
