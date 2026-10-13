package ui

import (
	"context"
	"errors"
	"strings"
	"time"
)

// logTimeLayout is the time in front of the lines of journalctl -o
// short-iso-precise, the default logCommand.
const logTimeLayout = "2006-01-02T15:04:05.999999-0700"

// maxLogLines caps what the view shows: it recolors and redraws the whole
// text on every read. s saves the whole log.
const maxLogLines = 5000

// CallLogView shows rtpengine's log lines of a call (the host's logCommand),
// newest at the bottom. f follows the log: every refresh tick reads the lines
// after the last one shown, and the view keeps to the bottom unless scrolled
// up. ctrl-r reads new lines once, s saves the whole log of the call.
type CallLogView struct {
	*TextViewer
	app    *App
	callid string
	since  int64 // where the first read starts, unix seconds
	lines  []string
	total  int // lines in the log, shown or not
	follow bool
	busy   bool
}

func newCallLogView(app *App, callid string, created int64) *CallLogView {
	v := &CallLogView{TextViewer: newTextViewer(app, "log "+trunc(callid, 40), "log"), app: app, callid: callid}
	if created > 0 {
		v.since = created - 60 // signalling may come a little before the call is created
	}
	v.Loader = v.load
	v.Keys['f'] = func() {
		v.follow = !v.follow
		v.setTitle()
		if v.follow {
			v.app.Flash().Infof("following the log: new lines every refresh")
			v.Text().ScrollToEnd()
			v.load()
		} else {
			v.app.Flash().Infof("not following the log")
		}
	}
	v.Keys['s'] = v.save
	v.Extra = []Hint{{Key: "f", Desc: N("follow")}, {Key: "ctrl-r", Desc: N("new lines")}}
	v.SetLoading()
	v.load()
	return v
}

// AutoRefresh makes the ticker read new lines while following.
func (v *CallLogView) AutoRefresh() bool { return v.follow }

func (v *CallLogView) setTitle() {
	t := " log " + trunc(v.callid, 40) + " "
	if v.total > len(v.lines) {
		t += "· " + Tf("last %d of %d lines, s saves all", len(v.lines), v.total) + " "
	}
	if v.follow {
		t += "· " + T("following") + " "
	}
	v.Text().SetTitle(t)
}

// save writes the whole log of the call, not only the lines shown, to the
// download directory.
func (v *CallLogView) save() {
	c := v.app.Client()
	v.app.Flash().Infof("reading the whole log…")
	v.app.Async(func(ctx context.Context) func() {
		out, total, err := c.CallLog(ctx, v.callid, v.since, 0)
		return func() {
			if err == nil && total == 0 {
				err = errors.New(T("no log lines with this Call-ID"))
			}
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			path, err := v.app.SaveFile("log "+v.callid, ".log", out+"\n")
			if err != nil {
				v.app.Flash().Err(err)
				return
			}
			v.app.Flash().Infof("saved %d lines to %s", total, path)
		}
	})
}

// load reads the log: all of it the first time, then from the second of
// the last line shown on (the whole log again when the lines carry no
// time, as with a custom logCommand).
func (v *CallLogView) load() {
	if v.busy {
		return
	}
	v.busy = true
	c := v.app.Client()
	since, incremental := v.since, false
	if n := len(v.lines); n > 0 {
		if t, ok := logLineTime(v.lines[n-1]); ok {
			since, incremental = t.Unix(), true
		}
	}
	cmd := c.LogCommand(v.callid, since)
	v.app.Async(func(ctx context.Context) func() {
		out, total, err := c.CallLog(ctx, v.callid, since, maxLogLines)
		return func() {
			v.busy = false
			if err != nil {
				if len(v.lines) == 0 {
					v.SetError(errors.New(Tf("%s\n\ncommand: %s", err, cmd)))
				} else {
					v.app.Flash().Err(err)
				}
				return
			}
			first := len(v.lines) == 0
			var fresh []string
			if out != "" {
				fresh = strings.Split(out, "\n")
			}
			var added int
			v.lines, added = mergeLog(v.lines, fresh, incremental)
			if added < 0 { // a new log: count from this read
				v.total = total
			} else {
				v.total += added
			}
			if len(v.lines) > maxLogLines {
				v.lines = v.lines[len(v.lines)-maxLogLines:]
			}
			if len(v.lines) == 0 {
				v.SetContent(Tf("No log lines with this Call-ID.\n\ncommand: %s\n\nIf rtpengine logs elsewhere, set logCommand for the host in the config.", cmd), "plain")
				return
			}
			atEnd := first || v.atEnd()
			v.setTitle()
			v.SetContent(strings.Join(v.lines, "\n"), "log")
			if atEnd {
				v.Text().ScrollToEnd()
			}
		}
	})
}

// atEnd reports whether the last line is on screen: only then new lines
// scroll the view.
func (v *CallLogView) atEnd() bool {
	row, _ := v.Text().GetScrollOffset()
	_, _, _, h := v.Text().GetInnerRect()
	return row+h >= v.Text().GetWrappedLineCount()
}

// mergeLog adds the lines of a new read to the shown ones. A read repeats
// what is shown (the whole log, or from the second of the last line on),
// so it continues after the last shown line. Without it in the read, an
// incremental read is all new, a full one replaces the log (rotated).
// added is the number of lines added, -1 when the read replaced the log.
func mergeLog(shown, read []string, incremental bool) (merged []string, added int) {
	if len(shown) == 0 {
		return read, -1
	}
	last := shown[len(shown)-1]
	for i := len(read) - 1; i >= 0; i-- {
		if read[i] == last {
			return append(shown, read[i+1:]...), len(read) - i - 1
		}
	}
	if incremental {
		return append(shown, read...), len(read)
	}
	return read, -1
}

// logLineTime is the time in front of a line of journalctl -o short-iso-precise.
func logLineTime(line string) (time.Time, bool) {
	f, _, ok := strings.Cut(line, " ")
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(logTimeLayout, f)
	return t, err == nil
}
