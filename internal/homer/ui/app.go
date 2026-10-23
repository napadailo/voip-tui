// Package ui implements the homer-tui terminal UI on top of the tui
// framework.
package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/internal/homer/config"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// Version is set at build time.
var Version = "dev"

var logo = []string{
	" _                               ",
	"| |__   ___  _ __ ___   ___ _ __ ",
	"| '_ \\ / _ \\| '_ ` _ \\ / _ \\ '__|",
	"| | | | (_) | | | | | |  __/ |   ",
	"|_| |_|\\___/|_| |_| |_|\\___|_|   ",
}

// hostState is a Homer instance: its client and what the probe found
// (login, profile, database nodes, search profiles).
type hostState struct {
	client  *homer.Client
	meta    meta
	probing bool
	probed  bool
	err     string   // login or client error
	after   []func() // run on the UI goroutine when the running probe ends
}

type meta struct {
	profile  *homer.Profile
	nodes    []homer.Node
	mappings []homer.Mapping
	warnings []string // what failed besides the login
	// down is set when neither nodes nor mappings could be read: with a
	// token there is no login request, so this is how a dead host shows
	down error
}

// App is the homer-tui application: the tui frame plus the Homer hosts.
type App struct {
	*tui.App

	cfg    *config.Config
	host   *config.Host
	client *homer.Client // of the current host
	loc    *time.Location

	mu    sync.RWMutex
	hosts map[string]*hostState
	flow  flowOpts // of the call views
	// formFields are the sets of the search form ctrl-a and ctrl-d changed, by profile
	formFields map[string][]string
	// transport builds the client of a host; tests point it at the emulator
	transport func(*config.Host) (*homer.Client, error)
}

// New creates the app. Clients are built here and log in in the
// background (Start), so a host that is down does not stop the others.
func New(cfg *config.Config, host *config.Host) *App {
	a := &App{
		App: tui.New(tui.Options{
			Name:        "homer-tui",
			Description: N("homer-tui - terminal UI over the Homer 7 API (homer-app /api/v3)"),
			Logo:        logo,
			DownloadDir: cfg.DownloadDir,
			Refresh:     time.Duration(cfg.RefreshSeconds) * time.Second,
			Timeout:     90 * time.Second,
		}),
		cfg:        cfg,
		hosts:      map[string]*hostState{},
		transport:  NewClient,
		flow:       flowOpts{byAlias: cfg.Flow.ByAlias, fold: cfg.Flow.Unfold != nil && !*cfg.Flow.Unfold, qosChart: cfg.Flow.QoSGraphs, hide: map[string]bool{}},
		formFields: map[string][]string{},
	}
	for _, k := range cfg.Flow.Hide {
		a.flow.hide[k] = true
	}
	for i := range cfg.Hosts {
		h := &cfg.Hosts[i]
		st := &hostState{}
		if c, err := a.transport(h); err != nil {
			st.err = err.Error()
		} else {
			st.client = c
		}
		a.hosts[h.Name] = st
	}
	a.setHost(host)
	a.Info = a.infoLines
	a.Help = a.helpSections
	a.OnLanguage = func(lang string) { a.cfg.Language = lang }
	a.OnTheme = func(name string) { a.cfg.Theme = name }
	a.Completions = func() []string {
		var names []string
		for _, m := range a.Mappings() {
			names = append(names, "search "+m.Key())
		}
		for _, h := range a.cfg.Hosts {
			names = append(names, "host "+h.Name)
		}
		return names
	}
	// a profile key (1_call, 5_default…) opens its search, a host name
	// switches to the host
	a.Unknown = func(name, args string) (bool, error) {
		if a.Mapping(name) != nil {
			return true, a.openSearch(name, args)
		}
		if _, ok := a.cfg.Find(name); ok {
			return true, a.SwitchHost(name)
		}
		return false, nil
	}
	a.registerCommands()
	return a
}

// setHost makes h current; its client and time zone follow.
func (a *App) setHost(h *config.Host) {
	a.host = h
	a.client = nil
	a.loc = time.Local
	if h == nil {
		return
	}
	a.cfg.CurrentHost = h.Name
	if st := a.hosts[h.Name]; st != nil {
		a.client = st.client
	}
	if h.Timezone != "" {
		if l, err := time.LoadLocation(h.Timezone); err == nil {
			a.loc = l
		}
	}
}

// Config returns the configuration.
func (a *App) Config() *config.Config { return a.cfg }

// Client returns the API client of the current host.
func (a *App) Client() *homer.Client { return a.client }

// Loc returns the display time zone.
func (a *App) Loc() *time.Location { return a.loc }

// state is the state of the current host.
func (a *App) state() *hostState {
	if a.host == nil {
		return nil
	}
	return a.hosts[a.host.Name]
}

// HostState returns the probe status of a host: probing, its error.
func (a *App) HostState(name string) (probing bool, err string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if st := a.hosts[name]; st != nil {
		return st.probing, st.err
	}
	return false, ""
}

func fetchMeta(ctx context.Context, c *homer.Client) meta {
	var m meta
	if p, err := c.Profile(ctx); err == nil {
		m.profile = p
	} else if c.Username != "" {
		m.warnings = append(m.warnings, Tf("profile: %v", err))
	}
	n, nerr := c.Nodes(ctx)
	if nerr == nil {
		m.nodes = n
	} else {
		m.warnings = append(m.warnings, Tf("nodes: %v", nerr))
	}
	ms, merr := c.Mappings(ctx)
	if merr == nil {
		m.mappings = ms
	} else {
		m.warnings = append(m.warnings, Tf("mappings: %v", merr))
	}
	if nerr != nil && merr != nil {
		m.down = nerr
	}
	return m
}

// ProbeAll logs into every host in the background.
func (a *App) ProbeAll() {
	for _, h := range a.cfg.Hosts {
		a.probe(h.Name, nil)
	}
}

// Probe logs into a host again in the background.
func (a *App) Probe(name string) { a.probe(name, nil) }

// probe logs into a host and reads its profile, nodes and mappings, then
// runs after on the UI goroutine; when a probe is already running, after
// waits for that one.
func (a *App) probe(name string, after func()) {
	a.mu.Lock()
	st := a.hosts[name]
	if st == nil {
		a.mu.Unlock()
		return
	}
	if after != nil {
		st.after = append(st.after, after)
	}
	if st.probing || st.client == nil {
		if st.client == nil { // no client: the error is already known
			st.probed = true
			afters := st.after
			st.after = nil
			a.mu.Unlock()
			for _, f := range afters {
				f()
			}
			return
		}
		a.mu.Unlock()
		return
	}
	st.probing = true
	c := st.client
	a.mu.Unlock()
	go func() {
		ctx, cancel := a.Ctx()
		defer cancel()
		var m meta
		_, err := c.Login(ctx)
		if err == nil {
			m = fetchMeta(ctx, c)
		}
		a.mu.Lock()
		st.probing, st.probed, st.err = false, true, ""
		switch {
		case err != nil:
			st.err = err.Error()
		case m.down != nil:
			st.err = m.down.Error()
		default:
			st.meta = m
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

// whenReady opens a view once the current host answered: right away when
// it did, otherwise after a "connecting" screen the view then replaces; a
// host that failed shows why. push puts it over the current view (esc
// comes back), else it replaces it.
func (a *App) whenReady(push bool, open func(show func(View)) error) error {
	if a.host == nil {
		return errors.New(T("no Homer host selected, see :hosts"))
	}
	name := a.host.Name
	a.mu.RLock()
	st := a.hosts[name]
	ready := st.probed && !st.probing && st.err == ""
	a.mu.RUnlock()
	show := a.Replace
	if push {
		show = a.Push
	}
	if ready {
		return open(show)
	}
	wait := newTextViewer(a, Tf("connecting to %s", name), "")
	wait.SetContent(Tf("connecting to %s (%s)…", name, a.host.URL), "")
	show(wait)
	a.probe(name, func() {
		if a.host == nil || a.host.Name != name {
			return // switched away meanwhile
		}
		if _, err := a.HostState(name); err != "" {
			wait.SetContent(Tf("%s: %s", name, err), "")
			a.Flash().Errf("%s: %s", name, err)
			return
		}
		if err := open(a.Replace); err != nil {
			a.Flash().Err(err)
		}
	})
	return nil
}

// NodeValues returns all database node values of the current host.
func (a *App) NodeValues() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []string
	if st := a.state(); st != nil {
		for _, n := range st.meta.nodes {
			out = append(out, n.Value)
		}
	}
	if len(out) == 0 {
		out = []string{"localnode"}
	}
	return out
}

// Mappings returns the protocol profiles of the current host.
func (a *App) Mappings() []homer.Mapping {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if st := a.state(); st != nil {
		return st.meta.mappings
	}
	return nil
}

// Mapping returns the mapping by key (e.g. 1_call).
func (a *App) Mapping(key string) *homer.Mapping {
	for _, m := range a.Mappings() {
		if m.Key() == key {
			return &m
		}
	}
	return nil
}

// profile is the profile of the current host's user, nil when unknown.
func (a *App) profile() *homer.Profile {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if st := a.state(); st != nil {
		return st.meta.profile
	}
	return nil
}

// IsAdmin reports admin rights of the current user (unknown = true, server decides).
func (a *App) IsAdmin() bool {
	p := a.profile()
	return p == nil || p.Admin
}

// Start logs into the hosts and runs the UI, as rtpengine-tui: one host
// opens its calls, several start on the host list.
func (a *App) Start(initial string) error {
	a.ProbeAll()
	if initial == "" {
		initial = "calls"
		if len(a.cfg.Hosts) > 1 {
			initial = "hosts"
		}
	}
	return a.App.Start(initial, "hosts")
}

// userLabel is the user and its role: "admin (admin)", "token".
func userLabel(c *homer.Client, p *homer.Profile) string {
	user := c.Username
	if user == "" {
		user = "token"
	}
	if p == nil {
		return user
	}
	role := p.Group
	if p.Admin && !strings.Contains(role, "admin") {
		role = strings.TrimPrefix(role+",admin", ",")
	}
	if role == "" {
		return user
	}
	return user + " (" + role + ")"
}

// infoLines fills the header, as in rtpengine-tui and opensips-tui: the
// host, the user, the database nodes, how homer-tui signs in, the build.
func (a *App) infoLines() [][2]string {
	if a.host == nil {
		return [][2]string{{"Host", T("none")}, {"Config", tview.Escape(tui.ShortPath(a.cfg.Path()))}}
	}
	h := a.host
	probing, failed := a.HostState(h.Name)
	host := tview.Escape(h.Name) + theme.Tag(theme.Dim) + " " + tview.Escape(h.URL)
	user, nodes := theme.Tag(theme.Dim)+"-", theme.Tag(theme.Dim)+"-"
	switch {
	case failed != "":
		user = theme.Tag(theme.Err) + tview.Escape(tui.Trunc(failed, 60))
	case probing:
		user = theme.Tag(theme.Dim) + T("probing…")
	case a.client != nil:
		user = tview.Escape(userLabel(a.client, a.profile()))
		if v := a.NodeValues(); len(v) > 0 {
			nodes = tview.Escape(strings.Join(v, ", "))
		}
	}
	auth := h.Auth()
	if h.Insecure {
		auth += theme.Tag(theme.Warn) + " · " + T("TLS not verified")
	}
	if a.cfg.Exposed() {
		auth += theme.Tag(theme.Warn) + " · " + tview.Escape(Tf("others can read the password in %s: chmod 600", a.cfg.Path()))
	}
	return [][2]string{
		{"Host", host},
		{"User", user},
		{"Nodes", nodes},
		{"Auth", auth},
		{"Rev", Version + theme.Tag(theme.Dim) + " · " + theme.Current() + " · " + i18n.Lang()},
	}
}
