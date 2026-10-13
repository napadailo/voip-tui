package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/tui"
	"github.com/napadailo/voip-tui/tui/theme"
)

// SettingsView shows rtpengine.conf and rtpengine-recording.conf as a table,
// checked against the option set of the host's release and the running config.
type SettingsView struct {
	*TableView
	app  *App
	mode string // all, main, issues
	data []rtpengine.Setting
}

// settingsModes names the v modes of the config view.
var settingsModes = map[string]string{"all": N("all entries"), "main": N("daemon sections only"), "issues": N("entries with findings")}

func newSettingsView(app *App) *SettingsView {
	v := &SettingsView{TableView: NewTableView(app, N("Config"), []string{N("DAEMON"), N("SECTION"), N("OPTION"), N("VALUE"), N("RUNTIME"), N("STATUS"), N("DESCRIPTION")}), app: app, mode: "all"}
	open := func(r *Row) {
		s := r.Data.(rtpengine.Setting)
		if s.Role == "templates" {
			app.Push(newTemplateView(app, s))
			return
		}
		app.Push(newOptionView(app, s))
	}
	v.OnEnter(open)
	v.Bind('d', func() {
		if r := v.Selected(); r != nil {
			open(r)
		}
	})
	v.Bind('v', func() {
		v.mode = map[string]string{"all": "main", "main": "issues", "issues": "all"}[v.mode]
		v.show()
		app.Flash().Infof("showing %s", T(settingsModes[v.mode]))
	})
	v.Bind('y', func() { app.Push(newRawFileView(app, rtpengine.DaemonRTPEngine)) })
	v.Bind('Y', func() { app.Push(newRawFileView(app, rtpengine.DaemonRecording)) })
	app.ensureProbed(v.Refresh)
	return v
}

func (v *SettingsView) Name() string      { return "config" }
func (v *SettingsView) Table() *TableView { return v.TableView }
func (v *SettingsView) Close()            {}
func (v *SettingsView) Hints() []Hint {
	return []Hint{{Key: "enter", Desc: N("option")}, {Key: "v", Desc: N("all/main/issues")}, {Key: "y", Desc: N("rtpengine.conf")}, {Key: "Y", Desc: N("recording.conf")}, {Key: "o", Desc: N("sort")}}
}

// Refresh re-reads both files and `params current`.
func (v *SettingsView) Refresh() {
	v.SetLoading(true)
	c := v.app.Client()
	v.app.Async(func(ctx context.Context) func() {
		data, errs := c.Settings(ctx)
		return func() {
			v.data = data
			v.show()
			for _, e := range errs {
				v.app.Flash().Err(e)
			}
		}
	})
}

func (v *SettingsView) show() {
	var rows []Row
	issues := 0
	for i, s := range v.data {
		if s.Status >= rtpengine.StatusNotInVersion || s.Status == rtpengine.StatusDiffers || s.Status == rtpengine.StatusOverridden {
			issues++
		}
		switch v.mode {
		case "main":
			if s.Role != "main" {
				continue
			}
		case "issues":
			if s.Status == rtpengine.StatusOK || s.Status == rtpengine.StatusOtherSection {
				continue
			}
		}
		daemon := "rtpengine"
		if s.Daemon == rtpengine.DaemonRecording {
			daemon = "recording"
		}
		status := T(s.Status.String())
		tone := ToneFg
		switch s.Status {
		case rtpengine.StatusDiffers:
			tone = ToneWarn
		case rtpengine.StatusOverridden:
			tone = ToneDim
		case rtpengine.StatusOtherSection:
			tone, status = ToneDim, T(s.Role)
		case rtpengine.StatusNotInVersion, rtpengine.StatusUnknown:
			tone = ToneErr
		}
		desc := s.Desc
		if s.Note != "" && s.Status != rtpengine.StatusOtherSection {
			desc = s.Note
		}
		rows = append(rows, Row{Key: fmt.Sprintf("%d", i), Data: s, Tone: tone,
			CellTones: map[int]Tone{5: tone},
			Cells:     []string{daemon, s.Section, s.Key, rtpengine.MaskSecret(s.Key, s.Value), rtpengine.MaskSecret(s.Key, s.Runtime), status, desc}})
	}
	v.SetRows(rows)
	ver := v.app.Client().Info().Version
	v.SetTitleInfo(v.app.host.Name + " · " + ver.String() + " · " + Tf("%d finding(s)", issues) + " · " + T(settingsModes[v.mode]))
}

func newOptionView(app *App, s rtpengine.Setting) *TextViewer {
	v := newTextViewer(app, "option "+s.Key, "markup")
	v.SetWrap(true) // descriptions are prose
	v.Loader = func() { v.SetContent(optionText(app, s), "markup") }
	v.Loader()
	return v
}

// optionText is the card of one config entry: what it is, its value in the
// file and in the running daemon, and which releases have the option.
func optionText(app *App, s rtpengine.Setting) string {
	desc, arg, branches := rtpengine.OptionHelp(s.Daemon, s.Key)
	if desc == "" {
		desc = s.Desc
	}
	c := tui.NewCard(s.Key).Sub(fmt.Sprintf("[%s] (%s)", s.Section, T(s.Role)), fmt.Sprintf("%s:%d", s.File, s.Line)).Text(desc)

	c.Section(N("Value"))
	value := rtpengine.MaskSecret(s.Key, s.Value)
	c.KV(N("in the file"), tui.Strong(value))
	if strings.Contains(s.Value, ";") && value == s.Value {
		for i, part := range strings.Split(s.Value, ";") {
			c.Line(tui.Faint(fmt.Sprintf("[%d] ", i)) + tview.Escape(strings.TrimSpace(part)))
		}
	}
	if s.Runtime != "" {
		running := rtpengine.MaskSecret(s.Key, s.Runtime)
		switch {
		case sameNumber(s.Runtime, s.Value):
			c.KV(N("running"), tui.Paint(theme.OK, running)+tui.Faint(" · "+T("same as the file")))
		case s.Status == rtpengine.StatusDiffers:
			c.KV(N("running"), tui.Paint(theme.Warn, running)+tui.Faint(" · "+T("differs from the file: changed at runtime")))
		default: // overridden by a later line, the daemon runs that one
			c.KV(N("running"), tui.Paint(theme.InfoValue, running))
		}
	}
	tone := theme.OK
	switch s.Status {
	case rtpengine.StatusDiffers:
		tone = theme.Warn
	case rtpengine.StatusOverridden, rtpengine.StatusOtherSection:
		tone = theme.Dim
	case rtpengine.StatusNotInVersion, rtpengine.StatusUnknown:
		tone = theme.Err
	}
	c.KV(N("status"), tui.Paint(tone, T(s.Status.String())))
	if s.Note != "" {
		c.KV(N("note"), tui.Paint(theme.Warn, s.Note))
	}

	c.Section(N("Option"))
	if arg != "" {
		c.KV(N("argument"), tview.Escape(arg))
	}
	if len(branches) > 0 {
		c.KV(N("releases"), tview.Escape(rtpengine.ReleaseRanges(branches)))
	}
	ver := app.Client().Info().Version.String()
	switch s.Status {
	case rtpengine.StatusNotInVersion:
		c.KV(N("on this host"), tui.Paint(theme.Err, Tf("no: %s does not have it", ver)))
	case rtpengine.StatusUnknown:
		c.KV(N("on this host"), tui.Paint(theme.Err, T("unknown option")))
	default:
		if len(branches) > 0 {
			c.KV(N("on this host"), tui.Paint(theme.OK, Tf("yes, %s", ver)))
		}
	}
	return c.String()
}

func newRawFileView(app *App, d rtpengine.Daemon) View {
	info := app.Client().Info()
	path := info.ConfigPath
	if d == rtpengine.DaemonRecording {
		path = info.RecConfPath
	}
	if path == "" {
		path = d.DefaultPath()
	}
	v := newTextViewer(app, path, "plain")
	v.SetLoading()
	v.Loader = func() {
		app.Async(func(ctx context.Context) func() {
			text, err := app.Client().ReadFile(ctx, path)
			return func() {
				if err != nil {
					v.SetError(err)
					return
				}
				v.SetContent(maskConfText(text), "plain")
			}
		})
	}
	v.Loader()
	return v
}

// maskConfText hides passwords in a raw config file.
func maskConfText(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		k, val, ok := strings.Cut(l, "=")
		if !ok || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		key, value := strings.TrimSpace(k), strings.TrimSpace(val)
		if m := rtpengine.MaskSecret(key, value); m != value {
			lines[i] = k + "= " + m
		}
	}
	return strings.Join(lines, "\n")
}

// TunablesView lists runtime limits (`list X`) and sets them (`set X`).
type TunablesView struct {
	*TableView
	app *App
}

func newTunablesView(app *App) *TunablesView {
	v := &TunablesView{TableView: NewTableView(app, N("Limits"), []string{N("NAME"), N("VALUE"), N("CONFIG"), N("OPTION"), N("UNIT"), N("SET")}), app: app}
	edit := func(r *Row) {
		t := r.Data.(rtpengine.Tunable)
		if !t.Settable {
			app.Flash().Warnf("%s cannot be set at runtime on %s", t.Name, app.Client().Info().Version.String())
			return
		}
		app.Prompt("set " + t.Name + " ")
	}
	v.OnEnter(edit)
	v.Bind('e', func() {
		if r := v.Selected(); r != nil {
			edit(r)
		}
	})
	app.ensureProbed(v.Refresh)
	return v
}

func (v *TunablesView) Name() string      { return "limits" }
func (v *TunablesView) Table() *TableView { return v.TableView }
func (v *TunablesView) Close()            {}
func (v *TunablesView) AutoRefresh() bool { return true }
func (v *TunablesView) Hints() []Hint     { return []Hint{{Key: "e/enter", Desc: N("set value")}} }

// Refresh reads all tunables.
func (v *TunablesView) Refresh() {
	v.SetLoading(true)
	c := v.app.Client()
	v.app.Async(func(ctx context.Context) func() {
		tun := c.Tunables(ctx)
		info := c.Info()
		return func() {
			var rows []Row
			for _, t := range tun {
				conf := ""
				if t.Option != "" && info.Conf != nil {
					conf, _ = info.Conf.Get(info.Section, t.Option)
				}
				r := Row{Key: t.Name, Data: t, Cells: []string{t.Name, t.Value, conf, t.Option, T(t.Unit), yesNo(t.Settable)}}
				switch {
				case t.Err != nil:
					r.Tone, r.Cells[1] = ToneErr, t.Err.Error()
				case conf != "" && !sameNumber(conf, t.Value):
					r.CellTones = map[int]Tone{1: ToneWarn, 2: ToneWarn}
				}
				rows = append(rows, r)
			}
			v.SetRows(rows)
			v.SetTitleInfo(v.app.host.Name + " · " + T("values differing from rtpengine.conf are highlighted"))
		}
	})
}

func sameNumber(a, b string) bool {
	fa, ea := strconv.ParseFloat(a, 64)
	fb, eb := strconv.ParseFloat(b, 64)
	if ea != nil || eb != nil {
		return a == b
	}
	return fa == fb
}

func yesNo(b bool) string {
	if b {
		return T("yes")
	}
	return T("no")
}

// InterfacesView is `list interfaces` (mr7.5+): port usage per interface,
// enter opens the parsed counters of one interface.
type InterfacesView struct {
	*TableView
	app *App
}

func newInterfacesView(app *App) *InterfacesView {
	v := &InterfacesView{TableView: NewTableView(app, N("Interfaces"), []string{N("NAME"), N("ADDRESS"), N("FAMILY"), N("PORTS"), N("USED"), N("TOTAL"), N("USAGE")}), app: app}
	v.OnEnter(func(r *Row) { app.Push(newInterfaceView(app, r.Key)) })
	app.ensureProbed(v.Refresh)
	return v
}

func (v *InterfacesView) Name() string      { return "interfaces" }
func (v *InterfacesView) Table() *TableView { return v.TableView }
func (v *InterfacesView) Close()            {}
func (v *InterfacesView) AutoRefresh() bool { return true }
func (v *InterfacesView) Hints() []Hint     { return []Hint{{Key: "enter", Desc: N("counters")}} }

// Refresh reloads interfaces.
func (v *InterfacesView) Refresh() {
	c := v.app.Client()
	v.SetLoading(true)
	v.app.Async(func(ctx context.Context) func() {
		out, err := c.Text(ctx, "list interfaces")
		return func() {
			v.SetLoading(false)
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			var rows []Row
			for _, i := range rtpengine.ParseInterfaces(out) {
				pct := portUsage(i)
				r := Row{Key: interfaceKey(i), Data: i, Cells: []string{i.Name, i.Address, i.Family, fmt.Sprintf("%d-%d", i.PortMin, i.PortMax),
					strconv.Itoa(i.Used), strconv.Itoa(i.Total), fmt.Sprintf("%.1f%%", pct)}}
				switch {
				case pct >= 90:
					r.Tone = ToneErr
				case pct >= 70:
					r.Tone = ToneWarn
				}
				rows = append(rows, r)
			}
			v.SetRows(rows)
			v.SetTitleInfo(v.app.host.Name)
		}
	})
}

func interfaceKey(i rtpengine.Interface) string { return i.Name + "/" + i.Address }

func portUsage(i rtpengine.Interface) float64 {
	if i.Total == 0 {
		return 0
	}
	return float64(i.Used) * 100 / float64(i.Total)
}

// newInterfaceView shows one interface of `list interfaces`, refreshed with the ticker.
func newInterfaceView(app *App, key string) View {
	v := newTextViewer(app, "interface "+key, "markup")
	v.SetLoading()
	c := app.Client()
	v.Loader = func() {
		app.Async(func(ctx context.Context) func() {
			out, err := c.Text(ctx, "list interfaces")
			return func() {
				if err != nil {
					v.SetError(err)
					return
				}
				for _, i := range rtpengine.ParseInterfaces(out) {
					if interfaceKey(i) == key {
						v.SetContent(interfaceText(i, c.Info().Version), "markup")
						return
					}
				}
				v.SetError(fmt.Errorf(T("interface %s is gone from list interfaces"), key))
			}
		})
	}
	app.ensureProbed(v.Loader)
	return &autoText{v}
}

func interfaceText(i rtpengine.Interface, ver rtpengine.Version) string {
	c := tui.NewCard(i.Name).Sub(i.Address, i.Family)

	c.Section(N("Ports"))
	c.KV(N("range"), fmt.Sprintf("%d-%d", i.PortMin, i.PortMax))
	pct := portUsage(i)
	tone := theme.OK
	switch {
	case pct >= 90:
		tone = theme.Err
	case pct >= 70:
		tone = theme.Warn
	}
	c.KV(N("used"), tui.Strong(strconv.Itoa(i.Used))+tui.Faint(" "+Tf("of %d", i.Total)+" · ")+tui.Paint(tone, fmt.Sprintf("%.1f%%", pct)))
	if i.LastPort != "" {
		c.KV(N("last used"), tview.Escape(i.LastPort))
	}

	c.Section(N("Traffic"))
	if !i.HasTraffic {
		c.Line(tui.Faint(Tf("Traffic and quality counters are printed by rtpengine mr12.5+, host runs %s.", ver.String())))
	} else {
		c.Line(tui.Paint(theme.Header, padLeft(T("PACKETS"), 14)+padLeft(T("BYTES"), 11)+padLeft(T("ERRORS"), 9)))
		for _, d := range []struct {
			name string
			t    rtpengine.Traffic
		}{{N("ingress"), i.In}, {N("egress"), i.Out}} {
			errTone := theme.Fg
			if n, _ := strconv.ParseUint(d.t.Errors, 10, 64); n > 0 {
				errTone = theme.Warn
			}
			c.KV(d.name, fmt.Sprintf("%14s%11s", groupDigits(d.t.Packets), humanBytes(d.t.Bytes))+tui.Paint(errTone, fmt.Sprintf("%9s", d.t.Errors)))
		}
		c.KV(N("lost packets"), fmt.Sprintf("%14s", groupDigits(i.Lost)))
		c.KV(N("duplicates"), fmt.Sprintf("%14s", groupDigits(i.Duplicates)))

		c.Section(N("Quality"), T("averages over sampled streams"))
		c.KV(N("MOS"), graded(i.MOS, func(f float64) tcell.Color {
			switch {
			case f >= 4:
				return theme.OK
			case f >= 3.6:
				return theme.Warn
			}
			return theme.Err
		}, ""))
		c.KV(N("packet loss"), graded(i.PacketLoss, func(f float64) tcell.Color {
			switch {
			case f <= 1:
				return theme.OK
			case f <= 3:
				return theme.Warn
			}
			return theme.Err
		}, "%"))
		jitter := orNoSamples(i.Jitter)
		if i.JitterMeas != "" {
			jitter += tui.Faint(" · " + Tf("measured %s", i.JitterMeas))
		}
		c.KV(N("jitter"), jitter)
		c.KV(N("RTT e2e"), orNoSamples(i.RTTE2E))
		c.KV(N("RTT dsct"), orNoSamples(i.RTTDiscrete))
	}
	if len(i.Extra) > 0 {
		c.Section(N("Other"))
		for _, kv := range i.Extra {
			c.KV(kv.Key, tview.Escape(kv.Value))
		}
	}
	return c.String()
}

// graded colors a number by how good it is; "" has no samples yet.
func graded(s string, tone func(float64) tcell.Color, unit string) string {
	if s == "" {
		return orNoSamples(s)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return tview.Escape(s + unit)
	}
	return tui.Paint(tone(f), s+unit)
}

func orNoSamples(s string) string {
	if s == "" {
		return tui.Faint(T("no samples yet"))
	}
	return tview.Escape(s)
}

// groupDigits renders 1393629496 as 1 393 629 496.
func groupDigits(s string) string {
	if _, err := strconv.ParseUint(s, 10, 64); err != nil || len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// humanBytes renders a byte counter in decimal units.
func humanBytes(s string) string {
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}
	i := 0
	for n >= 1000 && i < len(units)-1 {
		n /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f B", n)
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

// KVView shows key/value output (params, keyspaces, loglevel).
type KVView struct {
	*TableView
	app  *App
	name string
	load func(ctx context.Context, c *rtpengine.Client) ([]rtpengine.KV, error)
}

func newKVView(app *App, name, title string, headers []string, load func(context.Context, *rtpengine.Client) ([]rtpengine.KV, error)) *KVView {
	v := &KVView{TableView: NewTableView(app, title, headers), app: app, name: name, load: load}
	app.ensureProbed(v.Refresh)
	return v
}

func (v *KVView) Name() string      { return v.name }
func (v *KVView) Table() *TableView { return v.TableView }
func (v *KVView) Close()            {}
func (v *KVView) Hints() []Hint     { return nil }

// Refresh reloads values.
func (v *KVView) Refresh() {
	c := v.app.Client()
	v.SetLoading(true)
	v.app.Async(func(ctx context.Context) func() {
		kvs, err := v.load(ctx, c)
		return func() {
			v.SetLoading(false)
			if err != nil {
				v.app.Flash().Err(err)
			}
			rows := make([]Row, 0, len(kvs))
			for i, kv := range kvs {
				cells := []string{kv.Key, kv.Value}
				if len(v.Headers()) == 3 {
					cells = []string{kv.Section, kv.Key, kv.Value}
				}
				rows = append(rows, Row{Key: strconv.Itoa(i), Cells: cells})
			}
			v.SetRows(rows)
			v.SetTitleInfo(v.app.host.Name)
		}
	})
}

// newCLIView runs a raw CLI command once.
func newCLIView(app *App, cmd string) View {
	v := newTextViewer(app, "cli "+cmd, "plain")
	v.SetLoading()
	v.Loader = func() {
		c := app.Client()
		app.Async(func(ctx context.Context) func() {
			out, err := c.Do(ctx, cmd)
			return func() {
				if err != nil && out == "" {
					v.SetError(err)
					return
				}
				v.SetContent(out, "plain")
			}
		})
	}
	app.ensureProbed(v.Loader)
	return v
}
