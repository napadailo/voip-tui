package rtpengine

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/napadailo/voip-tui/i18n"
)

// LogLevel is the log level of one subsystem (`list loglevel`, mr9.5+) or,
// before mr9.5, the one level of the daemon (Name "").
type LogLevel struct {
	Name  string
	Level int
	Desc  string // from `list loglevels`
}

// syslogLevels name the levels rtpengine uses (syslog priorities).
var syslogLevels = []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}

// LevelName is the syslog name of a level: 6 is info, 7 debug.
func LevelName(l int) string {
	if l < 0 || l >= len(syslogLevels) {
		return ""
	}
	return syslogLevels[l]
}

// ParseLogLevels reads `list loglevel` ("core = 6" per subsystem, or one
// number before mr9.5) and `list loglevels` ("core - Everything that…").
func ParseLogLevels(levels, descs string) []LogLevel {
	desc := map[string]string{}
	for _, l := range strings.Split(descs, "\n") {
		if name, d, ok := strings.Cut(l, " - "); ok {
			desc[strings.TrimSpace(name)] = strings.TrimSpace(d)
		}
	}
	var out []LogLevel
	for _, l := range strings.Split(levels, "\n") {
		l = strings.TrimSpace(l)
		name, val, ok := strings.Cut(l, "=")
		if !ok {
			name, val = "", l
		}
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			continue
		}
		name = strings.TrimSpace(name)
		out = append(out, LogLevel{Name: name, Level: n, Desc: desc[name]})
	}
	return out
}

// LogLevels reads the log levels and, where the host has them, what the
// subsystems are.
func (c *Client) LogLevels(ctx context.Context) ([]LogLevel, error) {
	levels, err := c.Do(ctx, "list loglevel")
	if err != nil {
		return nil, err
	}
	var descs string
	if c.Has("list loglevels") {
		descs, _ = c.Do(ctx, "list loglevels")
	}
	return ParseLogLevels(levels, descs), nil
}

// MinLogLevel and MaxLogLevel bound `set loglevel`: 1 alert … 7 debug.
const MinLogLevel, MaxLogLevel = 1, 7

// SetLogLevel sets the level of one subsystem, or of all with sub "".
// rtpengine checks neither from mr9.5 on: an unknown subsystem changes
// nothing and still reports success, so both are checked here.
func (c *Client) SetLogLevel(ctx context.Context, sub string, level int) (string, error) {
	if level < MinLogLevel || level > MaxLogLevel {
		return "", errors.New(i18n.Tf("log level %d is out of range: 1 (alert) … 7 (debug)", level))
	}
	if sub != "" {
		if !c.Has("list loglevels") {
			return "", errors.New(i18n.Tf("%s has one log level for everything (per subsystem from mr9.5)", c.Info().Version.String()))
		}
		levels, err := c.LogLevels(ctx)
		if err != nil {
			return "", err
		}
		var names []string
		found := false
		for _, l := range levels {
			names = append(names, l.Name)
			found = found || l.Name == sub
		}
		if !found {
			return "", errors.New(i18n.Tf("no log subsystem %q, the host has: %s", sub, strings.Join(names, " ")))
		}
		return c.Set(ctx, "loglevel", sub+" "+strconv.Itoa(level))
	}
	return c.Set(ctx, "loglevel", strconv.Itoa(level))
}

// ParseSetLogLevel reads the value of `set loglevel`: "N" or "<sub> N".
func ParseSetLogLevel(value string) (sub string, level int, err error) {
	f := strings.Fields(value)
	switch len(f) {
	case 1:
		level, err = strconv.Atoi(f[0])
	case 2:
		sub = f[0]
		level, err = strconv.Atoi(f[1])
	default:
		err = errors.New(i18n.T("usage: set loglevel [subsystem] N"))
	}
	if err != nil && len(f) <= 2 {
		err = errors.New(i18n.T("usage: set loglevel [subsystem] N"))
	}
	return sub, level, err
}
