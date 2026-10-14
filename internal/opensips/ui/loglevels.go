package ui

import (
	"context"
	"strconv"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/tui/theme"
)

// LogLevelsView shows the two levels OpenSIPS has, log_level of the
// processes and xlog_level of xlog() in the script, as the log levels of
// rtpengine-tui, and changes them at runtime: +/- one step right away,
// enter puts the MI command in the prompt. One process alone is changed in
// :processes.
type LogLevelsView struct {
	*TableView
	app  *App
	busy bool
}

// logParam is a row: log_level (all processes) or xlog_level.
type logParam struct {
	xlog  bool
	level int64
}

func newLogLevelsView(app *App) *LogLevelsView {
	v := &LogLevelsView{TableView: NewTableView(app, N("Log levels"), []string{N("PARAMETER"), N("LEVEL"), N("NAME"), N("DESCRIPTION")}), app: app}
	v.SetTitleInfo(app.host.Name)
	prompt := func(r *Row) {
		if r.Data.(logParam).xlog {
			app.Prompt("mi xlog_level level=")
			return
		}
		app.Prompt("mi log_level level=")
	}
	v.OnEnter(prompt)
	v.Bind('e', func() {
		if r := v.Selected(); r != nil {
			prompt(r)
		}
	})
	v.Bind('+', func() { v.step(1) })
	v.Bind('=', func() { v.step(1) }) // + without shift
	v.Bind('-', func() { v.step(-1) })
	v.Refresh()
	return v
}

func (v *LogLevelsView) Name() string      { return "loglevels" }
func (v *LogLevelsView) Table() *TableView { return v.TableView }
func (v *LogLevelsView) Close()            {}
func (v *LogLevelsView) AutoRefresh() bool { return true }
func (v *LogLevelsView) Hints() []Hint {
	return []Hint{{Key: "+/-", Desc: N("level up/down")}, {Key: "enter", Desc: N("set level")}}
}

// nextLevel steps a log level; OpenSIPS has no level 0, err (-1) is
// followed by warn (1).
func nextLevel(l int64, delta int) int64 {
	n := l + int64(delta)
	if n == 0 {
		n += int64(delta)
	}
	return n
}

// step raises or lowers the selected level by one.
func (v *LogLevelsView) step(delta int) {
	r := v.Selected()
	if r == nil {
		return
	}
	p := r.Data.(logParam)
	n := nextLevel(p.level, delta)
	if n < opensips.MinLogLevel || n > opensips.MaxLogLevel {
		v.app.Flash().Warnf("log levels go from -3 (alert) to 4 (debug)")
		return
	}
	c := v.app.Client()
	op, name := "core.set_log_level", "log_level"
	if p.xlog {
		op, name = "core.set_xlog_level", "xlog_level"
	}
	if s := c.Supports(op); !s.OK {
		v.app.Flash().Warn(s.Reason)
		return
	}
	v.app.Async(func(ctx context.Context) func() {
		var err error
		if p.xlog {
			err = c.SetXlogLevel(ctx, n)
		} else {
			err = c.SetLogLevel(ctx, "", n) // every process
		}
		return func() {
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			v.app.Flash().Infof("%s: %s, until OpenSIPS restarts", name, strconv.FormatInt(n, 10)+" "+opensips.LevelName(n))
			v.Refresh()
		}
	})
}

// Refresh reads log_level of the processes and xlog_level.
func (v *LogLevelsView) Refresh() {
	if v.busy {
		return
	}
	c := v.app.Client()
	if s := c.Supports("core.log_level"); !s.OK {
		v.SetRows([]Row{{Key: "why", Cells: []string{s.Reason}, Color: theme.Err}})
		return
	}
	v.busy = true
	v.app.Async(func(ctx context.Context) func() {
		procs, err := c.ProcessLevels(ctx)
		xlog, xok := int64(0), false
		if c.Supports("core.xlog_level").OK {
			var xerr error
			xlog, xerr = c.XlogLevel(ctx)
			xok = xerr == nil
		}
		return func() {
			v.busy = false
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			var rows []Row
			if groups := opensips.LogLevels(procs); len(groups) > 0 {
				// the level most processes run with; the others are told
				desc := Tf("all %d processes", len(procs))
				if len(groups) > 1 {
					desc = Tf("%d of %d processes, the others differ: see :processes", groups[0].Processes, len(procs))
				}
				rows = append(rows, levelRow("log_level", logParam{level: groups[0].Level}, desc))
			}
			if xok {
				rows = append(rows, levelRow("xlog_level", logParam{xlog: true, level: xlog}, T("xlog() in the script")))
			}
			v.SetRows(rows)
		}
	})
}

func levelRow(name string, p logParam, desc string) Row {
	color := tcell.Color(0)
	if p.level >= 4 { // debug floods the log on a busy host
		color = theme.Warn
	}
	return Row{Key: name, Data: p, Cells: []string{name, strconv.FormatInt(p.level, 10), opensips.LevelName(p.level), desc},
		Colors: []tcell.Color{0, color, color}}
}
