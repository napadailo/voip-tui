package opensips

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/napadailo/voip-tui/internal/opensips/config"
)

// Result of a remote command.
type Result struct {
	RC  int
	Out string
	Err string
}

// Transport runs commands on a host.
type Transport interface {
	Run(ctx context.Context, argv []string) (Result, error)
	// Interactive returns a local command that runs argv on the host with a tty (nil argv = login shell).
	// Empty result: not possible for this transport.
	Interactive(argv []string) []string
	Close()
}

// Unreachable means the host could not be reached: ssh, network, timeout.
type Unreachable struct{ Msg string }

func (e *Unreachable) Error() string { return e.Msg }

// IsUnreachable reports an Unreachable error.
func IsUnreachable(err error) bool {
	var u *Unreachable
	return errors.As(err, &u)
}

// ssh itself exits with 255 on connection errors, but opensips-cli may exit 255 on an MI error
// too (sys.exit(-1)). Tell them apart by stderr.
var sshFailure = regexp.MustCompile(`(?m)^ssh: |Permission denied \(|Connection (refused|timed out|closed by)|` +
	`Could not resolve|Host key verification failed|No route to host|Network is unreachable|` +
	`kex_exchange_identification|Control socket connect|mux_client`)

var safeShellArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// ShellQuote quotes s for a POSIX shell.
func ShellQuote(s string) string {
	if s != "" && safeShellArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// RemoteCommand joins argv for the remote shell: ssh glues its arguments with spaces, so quote them here.
func RemoteCommand(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = ShellQuote(a)
	}
	return strings.Join(q, " ")
}

// SSH runs commands through the system ssh with connection multiplexing, so ~/.ssh/config,
// ProxyJump, the agent and keys all work, and every opensips-cli call reuses one TCP connection.
// BatchMode=yes: without a usable key ssh fails at once instead of asking for a password.
type SSH struct {
	command string
	target  string
	options []string
	dir     string
}

// ControlDir keeps the ControlMaster sockets; "" runs ssh without
// multiplexing.
//
// Unix socket paths are limited to 104 bytes on macOS (108 on Linux), and ssh
// binds the master to "<ControlPath>.<16 random chars>" before renaming it;
// %C is a 40 char hash. ~/Library/Caches does not fit, so a short per-user
// directory under /tmp is used.
func ControlDir() string {
	dir := fmt.Sprintf("/tmp/opensips-tui-%d", os.Getuid())
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

// NewSSH builds the transport for a host.
func NewSSH(c *config.Host, s config.SSHConfig) *SSH {
	dir := ControlDir()
	opts := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(s.ConnectTimeoutSeconds),
		"-o", "ServerAliveInterval=15",
	}
	if dir != "" {
		opts = append(opts, "-o", "ControlMaster=auto", "-o", "ControlPath="+filepath.Join(dir, "%C"),
			"-o", "ControlPersist="+strconv.Itoa(s.ControlPersistSeconds))
	}
	if c.Port > 0 {
		opts = append(opts, "-p", strconv.Itoa(c.Port))
	}
	if c.User != "" {
		opts = append(opts, "-l", c.User)
	}
	for _, o := range c.SSHOptions {
		opts = append(opts, "-o", o)
	}
	return &SSH{command: s.Command, target: c.Target(), options: opts, dir: dir}
}

// Argv is the local command line for a remote argv.
func (s *SSH) Argv(remote []string, tty bool) []string {
	argv := append([]string{}, s.options...)
	if tty {
		argv = append(argv, "-t")
	}
	argv = append(argv, "--", s.target)
	if len(remote) > 0 {
		argv = append(argv, RemoteCommand(remote))
	}
	return argv
}

// Run executes a remote command.
func (s *SSH) Run(ctx context.Context, remote []string) (Result, error) {
	cmd := exec.CommandContext(ctx, s.command, s.Argv(remote, false)...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return Result{}, &Unreachable{Msg: "no answer: " + ctx.Err().Error()}
	}
	res := Result{Out: out.String(), Err: errb.String()}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		res.RC = exitErr.ExitCode()
	case err != nil:
		return res, &Unreachable{Msg: fmt.Sprintf("%s: %v", s.command, err)}
	}
	if res.RC == 255 && sshFailure.MatchString(res.Err) {
		return res, &Unreachable{Msg: lastLine(res.Err, "ssh failed")}
	}
	return res, nil
}

// Interactive returns ssh -t for the remote command.
func (s *SSH) Interactive(remote []string) []string {
	return append([]string{s.command}, s.Argv(remote, true)...)
}

// Close stops the master connection.
func (s *SSH) Close() {
	if s.dir == "" {
		return // no master connection
	}
	argv := append(append([]string{}, s.options...), "-O", "exit", "--", s.target)
	_ = exec.Command(s.command, argv...).Run()
}

func lastLine(s, fallback string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return fallback
}
