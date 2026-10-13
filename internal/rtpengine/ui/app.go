// Package ui implements the rtpengine-tui terminal UI on top of the tui
// framework: hosts, sessions and call details, config check, limits,
// interfaces, proxies.
package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/internal/rtpengine/config"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// Version is set at build time.
var Version = "dev"

var logo = []string{
	"     _                       _          ",
	" _ _| |_ _ __  ___ _ _  __ _(_)_ _  ___ ",
	"| '_|  _| '_ \\/ -_) ' \\/ _` | | ' \\/ -_)",
	"|_|  \\__| .__/\\___|_||_\\__, |_|_||_\\___|",
	"        |_|            |___/            ",
}

// App is the rtpengine-tui application: the tui frame plus the hosts.
type App struct {
	*tui.App

	cfg     *config.Config
	host    *config.Host
	clients map[string]*rtpengine.Client
}

// New creates the app. host is the initially selected rtpengine machine.
func New(cfg *config.Config, host *config.Host) *App {
	a := &App{
		App: tui.New(tui.Options{
			Name:        "rtpengine-tui",
			Description: N("rtpengine-tui - terminal UI over rtpengine's listen-cli (rtpengine-ctl) over SSH"),
			Logo:        logo,
			Refresh:     time.Duration(cfg.RefreshSeconds) * time.Second,
			DownloadDir: cfg.DownloadDir,
		}),
		cfg:     cfg,
		host:    host,
		clients: map[string]*rtpengine.Client{},
	}
	for i := range cfg.Hosts {
		h := &cfg.Hosts[i]
		a.clients[h.Name] = rtpengine.New(cfg, h)
	}
	a.Info = a.infoLines
	a.Help = helpSections
	a.Completions = func() []string {
		var names []string
		for _, h := range a.cfg.Hosts {
			names = append(names, "host "+h.Name)
		}
		return names
	}
	a.Unknown = func(name, _ string) (bool, error) {
		if _, ok := a.cfg.Find(name); ok {
			return true, a.SwitchHost(name, "sessions")
		}
		return false, nil
	}
	a.OnTheme = func(name string) { a.cfg.Theme = name }
	a.OnLanguage = func(lang string) { a.cfg.Language = lang }
	a.registerCommands()
	return a
}

// Config returns the configuration.
func (a *App) Config() *config.Config { return a.cfg }

// Host returns the current host.
func (a *App) Host() *config.Host { return a.host }

// Client returns the client of the current host.
func (a *App) Client() *rtpengine.Client {
	if a.host == nil {
		return nil
	}
	return a.clients[a.host.Name]
}

// ClientFor returns the client of a host.
func (a *App) ClientFor(name string) *rtpengine.Client { return a.clients[name] }

// Start runs the UI: sessions of a single host, the host list otherwise.
func (a *App) Start(initial string) error {
	if initial == "" {
		initial = "sessions"
		if len(a.cfg.Hosts) > 1 {
			initial = "hosts"
		}
	}
	return a.App.Start(initial, "hosts")
}

// ensureProbed connects to the current host in the background if needed.
func (a *App) ensureProbed(then func()) {
	c := a.Client()
	if c.Probed() {
		then()
		return
	}
	a.Flash().Infof("connecting to %s…", a.host.Name)
	a.Async(func(ctx context.Context) func() {
		_, err := c.Probe(ctx)
		return func() {
			a.UpdateInfo()
			if err != nil {
				a.Flash().Errf("%s: %v", a.host.Name, err)
			}
			then()
		}
	})
}

// infoLines fills the header: host, version, CLI transport, process.
func (a *App) infoLines() [][2]string {
	c := a.Client()
	if c == nil {
		return [][2]string{{"Host", T("none")}, {"Config", tview.Escape(tui.ShortPath(a.cfg.Path()))}}
	}
	i := c.Info()
	ver := theme.Tag(theme.Dim) + T("probing…")
	switch {
	case i.Version.Known():
		ver = tview.Escape(i.Version.String()) + theme.Tag(theme.Dim) + " (" + tview.Escape(i.Version.Source) + ")"
	case !i.ProbedAt.IsZero():
		ver = theme.Tag(theme.Warn) + T("unknown")
	}
	cli := tview.Escape(i.CLI)
	if i.CLIErr != nil {
		cli = theme.Tag(theme.Err) + tview.Escape(tui.Trunc(i.CLIErr.Error(), 40))
	}
	proc := theme.Tag(theme.Dim) + T("n/a")
	switch {
	case i.PID != "":
		proc = "pid " + i.PID
		if len(i.Kernel) > 0 {
			proc += theme.Tag(theme.OK) + " kernel"
		} else {
			proc += theme.Tag(theme.Warn) + " userspace"
		}
		if i.RecPID != "" {
			proc += theme.Tag(theme.InfoValue) + " +recording"
		}
	case c.Shell != nil && a.host.Via != config.ViaDirect && !i.ProbedAt.IsZero():
		proc = theme.Tag(theme.Err) + T("not running")
	}
	return [][2]string{
		{"Host", tview.Escape(a.host.Name) + theme.Tag(theme.Dim) + " " + tview.Escape(c.Target())},
		{"Version", ver},
		{"CLI", cli},
		{"Process", proc},
		{"Rev", Version + theme.Tag(theme.Dim) + " · " + theme.Current() + " · " + i18n.Lang()},
	}
}

// Shell suspends the UI and opens an interactive shell on the current host.
func (a *App) Shell() {
	c := a.Client()
	if c.Shell == nil {
		a.Flash().Warn(T("no shell access to this host (via: direct)"))
		return
	}
	cmd := c.Shell.Interactive()
	var err error
	a.Suspend(func() {
		fmt.Printf("\nrtpengine-tui: %s\n\n", Tf("%s (exit the shell to come back)", strings.Join(cmd.Args, " ")))
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		err = cmd.Run()
	})
	if err != nil {
		a.Flash().Warnf("shell: %v", err)
	}
}
