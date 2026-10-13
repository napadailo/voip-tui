package rtpengine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/napadailo/voip-tui/i18n"
)

// commandMatrix lists CLI (listen-cli) commands by the release that added
// them. Extracted from daemon/cli.c of the sipwise/rtpengine release branches
// mr6.5 … mr13.3 and master; nothing was removed in that range.
var commandMatrix = []struct {
	Path  string
	Since mr
	Help  string
}{
	{"list numsessions", mr{6, 5}, "number of own/foreign sessions"},
	{"list sessions", mr{6, 5}, "list sessions (all|own|foreign) or one call"},
	{"list totals", mr{6, 5}, "total statistics"},
	{"list counters", mr{6, 5}, "per-second counters"},
	{"list maxopenfiles", mr{6, 5}, "allowed open files"},
	{"list maxsessions", mr{6, 5}, "session limit"},
	{"list maxcpu", mr{6, 5}, "CPU limit"},
	{"list maxload", mr{6, 5}, "load average limit"},
	{"list maxbw", mr{6, 5}, "bandwidth limit"},
	{"list timeout", mr{6, 5}, "RTP timeout"},
	{"list silenttimeout", mr{6, 5}, "silent timeout"},
	{"list offertimeout", mr{6, 5}, "offer timeout"},
	{"list finaltimeout", mr{6, 5}, "final timeout"},
	{"list loglevel", mr{6, 5}, "log level"},
	{"list redisallowederrors", mr{6, 5}, "redis allowed errors"},
	{"list redisdisabletime", mr{6, 5}, "redis disable time"},
	{"list redisconnecttimeout", mr{6, 5}, "redis connect timeout"},
	{"list rediscmdtimeout", mr{6, 5}, "redis command timeout"},
	{"list controltos", mr{6, 5}, "TOS of control packets"},
	{"set maxopenfiles", mr{6, 5}, ""},
	{"set maxsessions", mr{6, 5}, ""},
	{"set maxcpu", mr{6, 5}, ""},
	{"set maxload", mr{6, 5}, ""},
	{"set maxbw", mr{6, 5}, ""},
	{"set timeout", mr{6, 5}, ""},
	{"set silenttimeout", mr{6, 5}, ""},
	{"set offertimeout", mr{6, 5}, ""},
	{"set finaltimeout", mr{6, 5}, ""},
	{"set loglevel", mr{6, 5}, ""},
	{"set redisallowederrors", mr{6, 5}, ""},
	{"set redisdisabletime", mr{6, 5}, ""},
	{"set redisconnecttimeout", mr{6, 5}, ""},
	{"set rediscmdtimeout", mr{6, 5}, ""},
	{"set controltos", mr{6, 5}, ""},
	{"terminate", mr{6, 5}, "terminate a call or all/own/foreign calls"},
	{"params", mr{6, 5}, "start/current/diff/revert configuration"},
	{"ksadd", mr{6, 5}, "subscribe to a redis keyspace"},
	{"ksrm", mr{6, 5}, "unsubscribe from a redis keyspace"},
	{"kslist", mr{6, 5}, "subscribed redis keyspaces"},
	{"list interfaces", mr{7, 5}, "local interfaces and port usage"},
	{"list transcoders", mr{7, 5}, "transcoding statistics"},
	{"list jsonstats", mr{8, 5}, "statistics as JSON"},
	{"active", mr{8, 5}, "set all sessions to owned"},
	{"standby", mr{8, 5}, "set all sessions to foreign"},
	{"list loglevels", mr{9, 5}, "per-subsystem log levels"},
	{"list deletedelay", mr{9, 5}, "delete delay"},
	{"set deletedelay", mr{9, 5}, ""},
	{"debug", mr{9, 5}, "per-call debug logging"},
	{"help", mr{9, 5}, "list sub-commands"},
	{"call", mr{10, 5}, "call <id> info|terminate|debug|tag"},
	{"set redisdisable", mr{11, 5}, ""},
	{"usage", mr{13, 2}, "full command list"},
	{"media", mr{13, 2}, "media player cache (transcoding builds)"},
}

// Caps is the set of CLI commands a host supports.
type Caps struct {
	cmds map[string]bool
	// Source says how the set was obtained: "release table" or "CLI help".
	Source string
}

// CapsForVersion derives commands from the release table. An unknown version
// gets the mr6.5 base set.
func CapsForVersion(v Version) Caps {
	c := Caps{cmds: map[string]bool{}, Source: "release table"}
	for _, e := range commandMatrix {
		if v.Known() && v.Compare(e.Since.Major, e.Since.Minor) >= 0 || !v.Known() && e.Since == (mr{6, 5}) {
			c.cmds[e.Path] = true
		}
	}
	return c
}

// capsFromHelp builds caps from `help`, `list help` and `set help` replies.
func capsFromHelp(top, list, set []string) Caps {
	c := Caps{cmds: map[string]bool{}, Source: "CLI help"}
	for _, t := range top {
		c.cmds[t] = true
	}
	for _, t := range list {
		c.cmds["list "+t] = true
	}
	for _, t := range set {
		c.cmds["set "+t] = true
	}
	c.cmds["help"] = true
	return c
}

// Has reports support for a command path such as "list jsonstats".
func (c Caps) Has(path string) bool { return c.cmds[path] }

// Check returns an UnsupportedError for missing commands.
func (c Caps) Check(path string, v Version) error {
	if c.Has(path) {
		return nil
	}
	return &UnsupportedError{Command: path, Version: v, Since: Since(path)}
}

// Commands returns supported command paths, sorted.
func (c Caps) Commands() []string {
	out := make([]string, 0, len(c.cmds))
	for k := range c.cmds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Floor estimates the oldest release that has all these commands.
func (c Caps) Floor() (Version, bool) {
	best := mr{}
	for _, e := range commandMatrix {
		if c.cmds[e.Path] && (e.Since.Major > best.Major || e.Since.Major == best.Major && e.Since.Minor > best.Minor) {
			best = e.Since
		}
	}
	if best.Major == 0 {
		return Version{}, false
	}
	return Version{Major: best.Major, Minor: best.Minor, Full: fmt.Sprintf("%d.%d", best.Major, best.Minor), Floor: true, Source: "CLI commands"}, true
}

// MatrixCommands lists all known CLI commands in release order.
func MatrixCommands() []string {
	out := make([]string, len(commandMatrix))
	for i, e := range commandMatrix {
		out[i] = e.Path
	}
	return out
}

// Since returns the release that added a command, or "".
func Since(path string) string {
	for _, e := range commandMatrix {
		if e.Path == path {
			return e.Since.String()
		}
	}
	return ""
}

// UnsupportedError explains which release introduced a command.
type UnsupportedError struct {
	Command string
	Version Version
	Since   string
}

func (e *UnsupportedError) Error() string {
	if e.Since == "" {
		return i18n.Tf("rtpengine %s has no CLI command %q", e.Version, e.Command)
	}
	return i18n.Tf("%q needs rtpengine %s+, host runs %s", e.Command, e.Since, e.Version)
}

// parseHelp extracts command names from a `help` reply in both the pre-13.2
// ("Available sub-commands at this level:" + tab-indented names) and the
// 13.2+ ("name params : description") layouts.
func parseHelp(out string) []string {
	var names []string
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "Available sub-commands") {
			continue
		}
		f := strings.Fields(t)
		names = append(names, strings.TrimSuffix(f[0], ":"))
	}
	return names
}
