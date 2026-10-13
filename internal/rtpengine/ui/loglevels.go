package ui

import (
	"context"
	"strconv"
	"strings"

	"github.com/napadailo/voip-tui/internal/rtpengine"
)

// LogLevelsView lists the log levels per subsystem (one level before
// mr9.5) and changes them at runtime: +/- one step right away, enter puts
// `set loglevel <subsystem> ` in the prompt, a the same for all.
type LogLevelsView struct {
	*TableView
	app  *App
	busy bool
}

func newLogLevelsView(app *App) *LogLevelsView {
	v := &LogLevelsView{TableView: NewTableView(app, N("Log levels"), []string{N("SUBSYSTEM"), N("LEVEL"), N("NAME"), N("DESCRIPTION")}), app: app}
	prompt := func(r *Row) {
		l := r.Data.(rtpengine.LogLevel)
		app.Prompt(strings.TrimSpace("set loglevel "+l.Name) + " ")
	}
	v.OnEnter(prompt)
	v.Bind('e', func() {
		if r := v.Selected(); r != nil {
			prompt(r)
		}
	})
	v.Bind('a', func() { app.Prompt("set loglevel ") })
	v.Bind('+', func() { v.step(1) })
	v.Bind('=', func() { v.step(1) }) // + without shift
	v.Bind('-', func() { v.step(-1) })
	app.ensureProbed(v.Refresh)
	return v
}

func (v *LogLevelsView) Name() string      { return "loglevels" }
func (v *LogLevelsView) Table() *TableView { return v.TableView }
func (v *LogLevelsView) Close()            {}
func (v *LogLevelsView) AutoRefresh() bool { return true }
func (v *LogLevelsView) Hints() []Hint {
	return []Hint{{Key: "+/-", Desc: N("level up/down")}, {Key: "enter", Desc: N("set level")}, {Key: "a", Desc: N("all subsystems")}}
}

// step raises or lowers the level of the selected subsystem by one.
func (v *LogLevelsView) step(delta int) {
	r := v.Selected()
	if r == nil {
		return
	}
	l := r.Data.(rtpengine.LogLevel)
	n := l.Level + delta
	if n < rtpengine.MinLogLevel || n > rtpengine.MaxLogLevel {
		v.app.Flash().Warnf("log levels go from 1 (alert) to 7 (debug)")
		return
	}
	c := v.app.Client()
	v.app.Async(func(ctx context.Context) func() {
		out, err := c.SetLogLevel(ctx, l.Name, n)
		return func() {
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			name := l.Name
			if name == "" {
				name = T("all subsystems")
			}
			v.app.Flash().Infof("%s: %s · %s", name, strconv.Itoa(n)+" "+rtpengine.LevelName(n), strings.TrimSpace(out))
			v.Refresh()
		}
	})
}

// Refresh reads the levels.
func (v *LogLevelsView) Refresh() {
	if v.busy {
		return
	}
	v.busy = true
	v.SetLoading(true)
	c := v.app.Client()
	v.app.Async(func(ctx context.Context) func() {
		levels, err := c.LogLevels(ctx)
		return func() {
			v.busy = false
			v.SetLoading(false)
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			rows := make([]Row, 0, len(levels))
			for _, l := range levels {
				name := l.Name
				if name == "" {
					name = T("(all)") // before mr9.5: one level
				}
				r := Row{Key: l.Name, Data: l, Cells: []string{name, strconv.Itoa(l.Level), rtpengine.LevelName(l.Level), l.Desc}}
				if l.Level >= 7 { // debug floods the log on a busy host
					r.CellTones = map[int]Tone{1: ToneWarn, 2: ToneWarn}
				}
				rows = append(rows, r)
			}
			v.SetRows(rows)
			v.SetTitleInfo(v.app.host.Name + " · " + T("changed at runtime, rtpengine.conf keeps its own"))
		}
	})
}
