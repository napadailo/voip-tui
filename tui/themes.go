package tui

import "github.com/napadailo/voip-tui/tui/theme"

// ThemesView lists themes with a live preview: moving the cursor applies a
// theme, enter keeps it, esc restores the one in use before.
type ThemesView struct {
	*TableView
	app      *App
	original string
}

// NewThemesView builds the theme picker.
func NewThemesView(app *App) *ThemesView {
	v := &ThemesView{TableView: NewTableView(app, N("Themes"), []string{"", N("NAME"), N("BACKGROUND"), N("BORDER"), N("ACCENT")}), app: app, original: theme.Current()}
	v.Refresh()
	v.SelectKey(theme.Current())
	v.OnSelect(func(r *Row) { _ = theme.Apply(r.Key) })
	v.OnEnter(func(r *Row) {
		v.original = r.Key
		if err := app.SetTheme(r.Key); err != nil {
			app.Flash().Err(err)
			return
		}
		app.Flash().Infof("theme %s (set theme: %s in the config to keep it)", r.Key, r.Key)
		v.Refresh()
	})
	return v
}

// Name is shown in the breadcrumbs.
func (v *ThemesView) Name() string { return "themes" }

// Table returns the list.
func (v *ThemesView) Table() *TableView { return v.TableView }

// Close restores the theme in use when the picker was opened, unless enter
// kept another one.
func (v *ThemesView) Close() {
	if theme.Current() != v.original {
		_ = v.app.SetTheme(v.original)
	}
}

// Hints of the picker.
func (v *ThemesView) Hints() []Hint {
	return []Hint{{Key: "enter", Desc: N("apply")}, {Key: "esc", Desc: N("cancel")}}
}

// Refresh lists the themes.
func (v *ThemesView) Refresh() {
	var rows []Row
	for _, n := range theme.Names() {
		t := theme.Themes[n]
		cur, tone := "", ToneFg
		if n == v.original {
			cur, tone = "*", ToneOK
		}
		rows = append(rows, Row{Key: n, Tone: tone, Cells: []string{cur, n, t["bg"], t["border"], t["accent"]}})
	}
	v.SetRows(rows)
}
