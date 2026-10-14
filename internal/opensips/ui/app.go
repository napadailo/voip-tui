// Package ui implements the opensips-tui terminal UI on top of the tui
// framework.
package ui

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/internal/opensips/config"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// Version is set at build time.
var Version = "dev"

// hostState is the probe status of a host.
type hostState struct {
	probing bool
	err     string   // unreachable
	after   []func() // run on the UI goroutine when the running probe ends
}

var logo = []string{
	"                                 _",
	"  ___  _ __   ___ _ __  ___  ___(_)_ __  ___",
	" / _ \\| '_ \\ / _ \\ '_ \\/ __|/ __| | '_ \\/ __|",
	"| (_) | |_) |  __/ | | \\__ \\\\__ \\ | |_) \\__ \\",
	" \\___/| .__/ \\___|_| |_|___/|___/_| .__/|___/",
	"      |_|                         |_|",
}

// App is the opensips-tui application: the tui frame plus the hosts.
type App struct {
	*tui.App

	cfg      *config.Config
	readOnly bool
	clients  map[string]*opensips.Client
	host     *config.Host

	mu    sync.RWMutex
	hosts map[string]*hostState
}

// New creates the app. host may be nil: the app starts on the hosts list.
// transport reaches a host; nil is ssh (tests pass the emulator).
func New(cfg *config.Config, host *config.Host, readOnly bool, transport func(*config.Host) opensips.Transport) *App {
	if transport == nil {
		transport = func(h *config.Host) opensips.Transport { return opensips.NewSSH(h, cfg.SSH) }
	}
	a := &App{
		App: tui.New(tui.Options{
			Name:        "opensips-tui",
			Description: N("opensips-tui - terminal UI over the OpenSIPS MI (opensips-cli) over ssh"),
			Logo:        logo,
			Refresh:     time.Duration(cfg.RefreshSeconds) * time.Second,
			Timeout:     60 * time.Second,
		}),
		cfg:      cfg,
		readOnly: readOnly || cfg.ReadOnly,
		clients:  map[string]*opensips.Client{},
		hosts:    map[string]*hostState{},
		host:     host,
	}
	for i := range cfg.Hosts {
		c := &cfg.Hosts[i]
		a.clients[c.Name] = opensips.NewClient(c, transport(c), a.readOnly)
		a.hosts[c.Name] = &hostState{}
	}
	a.Info = a.infoLines
	a.OnLanguage = func(lang string) { a.cfg.Language = lang }
	a.OnTheme = func(name string) { a.cfg.Theme = name }
	// :<host name> switches to the host
	a.Unknown = func(name, _ string) (bool, error) {
		if _, ok := a.cfg.Find(name); ok {
			return true, a.SwitchHost(name)
		}
		return false, nil
	}
	a.Help = a.helpSections
	a.OnTick = a.UpdateInfo
	a.Completions = func() []string {
		var names []string
		for _, c := range a.cfg.Hosts {
			names = append(names, "host "+c.Name)
		}
		if cl := a.Client(); cl != nil && cl.Info() != nil {
			for cmd := range cl.Info().Commands {
				names = append(names, "mi "+cmd)
			}
		}
		return names
	}
	a.registerCommands()
	return a
}

// Config returns the configuration.
func (a *App) Config() *config.Config { return a.cfg }

// Client returns the client of the current host.
func (a *App) Client() *opensips.Client {
	if a.host == nil {
		return nil
	}
	return a.clients[a.host.Name]
}

// Start probes hosts and runs the UI: the default view of the current
// host, the hosts list without one.
func (a *App) Start(initial string) error {
	a.ProbeAll()
	if initial == "" {
		initial = a.cfg.DefaultView
		if len(a.cfg.Hosts) > 1 {
			initial = "hosts"
		}
	}
	return a.App.Start(initial, "hosts")
}

// ProbeAll probes every host in the background.
func (a *App) ProbeAll() {
	for name := range a.clients {
		a.Probe(name)
	}
}

// Probe re-probes one host in the background.
func (a *App) Probe(name string) { a.probe(name, nil) }

// probe probes a host in the background and then runs after on the UI
// goroutine; when a probe is already running, after waits for that one.
func (a *App) probe(name string, after func()) {
	a.mu.Lock()
	st := a.hosts[name]
	if after != nil {
		st.after = append(st.after, after)
	}
	if st.probing {
		a.mu.Unlock()
		return
	}
	st.probing = true
	a.mu.Unlock()
	go func() {
		ctx, cancel := a.Ctx()
		defer cancel()
		_, err := a.clients[name].Probe(ctx)
		a.mu.Lock()
		st.probing = false
		st.err = ""
		if err != nil {
			st.err = err.Error()
		}
		afters := st.after
		st.after = nil
		a.mu.Unlock()
		a.QueueUpdateDraw(func() {
			if v, ok := a.Top().(*HostsView); ok {
				v.Refresh()
			}
			a.UpdateInfo()
			for _, f := range afters {
				f()
			}
		})
	}()
}

// HostState returns probe status: probing, unreachable error.
func (a *App) HostState(name string) (bool, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	st := a.hosts[name]
	return st.probing, st.err
}

// ensureProbed probes synchronously if needed (call off the UI goroutine).
func (a *App) ensureProbed(ctx context.Context, c *opensips.Client) error {
	if c.Info() != nil {
		return nil
	}
	_, err := c.Probe(ctx)
	a.mu.Lock()
	if st := a.hosts[c.Host.Name]; st != nil {
		st.err = ""
		if err != nil {
			st.err = err.Error()
		}
	}
	a.mu.Unlock()
	return err
}

// infoLines fills the header: host, ssh target, OpenSIPS version, uptime, refresh.
func (a *App) infoLines() [][2]string {
	c := a.Client()
	if c == nil {
		return [][2]string{{"Host", T("none")}, {"Config", tview.Escape(tui.ShortPath(a.cfg.Path()))}}
	}
	h, i := a.host, c.Info()
	probing, unreachable := a.HostState(h.Name)
	host := tview.Escape(h.Name) + theme.Tag(theme.Dim) + " " + tview.Escape(address(h))
	if c.ReadOnly {
		host += theme.Tag(theme.Warn) + " · " + T("read-only")
	}
	ver, mi, proc := theme.Tag(theme.Dim)+"-", theme.Tag(theme.Dim)+"-", theme.Tag(theme.Dim)+T("n/a")
	switch {
	case unreachable != "":
		ver = theme.Tag(theme.Err) + tview.Escape(tui.Trunc(unreachable, 60))
	case i == nil && probing:
		ver = theme.Tag(theme.Dim) + T("probing…")
	case i != nil:
		switch {
		case i.Running.IsZero():
			ver = theme.Tag(theme.Warn) + T("unknown")
		case i.RestartPending():
			ver = theme.Tag(theme.Warn) + tview.Escape(Tf("%s ⚠ installed %s", i.Running, i.Binary))
		default:
			ver = tview.Escape(i.Running.String())
		}
		if l := miLabel(h, i.Backend); l != "" {
			mi = tview.Escape(l)
		} else if i.Error != "" {
			mi = theme.Tag(theme.Err) + tview.Escape(tui.Trunc(i.Error, 60))
		}
		// as in rtpengine-tui: the pid, then how long it runs
		var parts []string
		if i.PID != "" {
			parts = append(parts, "pid "+tview.Escape(i.PID))
		}
		if up := i.UptimeNow(); up >= 0 {
			parts = append(parts, tview.Escape(Tf("up %s", opensips.Duration(up))))
		}
		if len(parts) > 0 {
			proc = parts[0]
			if len(parts) > 1 {
				proc += theme.Tag(theme.Dim) + " · " + parts[1]
			}
		}
	}
	return [][2]string{
		{"Host", host},
		{"Version", ver},
		{"MI", mi},
		{"Process", proc},
		{"Rev", Version + theme.Tag(theme.Dim) + " · " + theme.Current() + " · " + i18n.Lang()},
	}
}

// Shell suspends the UI and runs an interactive ssh session (cli = opensips-cli shell).
func (a *App) Shell(name string, cli bool) {
	c, ok := a.clients[name]
	if !ok {
		return
	}
	var remote []string
	if cli {
		remote = c.InteractiveCLI()
	}
	argv := c.Transport.Interactive(remote)
	if len(argv) == 0 {
		a.Flash().Warnf("%s: no ssh session for this host", name)
		return
	}
	var runErr error
	a.Suspend(func() {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		runErr = cmd.Run()
	})
	if runErr != nil {
		a.Flash().Errf("%s: %v", name, runErr)
	}
}

// Close stops ssh master connections.
func (a *App) Close() {
	for _, c := range a.clients {
		c.Close()
	}
}

// levelColor maps an OpenSIPS state level to a theme color.
func levelColor(l opensips.Level) tcell.Color {
	switch l {
	case opensips.LevelOK:
		return theme.OK
	case opensips.LevelWarn:
		return theme.Warn
	case opensips.LevelErr:
		return theme.Err
	case opensips.LevelDim:
		return theme.Dim
	case opensips.LevelAccent:
		return theme.Accent
	}
	return 0
}
