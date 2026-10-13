// Package theme holds the color schemes of the voip-tui tools. The current
// colors are package variables: views read them when they draw, so a theme
// switch is a redraw.
package theme

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/napadailo/voip-tui/i18n"
)

// Current colors.
var (
	Bg, Fg, Border, Title, Header    tcell.Color
	Key, KeyDesc, InfoKey, InfoValue tcell.Color
	Logo, SelectBg, SelectFg, Marked tcell.Color
	CrumbBg, CrumbFg, CrumbLast      tcell.Color
	OK, Warn, Err, Dim, Accent       tcell.Color
	JSONKey, JSONString              tcell.Color
	current                          = Default
)

// Default is the name of the default theme, the k9s default skin.
const Default = "default"

// Theme is a skin: color names or #rrggbb by key, the keys accepted in the
// `themes:` section of the tools' configs.
type Theme map[string]string

// Keys are the color keys of a theme.
var Keys = []string{"bg", "fg", "border", "title", "header", "key", "keyDesc", "infoKey", "infoValue",
	"logo", "selectBg", "selectFg", "marked", "crumbBg", "crumbFg", "crumbLast", "ok", "warn", "err", "dim", "accent",
	"jsonKey", "jsonString"}

func vars() map[string]*tcell.Color {
	return map[string]*tcell.Color{
		"bg": &Bg, "fg": &Fg, "border": &Border, "title": &Title, "header": &Header,
		"key": &Key, "keyDesc": &KeyDesc, "infoKey": &InfoKey, "infoValue": &InfoValue,
		"logo": &Logo, "selectBg": &SelectBg, "selectFg": &SelectFg, "marked": &Marked,
		"crumbBg": &CrumbBg, "crumbFg": &CrumbFg, "crumbLast": &CrumbLast,
		"ok": &OK, "warn": &Warn, "err": &Err, "dim": &Dim, "accent": &Accent,
		"jsonKey": &JSONKey, "jsonString": &JSONString,
	}
}

// aliases are other names of built-in themes.
var aliases = map[string]string{"k9s": Default}

// Themes are the built-in skins; jsonKey and jsonString default to key and ok.
var Themes = map[string]Theme{
	Default: {"bg": "default", "fg": "lightskyblue", "border": "dodgerblue", "title": "aqua", "header": "white",
		"key": "dodgerblue", "keyDesc": "gray", "infoKey": "orange", "infoValue": "white", "logo": "orange",
		"selectBg": "aqua", "selectFg": "black", "marked": "darkorange", "crumbBg": "aqua", "crumbFg": "black",
		"crumbLast": "orange", "ok": "springgreen", "warn": "yellow", "err": "orangered", "dim": "slategray", "accent": "mediumpurple",
		"jsonKey": "steelblue", "jsonString": "papayawhip"},
	"dracula": {"bg": "#282a36", "fg": "#f8f8f2", "border": "#6272a4", "title": "#bd93f9", "header": "#8be9fd",
		"key": "#ff79c6", "keyDesc": "#6272a4", "infoKey": "#ffb86c", "infoValue": "#f8f8f2", "logo": "#ff79c6",
		"selectBg": "#44475a", "selectFg": "#f8f8f2", "marked": "#ffb86c", "crumbBg": "#bd93f9", "crumbFg": "#282a36",
		"crumbLast": "#ff79c6", "ok": "#50fa7b", "warn": "#f1fa8c", "err": "#ff5555", "dim": "#6272a4", "accent": "#8be9fd",
		"jsonKey": "#8be9fd", "jsonString": "#f1fa8c"},
	"nord": {"bg": "#2e3440", "fg": "#d8dee9", "border": "#4c566a", "title": "#88c0d0", "header": "#81a1c1",
		"key": "#81a1c1", "keyDesc": "#616e88", "infoKey": "#ebcb8b", "infoValue": "#eceff4", "logo": "#88c0d0",
		"selectBg": "#434c5e", "selectFg": "#eceff4", "marked": "#d08770", "crumbBg": "#5e81ac", "crumbFg": "#eceff4",
		"crumbLast": "#b48ead", "ok": "#a3be8c", "warn": "#ebcb8b", "err": "#bf616a", "dim": "#616e88", "accent": "#b48ead",
		"jsonKey": "#81a1c1", "jsonString": "#a3be8c"},
	"gruvbox": {"bg": "#282828", "fg": "#ebdbb2", "border": "#665c54", "title": "#fabd2f", "header": "#d3869b",
		"key": "#8ec07c", "keyDesc": "#928374", "infoKey": "#fe8019", "infoValue": "#ebdbb2", "logo": "#fe8019",
		"selectBg": "#504945", "selectFg": "#fbf1c7", "marked": "#fe8019", "crumbBg": "#fabd2f", "crumbFg": "#282828",
		"crumbLast": "#fe8019", "ok": "#b8bb26", "warn": "#fabd2f", "err": "#fb4934", "dim": "#7c6f64", "accent": "#83a598",
		"jsonKey": "#83a598", "jsonString": "#b8bb26"},
	"solarized-dark": {"bg": "#002b36", "fg": "#93a1a1", "border": "#586e75", "title": "#268bd2", "header": "#2aa198",
		"key": "#268bd2", "keyDesc": "#586e75", "infoKey": "#b58900", "infoValue": "#eee8d5", "logo": "#cb4b16",
		"selectBg": "#073642", "selectFg": "#eee8d5", "marked": "#cb4b16", "crumbBg": "#268bd2", "crumbFg": "#002b36",
		"crumbLast": "#d33682", "ok": "#859900", "warn": "#b58900", "err": "#dc322f", "dim": "#586e75", "accent": "#6c71c4",
		"jsonKey": "#268bd2", "jsonString": "#2aa198"},
	"solarized-light": {"bg": "#fdf6e3", "fg": "#586e75", "border": "#93a1a1", "title": "#268bd2", "header": "#073642",
		"key": "#268bd2", "keyDesc": "#93a1a1", "infoKey": "#cb4b16", "infoValue": "#073642", "logo": "#cb4b16",
		"selectBg": "#eee8d5", "selectFg": "#073642", "marked": "#d33682", "crumbBg": "#268bd2", "crumbFg": "#fdf6e3",
		"crumbLast": "#d33682", "ok": "#859900", "warn": "#b58900", "err": "#dc322f", "dim": "#93a1a1", "accent": "#6c71c4",
		"jsonKey": "#268bd2", "jsonString": "#2aa198"},
	"monokai": {"bg": "#272822", "fg": "#f8f8f2", "border": "#75715e", "title": "#a6e22e", "header": "#66d9ef",
		"key": "#f92672", "keyDesc": "#75715e", "infoKey": "#fd971f", "infoValue": "#f8f8f2", "logo": "#f92672",
		"selectBg": "#49483e", "selectFg": "#f8f8f2", "marked": "#fd971f", "crumbBg": "#a6e22e", "crumbFg": "#272822",
		"crumbLast": "#f92672", "ok": "#a6e22e", "warn": "#e6db74", "err": "#f92672", "dim": "#75715e", "accent": "#ae81ff",
		"jsonKey": "#66d9ef", "jsonString": "#e6db74"},
	"catppuccin": {"bg": "#1e1e2e", "fg": "#cdd6f4", "border": "#585b70", "title": "#89b4fa", "header": "#cba6f7",
		"key": "#89dceb", "keyDesc": "#6c7086", "infoKey": "#fab387", "infoValue": "#cdd6f4", "logo": "#fab387",
		"selectBg": "#313244", "selectFg": "#cdd6f4", "marked": "#fab387", "crumbBg": "#89b4fa", "crumbFg": "#1e1e2e",
		"crumbLast": "#f5c2e7", "ok": "#a6e3a1", "warn": "#f9e2af", "err": "#f38ba8", "dim": "#6c7086", "accent": "#f5c2e7",
		"jsonKey": "#89b4fa", "jsonString": "#a6e3a1"},
	"tokyo-night": {"bg": "#1a1b26", "fg": "#c0caf5", "border": "#7aa2f7", "title": "#bb9af7", "header": "#c0caf5",
		"key": "#7aa2f7", "keyDesc": "#565f89", "infoKey": "#ff9e64", "infoValue": "#c0caf5", "logo": "#bb9af7",
		"selectBg": "#33467c", "selectFg": "#c0caf5", "marked": "#ff9e64", "crumbBg": "#7aa2f7", "crumbFg": "#1a1b26",
		"crumbLast": "#bb9af7", "ok": "#9ece6a", "warn": "#e0af68", "err": "#f7768e", "dim": "#565f89", "accent": "#7dcfff",
		"jsonKey": "#7aa2f7", "jsonString": "#9ece6a"},
}

func resolve(name string) string {
	if a, ok := aliases[name]; ok {
		return a
	}
	return name
}

// Register adds custom themes from a config: {name: {base: nord, border: "#ff8800"}}.
func Register(custom map[string]map[string]string) error {
	names := make([]string, 0, len(custom))
	for n := range custom {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		base := custom[n]["base"]
		if base == "" {
			base = Default
		}
		b, ok := Themes[resolve(base)]
		if !ok {
			return fmt.Errorf(i18n.T("theme %q: unknown base %q"), n, base)
		}
		t := Theme{}
		for k, v := range b {
			t[k] = v
		}
		for k, v := range custom[n] {
			if k == "base" {
				continue
			}
			if _, ok := vars()[k]; !ok {
				return fmt.Errorf(i18n.T("theme %q: unknown color %q, known: %s"), n, k, strings.Join(Keys, ", "))
			}
			t[k] = v
		}
		Themes[n] = t
	}
	return nil
}

// Names lists the themes, sorted, aliases left out.
func Names() []string {
	var n []string
	for k := range Themes {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Current is the name of the applied theme.
func Current() string { return current }

// Apply switches the colors and the tview defaults; "" is the default theme.
func Apply(name string) error {
	if name == "" {
		name = Default
	}
	t, ok := Themes[resolve(name)]
	if !ok {
		return fmt.Errorf(i18n.T("unknown theme %q, available: %s"), name, strings.Join(Names(), ", "))
	}
	v := vars()
	for _, k := range Keys {
		*v[k] = parse(t[k])
	}
	if t["jsonKey"] == "" {
		JSONKey = Key
	}
	if t["jsonString"] == "" {
		JSONString = OK
	}
	current = resolve(name)
	tview.Styles.PrimitiveBackgroundColor = Bg
	tview.Styles.ContrastBackgroundColor = SelectBg
	tview.Styles.MoreContrastBackgroundColor = Border
	tview.Styles.BorderColor = Border
	tview.Styles.TitleColor = Title
	tview.Styles.GraphicsColor = Border
	tview.Styles.PrimaryTextColor = Fg
	tview.Styles.SecondaryTextColor = InfoKey
	tview.Styles.TertiaryTextColor = OK
	tview.Styles.InverseTextColor = SelectFg
	tview.Styles.ContrastSecondaryTextColor = InfoValue
	return nil
}

// ButtonStyle is a button without focus: plain text.
func ButtonStyle() tcell.Style { return tcell.StyleDefault.Background(Bg).Foreground(Fg) }

// ButtonActivatedStyle is the focused button, colored like a selected row.
// tview's own default (text in selectFg on fg) is unreadable in themes where
// the two are equal, and in the default theme it looks like the other button.
func ButtonActivatedStyle() tcell.Style {
	return tcell.StyleDefault.Background(SelectBg).Foreground(SelectFg).Bold(true)
}

func parse(s string) tcell.Color {
	if s == "" || s == "default" {
		return tcell.ColorDefault
	}
	return tcell.GetColor(s)
}

func init() { _ = Apply(Default) }

// Tag is the tview color tag of c; the default color resets to "[-]".
func Tag(c tcell.Color) string {
	if c == tcell.ColorDefault || c == 0 {
		return "[-]"
	}
	return fmt.Sprintf("[#%06x]", c.Hex())
}

// Name renders c for tview style tags ("[fg:bg]"); default stays "-".
func Name(c tcell.Color) string {
	if c == tcell.ColorDefault || c == 0 {
		return "-"
	}
	return fmt.Sprintf("#%06x", c.Hex())
}
