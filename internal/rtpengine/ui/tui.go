package ui

import "github.com/napadailo/voip-tui/tui"

// The views of rtpengine-tui are built from the tui framework; these
// aliases keep their code short.
type (
	View       = tui.View
	Hint       = tui.Hint
	Row        = tui.Row
	Tone       = tui.Tone
	TableView  = tui.TableView
	TextViewer = tui.TextViewer
	Flash      = tui.Flash
)

const (
	ToneFg     = tui.ToneFg
	ToneOK     = tui.ToneOK
	ToneWarn   = tui.ToneWarn
	ToneErr    = tui.ToneErr
	ToneDim    = tui.ToneDim
	ToneAccent = tui.ToneAccent
)

// NewTableView builds a table in the frame of app.
func NewTableView(app *App, title string, headers []string) *TableView {
	return tui.NewTableView(app.App, title, headers)
}

func newTextViewer(app *App, name, syntax string) *TextViewer {
	return tui.NewTextViewer(app.App, name, syntax)
}

var (
	strWidth    = tui.StrWidth
	pad         = tui.Pad
	padLeft     = tui.PadLeft
	kvText      = tui.KVText
	kvTextWidth = tui.KVTextWidth
	stripTags   = tui.StripTags
	trunc       = tui.Trunc
	shortPath   = tui.ShortPath
)
