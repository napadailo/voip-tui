package ui

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// describePrefix starts the name of the host card, so that switching hosts
// keeps it open.
const describePrefix = "info "

// hostExtra is what the card reads besides the probe: modules and the
// ways to sign in.
type hostExtra struct {
	loaded       bool
	modules      map[string]any
	modulesErr   error
	authTypes    map[string]any
	authTypesErr error
}

// newDescribeView is the card of the current host, as describe in
// rtpengine-tui and :info in opensips-tui. y switches it to JSON and back.
func newDescribeView(app *App) View {
	h, c := app.host, app.client
	v := newTextViewer(app, describePrefix+h.Name, "markup")
	var ex hostExtra
	showRaw := false
	raw := func() any {
		return map[string]any{"profile": app.profile(), "nodes": app.NodeValues(), "modules": ex.modules,
			"auth_types": ex.authTypes, "mappings": len(app.Mappings())}
	}
	render := func() {
		if showRaw {
			v.SetContent(toJSON(raw()), "json")
			return
		}
		v.SetContent(describeCard(app, ex).String(), "markup")
	}
	v.Loader = func() {
		app.Async(func(ctx context.Context) func() {
			var n hostExtra
			n.modules, n.modulesErr = dataMap(ctx, c, "/modules/status")
			n.authTypes, n.authTypesErr = dataMap(ctx, c, "/auth/type/list")
			n.loaded = true
			return func() {
				ex = n
				render()
			}
		})
	}
	v.Keys['y'] = func() {
		showRaw = !showRaw
		render()
		v.Text().ScrollToBeginning()
	}
	v.Extra = []Hint{{Key: "y", Desc: N("JSON/parsed")}}
	render()
	v.Loader()
	return v
}

// dataMap reads {"data": {...}} of a GET.
func dataMap(ctx context.Context, c *homer.Client, path string) (map[string]any, error) {
	body, err := c.Do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

func describeCard(app *App, ex hostExtra) *tui.Card {
	h := app.host
	auth := Tf("auth %s", h.Auth())
	card := tui.NewCard(h.Name).Sub(h.URL, auth, h.Labels.String())

	card.Section(N("Homer"))
	card.KVRaw("URL", tview.Escape(h.URL))
	if h.APIPrefix != "" {
		card.KV(N("API prefix"), tview.Escape(h.APIPrefix))
	}
	if _, failed := app.HostState(h.Name); failed != "" {
		card.KV(N("status"), tui.Paint(theme.Err, failed))
	}
	if app.client != nil {
		card.KV(N("user"), tview.Escape(userLabel(app.client, app.profile())))
	}
	if p := app.profile(); p != nil {
		if p.DisplayName != "" {
			card.KV(N("name"), tview.Escape(p.DisplayName))
		}
		admin := tui.Faint(T("no"))
		if p.Admin {
			admin = tui.Strong(T("yes"))
		}
		card.KV(N("administrator"), admin)
		if p.ExternalAuth {
			card.KV(N("external auth"), T("yes"))
		}
	}
	tls := T("verified")
	if h.Insecure {
		tls = tui.Paint(theme.Warn, T("not verified"))
	}
	card.KVRaw("TLS", tls)
	if h.Timezone != "" {
		card.KV(N("time zone"), tview.Escape(h.Timezone))
	}
	app.mu.RLock()
	warnings := append([]string(nil), app.state().meta.warnings...)
	app.mu.RUnlock()
	for _, w := range warnings {
		card.KV(N("warning"), tui.Paint(theme.Warn, w))
	}

	card.Section(N("Database nodes"), T("searched in param.location.node"))
	for _, n := range app.NodeValues() {
		card.Line(tview.Escape(n))
	}

	card.Section(N("Modules"))
	switch {
	case !ex.loaded:
		card.Line(tui.Faint(T("loading…")))
	case ex.modulesErr != nil:
		card.Line(tui.Paint(theme.Err, ex.modulesErr.Error()))
	default:
		for _, k := range sortedKeys(ex.modules) {
			card.KVRaw(k, enabled(ex.modules[k]))
		}
	}

	card.Section(N("Auth types"))
	switch {
	case !ex.loaded:
		card.Line(tui.Faint(T("loading…")))
	case ex.authTypesErr != nil:
		card.Line(tui.Paint(theme.Err, ex.authTypesErr.Error()))
	default:
		for _, k := range sortedKeys(ex.authTypes) {
			card.KVRaw(k, enabled(ex.authTypes[k]))
		}
	}

	ms := app.Mappings()
	card.Section(N("Search profiles"), T(":search <profile>, fields in :mappings"))
	for _, m := range ms {
		card.KVRaw(m.Key(), tview.Escape(m.Title())+tui.Faint("  "+Tf("%d fields", len(m.Fields))))
	}
	return card
}

// enabled reads {"enable": true, ...} as yes or no, with the name or type.
func enabled(v any) string {
	m, _ := v.(map[string]any)
	on, _ := m["enable"].(bool)
	out := tui.Paint(theme.Err, T("off"))
	if on {
		out = tui.Paint(theme.OK, T("on"))
	}
	for _, k := range []string{"name", "type"} {
		if s, ok := m[k].(string); ok && s != "" {
			out += tui.Faint("  " + s)
		}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toJSON is v indented.
func toJSON(v any) string {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(out)
}
