package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/internal/homer/config"
	"github.com/napadailo/voip-tui/tui"
)

func (a *App) registerCommands() {
	search := func(key string) func(string) error {
		return func(args string) error { return a.openSearch(key, args) }
	}
	cmds := []*tui.Command{
		{Names: []string{"calls", "call", "c"}, Args: "[field=value…] [since=1h] [limit=N] [where <expr>]",
			Help: N("search SIP calls (1_call)"), Run: search("1_call")},
		{Names: []string{"registrations", "reg", "r"}, Args: "[filters]", Help: N("search SIP registrations (1_registration)"), Run: search("1_registration")},
		{Names: []string{"sip", "default", "msgs"}, Args: "[filters]", Help: N("search other SIP messages (1_default)"), Run: search("1_default")},
		{Names: []string{"logs", "log"}, Args: "[filters]", Help: N("search HEP logs (100_default)"), Run: search("100_default")},
		{Names: []string{"search", "s"}, Args: "<hepid_profile> [filters]", Help: N("search any profile, e.g. :search 5_default"), Run: func(args string) error {
			key, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
			if key == "" {
				return fmt.Errorf(T("usage: search <hepid_profile> [filters]; profiles: %s"), strings.Join(a.profileKeys(), ", "))
			}
			return a.openSearch(key, rest)
		}},
		{Names: []string{"hosts", "host", "ctx"}, Args: "[name]", Help: N("list Homer hosts or switch to one (keeps the current view)"), Run: func(args string) error {
			if args = strings.TrimSpace(args); args != "" {
				return a.SwitchHost(args)
			}
			a.Replace(newHostsView(a))
			return nil
		}},
		{Names: []string{"import"}, Args: "<file.pcap>", Help: N("upload a PCAP into Homer (original timestamps)"), Run: func(args string) error {
			return a.importPCAP(args, false)
		}},
		{Names: []string{"import-now"}, Args: "<file.pcap>", Help: N("upload a PCAP shifting timestamps to now"), Run: func(args string) error {
			return a.importPCAP(args, true)
		}},
		{Names: []string{"info", "describe", "me", "about"}, Help: N("the host card: user, database nodes, modules, auth types, search profiles"), Run: func(string) error {
			return a.whenReady(false, func(show func(View)) error {
				show(newDescribeView(a))
				return nil
			})
		}},
	}
	for _, r := range homer.Resources {
		r := r
		cmds = append(cmds, &tui.Command{
			Names: append([]string{r.Name}, r.Aliases...),
			Help:  r.Help,
			Run: func(string) error {
				return a.whenReady(false, func(show func(View)) error {
					show(newResourceView(a, r))
					return nil
				})
			},
		})
	}
	a.AddCommands(cmds...)
}

func (a *App) profileKeys() []string {
	var keys []string
	for _, m := range a.Mappings() {
		keys = append(keys, m.Key())
	}
	return keys
}

func (a *App) openSearch(key, args string) error {
	return a.whenReady(false, func(show func(View)) error { return a.search(key, args, show) })
}

// search opens a search of the current host with show.
func (a *App) search(key, args string, show func(View)) error {
	m := a.Mapping(key)
	if m == nil && len(a.Mappings()) > 0 {
		return fmt.Errorf(T("unknown profile %q; available: %s"), key, strings.Join(a.profileKeys(), ", "))
	}
	since, err := homer.ParseDuration(a.cfg.DefaultSince)
	if err != nil {
		since = time.Hour
	}
	base := homer.SearchSpec{Key: key, Since: since, Limit: a.cfg.DefaultLimit}
	spec, err := homer.ParseSearchArgs(args, base, a.loc, time.Now())
	if err != nil {
		return err
	}
	show(newSearchView(a, spec))
	return nil
}

func (a *App) importPCAP(file string, now bool) error {
	file = config.ExpandHome(strings.TrimSpace(file))
	if file == "" {
		return errors.New(T("usage: import <file.pcap>"))
	}
	if _, err := os.Stat(file); err != nil {
		return err
	}
	a.Flash().Infof("uploading %s…", file)
	a.Async(func(ctx context.Context) func() {
		msg, err := a.client.ImportPCAP(ctx, file, now)
		return func() {
			if err != nil && strings.Contains(err.Error(), "bad pcap data") {
				// homer-app imports only through tshark and reports every failure this way
				a.Flash().Errf("%v: the server imports PCAP via tshark, enable decoder_shark in webapp_config.json and install tshark on the homer-app host", err)
				return
			}
			if err != nil {
				a.Flash().Err(err)
				return
			}
			a.Flash().Infof("import: %s", msg)
		}
	})
	return nil
}

// SwitchHost makes another Homer host current and opens the same view
// there: the same search, the same settings, the host card, else calls.
func (a *App) SwitchHost(name string) error {
	h, ok := a.cfg.Find(name)
	if !ok {
		return fmt.Errorf(T("host %q not found in %s"), name, a.cfg.Path())
	}
	reopen := func(show func(View)) error { return a.search("1_call", "", show) }
	switch v := a.Top().(type) {
	case *SearchView:
		spec := v.spec
		reopen = func(show func(View)) error {
			show(newSearchView(a, spec))
			return nil
		}
	case *ResourceView:
		res := v.res
		reopen = func(show func(View)) error {
			show(newResourceView(a, res))
			return nil
		}
	case *TextViewer:
		if strings.HasPrefix(v.Name(), describePrefix) {
			reopen = func(show func(View)) error {
				show(newDescribeView(a))
				return nil
			}
		}
	}
	a.setHost(h)
	a.UpdateInfo()
	return a.whenReady(false, reopen)
}

// NewClient builds an API client for a host.
func NewClient(c *config.Host) (*homer.Client, error) {
	pass, err := c.ResolvePassword()
	if err != nil {
		return nil, err
	}
	token, err := c.ResolveToken()
	if err != nil {
		return nil, err
	}
	return homer.New(homer.Options{
		URL: c.URL, APIPrefix: c.APIPrefix, Username: c.Username, Password: pass,
		AuthToken: token, TokenHeader: c.TokenHeader, Insecure: c.Insecure,
	}), nil
}

func prettyJSON(data []byte) string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}
