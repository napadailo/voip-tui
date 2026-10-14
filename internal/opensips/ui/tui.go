package ui

import "github.com/napadailo/voip-tui/tui"

// The views of opensips-tui are built from the tui framework; these
// aliases keep their code short.
type (
	View       = tui.View
	Hint       = tui.Hint
	Row        = tui.Row
	TableView  = tui.TableView
	TextViewer = tui.TextViewer
	Flash      = tui.Flash
)

// NewTableView builds a table in the frame of app.
func NewTableView(app *App, title string, headers []string) *TableView {
	return tui.NewTableView(app.App, title, headers)
}

func newTextViewer(app *App, name, syntax string) *TextViewer {
	return tui.NewTextViewer(app.App, name, syntax)
}
