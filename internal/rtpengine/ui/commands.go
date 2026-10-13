package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/tui"
)

func (a *App) registerCommands() {
	needHost := func(f func(args string) error) func(string) error {
		return func(args string) error {
			if a.host == nil {
				return errors.New(T("no hosts configured, see rtpengine-tui -sample-config"))
			}
			return f(args)
		}
	}
	tunableNames := []string{"maxsessions", "maxcpu", "maxload", "maxbw", "maxopenfiles", "timeout", "silenttimeout",
		"offertimeout", "finaltimeout", "deletedelay", "controltos", "loglevel", "redisallowederrors",
		"redisdisabletime", "redisconnecttimeout", "rediscmdtimeout", "redisdisable"}
	a.AddCommands([]*tui.Command{
		{Names: []string{"hosts", "host", "nodes", "node", "ctx"}, Args: "[name]", Help: N("list rtpengine machines or switch to one"), Run: func(args string) error {
			if args = strings.TrimSpace(args); args != "" {
				return a.SwitchHost(args, "sessions")
			}
			a.Replace(newHostsView(a))
			return nil
		}},
		{Names: []string{"sessions", "ss", "calls"}, Args: "[all|own|foreign]", Help: N("list sessions (list sessions)"),
			Complete: []string{"all", "own", "foreign"}, Run: needHost(func(args string) error {
				which := strings.TrimSpace(args)
				if which == "" {
					which = "all"
				}
				if which != "all" && which != "own" && which != "foreign" {
					a.Replace(newSessionsView(a, "all"))
					a.Push(newCallView(a, which))
					return nil
				}
				a.Replace(newSessionsView(a, which))
				return nil
			})},
		{Names: []string{"proxies", "proxy", "px"}, Help: N("calls grouped by signalling proxy, enter lists the calls of one"), Run: needHost(func(string) error {
			a.Replace(newProxiesView(a))
			return nil
		})},
		{Names: []string{"config", "conf", "cfg"}, Help: N("rtpengine.conf + rtpengine-recording.conf as a table, checked against the release"), Run: needHost(func(string) error {
			a.Replace(newSettingsView(a))
			return nil
		})},
		{Names: []string{"limits", "tunables", "lim"}, Help: N("runtime limits and timeouts (list/set)"), Run: needHost(func(string) error {
			a.Replace(newTunablesView(a))
			return nil
		})},
		{Names: []string{"interfaces", "if", "ports"}, Help: N("interfaces and port usage (mr7.5+)"), Run: needHost(func(string) error {
			a.Replace(newInterfacesView(a))
			return nil
		})},
		{Names: []string{"stats", "totals", "counters", "jsonstats", "json", "transcoders"}, Help: N("statistics: sessions, traffic, totals, quality, NG commands, transcoders (y JSON, s saves it)"), Run: needHost(func(string) error {
			a.Replace(newStatsPanel(a))
			return nil
		})},
		{Names: []string{"params"}, Args: "[current|start|diff]", Help: N("runtime configuration (params)"), Complete: []string{"current", "start", "diff"},
			Run: needHost(func(args string) error {
				which := strings.TrimSpace(args)
				if which == "" {
					which = "current"
				}
				a.Replace(newKVView(a, "params", N("Params"), []string{N("NAME"), N("VALUE")}, func(ctx context.Context, c *rtpengine.Client) ([]rtpengine.KV, error) {
					kvs, raw, err := c.Params(ctx, which)
					for i := range kvs {
						kvs[i].Value = rtpengine.MaskSecret(strings.ReplaceAll(kvs[i].Key, "_", "-"), kvs[i].Value)
					}
					if which == "diff" {
						kvs = nil
						for _, l := range strings.Split(raw, "\n") {
							if k, v, ok := strings.Cut(l, ": "); ok {
								kvs = append(kvs, rtpengine.KV{Key: k, Value: v})
							}
						}
					}
					return kvs, err
				}))
				return nil
			})},
		{Names: []string{"keyspaces", "ks"}, Help: N("subscribed redis keyspaces (kslist)"), Run: needHost(func(string) error {
			a.Replace(newKVView(a, "keyspaces", N("Keyspaces"), []string{"#", N("KEYSPACE")}, func(ctx context.Context, c *rtpengine.Client) ([]rtpengine.KV, error) {
				out, err := c.Do(ctx, "kslist")
				var kvs []rtpengine.KV
				for i, k := range rtpengine.ParseKeyspaces(out) {
					kvs = append(kvs, rtpengine.KV{Key: fmt.Sprint(i), Value: k})
				}
				return kvs, err
			}))
			return nil
		})},
		{Names: []string{"set"}, Args: "<name> <value>", Help: N("change a runtime value (asks to confirm)"), Complete: tunableNames, Run: needHost(func(args string) error {
			f := strings.Fields(args)
			if len(f) < 2 {
				return fmt.Errorf(T("usage: set <name> <value>, names: %s"), strings.Join(tunableNames, " "))
			}
			name, value := f[0], strings.Join(f[1:], " ")
			c := a.Client()
			if err := c.Info().Caps.Check("set "+name, c.Info().Version); err != nil {
				return err
			}
			a.Confirm(Tf("set %s %s on %s?\n\nThe change is not written to rtpengine.conf and is lost on restart.", name, value, a.host.Name), T("Set"), func() {
				a.Async(func(ctx context.Context) func() {
					var out string
					var err error
					if name == "loglevel" { // rtpengine itself checks neither range nor subsystem
						sub, level, perr := rtpengine.ParseSetLogLevel(value)
						if out, err = "", perr; err == nil {
							out, err = c.SetLogLevel(ctx, sub, level)
						}
					} else {
						out, err = c.Set(ctx, name, value)
					}
					return func() {
						if err != nil {
							a.Flash().Err(err)
							return
						}
						a.Flash().Info(out)
						if v := a.Top(); v != nil {
							v.Refresh()
						}
					}
				})
			})
			return nil
		})},
		{Names: []string{"terminate"}, Args: "all|own|foreign|<call-id>", Help: N("terminate calls (asks to confirm)"), Complete: []string{"all", "own", "foreign"},
			Run: needHost(func(args string) error {
				what := strings.TrimSpace(args)
				if what == "" {
					return errors.New(T("usage: terminate all|own|foreign|<call-id>"))
				}
				text := Tf("terminate %s on %s?", what, a.host.Name)
				if what == "all" || what == "own" || what == "foreign" {
					text = Tf("Terminate ALL %s calls on %s?\nMedia stops immediately, SIP dialogs stay up until the endpoints notice.", what, a.host.Name)
				}
				a.Confirm(text, T("Terminate"), func() {
					c := a.Client()
					a.Async(func(ctx context.Context) func() {
						out, err := c.Terminate(ctx, what)
						return func() {
							if err != nil {
								a.Flash().Err(err)
								return
							}
							a.Flash().Info(strings.TrimSpace(out))
							if v := a.Top(); v != nil {
								v.Refresh()
							}
						}
					})
				})
				return nil
			})},
		{Names: []string{"active"}, Help: N("set all calls to owned (mr8.5+, asks to confirm)"), Run: needHost(func(string) error { return a.setActive(true) })},
		{Names: []string{"standby"}, Help: N("set all calls to foreign (mr8.5+, asks to confirm)"), Run: needHost(func(string) error { return a.setActive(false) })},
		{Names: []string{"revert"}, Help: N("params revert: runtime config back to startup values (asks to confirm)"), Run: needHost(func(string) error {
			c := a.Client()
			a.Confirm(Tf("params revert on %s?\nEvery value changed with set goes back to its startup value.", a.host.Name), T("Revert"), func() {
				a.Async(func(ctx context.Context) func() {
					_, err := c.Do(ctx, "params revert")
					return func() {
						if err != nil {
							a.Flash().Err(err)
							return
						}
						a.Flash().Info(T("params reverted"))
					}
				})
			})
			return nil
		})},
		{Names: []string{"cli"}, Args: "<command>", Help: N("run any rtpengine CLI command, e.g. :cli list maxsessions"), Run: needHost(func(args string) error {
			if strings.TrimSpace(args) == "" {
				return errors.New(T("usage: cli <command>"))
			}
			a.Push(newCLIView(a, strings.TrimSpace(args)))
			return nil
		})},
		{Names: []string{"describe", "info", "loglevel", "loglevels"}, Help: N("version, process, config paths, log levels and CLI commands of the host"), Run: needHost(func(string) error {
			a.Push(newDescribeView(a))
			return nil
		})},
	}...)
}

func (a *App) setActive(active bool) error {
	c := a.Client()
	cmd := "standby"
	text, button := T("Set ALL calls on %s to foreign (standby)?\nThis host stops owning its calls; use it only when another node takes over."), T("Standby")
	if active {
		cmd = "active"
		text, button = T("Set ALL calls on %s to owned (active)?\nThis host takes over the calls it has from redis."), T("Active")
	}
	if err := c.Info().Caps.Check(cmd, c.Info().Version); err != nil {
		return err
	}
	a.Confirm(fmt.Sprintf(text, a.host.Name), button, func() {
		a.Async(func(ctx context.Context) func() {
			out, err := c.SetActive(ctx, active)
			return func() {
				if err != nil {
					a.Flash().Err(err)
					return
				}
				a.Flash().Info(strings.TrimSpace(out))
			}
		})
	})
	return nil
}

// SwitchHost makes name the current host and opens a view on it.
func (a *App) SwitchHost(name, view string) error {
	h, ok := a.cfg.Find(name)
	if !ok {
		return fmt.Errorf(T("host %q not found in %s"), name, a.cfg.Path())
	}
	a.host = h
	a.cfg.CurrentHost = name
	a.UpdateInfo()
	return a.RunCommand(view)
}
