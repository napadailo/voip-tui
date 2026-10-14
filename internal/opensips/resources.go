package opensips

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/napadailo/voip-tui/i18n"
)

// Level is the semantic color of a cell; the UI maps it to the current theme.
type Level int

// Levels.
const (
	LevelNone Level = iota
	LevelOK
	LevelWarn
	LevelErr
	LevelDim
	LevelAccent
)

// Cell is one table cell.
type Cell struct {
	Text  string
	Level Level
}

// Row is one line of a resource table.
type Row struct {
	Key   string
	Cells []Cell
	Data  any // source object from the MI answer: describe view and action args
}

// Table is a fetched resource.
type Table struct {
	Columns []string
	Rows    []Row
}

// Action is a key bound to an operation on the row under the cursor (or marked rows).
type Action struct {
	Key   string // rune ("D", "T", "+") as shown in the header
	Label string
	Op    string
	// Args builds operation args from a row; nil means the action applies to the whole view.
	Args func(r *Row) Args
	// Verb names what the action does to this row ("enable", "disable"), for
	// actions whose effect depends on the state; nil: Label.
	Verb func(r *Row) string
}

// enableVerb is the verb of a toggle: disable what is on, enable the rest.
func enableVerb(on bool) string {
	if on {
		return i18n.N("disable")
	}
	return i18n.N("enable")
}

// Resource is a list view: what to ask OpenSIPS and how to show it.
type Resource struct {
	Name    string
	Aliases []string
	Title   string
	Help    string
	Op      string // operation the view cannot work without
	Fetch   func(ctx context.Context, c *Client, memo map[string]any) (*Table, error)
	Actions []Action
}

// Resources lists all views.
var Resources []*Resource

// FindResource looks a view up by name or alias.
func FindResource(name string) *Resource {
	for _, r := range Resources {
		if r.Name == name {
			return r
		}
		for _, a := range r.Aliases {
			if a == name {
				return r
			}
		}
	}
	return nil
}

func c(text string) Cell           { return Cell{Text: text} }
func cl(text string, l Level) Cell { return Cell{Text: text, Level: l} }
func cells(texts ...string) []Cell {
	out := make([]Cell, len(texts))
	for i, t := range texts {
		out[i] = c(t)
	}
	return out
}

// Duration formats seconds as short ages: 2d4h, 3h05m, 4m09s.
func Duration(sec int64) string {
	if sec < 0 {
		return "-"
	}
	d, h, m, s := sec/86400, sec%86400/3600, sec%3600/60, sec%60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd%dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

// Size formats bytes.
func Size(n int64) string {
	f := float64(n)
	for _, u := range []string{"B", "K", "M"} {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%.0fB", f)
			}
			return fmt.Sprintf("%.1f%s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1fG", f)
}

var uriUserRe = regexp.MustCompile(`^sips?:([^@;>]+)@`)

func uriUser(uri string) string {
	if m := uriUserRe.FindStringSubmatch(uri); m != nil {
		return m[1]
	}
	return uri
}

// StateCell colors common OpenSIPS states.
func StateCell(state string) Cell {
	low := strings.ToLower(state)
	switch {
	case strings.Contains(low, "inactive") || strings.Contains(low, "disabled") || low == "down" || low == "0":
		return cl(state, LevelErr)
	case strings.HasPrefix(low, "active") || low == "up" || low == "enabled" || low == "ok" || low == "1":
		return cl(state, LevelOK)
	case strings.Contains(low, "probing") || strings.Contains(low, "backup"):
		return cl(state, LevelWarn)
	}
	return c(state)
}

// Records flattens an MI answer into flat records: a nested list of objects is expanded and every
// record inherits the scalar fields of its parent ({"Group": "dr", "Identifiers": [{..}, {..}]}).
func Records(v any, inherited map[string]any) []map[string]any {
	switch x := v.(type) {
	case []any:
		var out []map[string]any
		for _, item := range x {
			switch item.(type) {
			case map[string]any, []any:
				out = append(out, Records(item, inherited)...)
			default:
				out = append(out, merge(inherited, map[string]any{"value": item}))
			}
		}
		return out
	case map[string]any:
		scalars := merge(inherited, nil)
		keys := sortedKeys(x)
		for _, k := range keys {
			switch x[k].(type) {
			case map[string]any, []any:
			default:
				scalars[k] = x[k]
			}
		}
		for _, k := range keys {
			if l, ok := x[k].([]any); ok && len(l) > 0 && allMaps(l) {
				return Records(l, scalars)
			}
		}
		if len(scalars) == 0 {
			return nil
		}
		return []map[string]any{scalars}
	}
	return []map[string]any{merge(inherited, map[string]any{"value": v})}
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func allMaps(l []any) bool {
	for _, x := range l {
		if _, ok := x.(map[string]any); !ok {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// GenericTable shows any MI answer as a table.
func GenericTable(v any) *Table {
	recs := Records(v, nil)
	var cols []string
	seen := map[string]bool{}
	for _, r := range recs {
		for _, k := range sortedKeys(r) {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	t := &Table{}
	for _, k := range cols {
		t.Columns = append(t.Columns, strings.ToUpper(k))
	}
	for i, r := range recs {
		row := Row{Key: strconv.Itoa(i), Data: r}
		for _, k := range cols {
			row.Cells = append(row.Cells, c(str(r[k])))
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func generic(op string) func(context.Context, *Client, map[string]any) (*Table, error) {
	return func(ctx context.Context, cl *Client, _ map[string]any) (*Table, error) {
		ans, err := cl.Op(ctx, op, nil)
		if err != nil {
			return nil, err
		}
		return GenericTable(ans), nil
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func fetchStats(ctx context.Context, cli *Client, memo map[string]any) (*Table, error) {
	ans, err := cli.Op(ctx, "stats.get", nil)
	if err != nil {
		return nil, err
	}
	stats := asMap(ans)
	now := time.Now()
	prev, _ := memo["values"].(map[string]int64)
	prevAt, _ := memo["at"].(time.Time)
	values := map[string]int64{}
	t := &Table{Columns: []string{i18n.N("GROUP"), i18n.N("NAME"), i18n.N("VALUE"), i18n.N("RATE/S")}}
	for _, k := range sortedKeys(stats) {
		group, name, _ := strings.Cut(k, ":")
		v := num(stats[k])
		values[k] = v
		rate := c("")
		if p, ok := prev[k]; ok && !prevAt.IsZero() {
			if r := float64(v-p) / now.Sub(prevAt).Seconds(); r != 0 {
				rate = cl(fmt.Sprintf("%.1f", r), LevelAccent)
			}
		}
		shown := str(stats[k])
		if group == "shmem" && strings.Contains(name, "size") {
			shown = Size(v)
		}
		t.Rows = append(t.Rows, Row{Key: k, Cells: []Cell{cl(group, LevelDim), c(name), c(shown), rate},
			Data: map[string]any{k: stats[k]}})
	}
	memo["values"], memo["at"] = values, now
	return t, nil
}

var dialogStates = map[int64]string{1: "unconfirmed", 2: "early", 3: "confirmed-na", 4: "confirmed", 5: "deleted"}

// DialogState is a dlg_list state by its OpenSIPS name, colored: confirmed
// is fine, the ones before it are on the way, deleted is over.
func DialogState(state int64) Cell {
	name := dialogStates[state]
	if name == "" {
		name = strconv.FormatInt(state, 10)
	}
	switch state {
	case 4:
		return cl(name, LevelOK)
	case 1, 2, 3:
		return cl(name, LevelWarn)
	}
	return cl(name, LevelErr)
}

// Str, Num, AsMap and AsList read the values of an MI answer.
func Str(v any) string           { return str(v) }
func Num(v any) int64            { return num(v) }
func AsMap(v any) map[string]any { return asMap(v) }
func AsList(v any) []any         { return asList(v) }

func fetchDialogs(ctx context.Context, cli *Client, _ map[string]any) (*Table, error) {
	ans, err := cli.Op(ctx, "dialog.list", nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	t := &Table{Columns: []string{i18n.N("ID"), i18n.N("STATE"), i18n.N("FROM"), i18n.N("TO"), i18n.N("DURATION"), i18n.N("CALL-ID")}}
	for _, x := range asList(asMap(ans)["Dialogs"]) {
		d := asMap(x)
		dur := "-"
		if start := num(d["timestart"]); start > 0 {
			dur = Duration(now - start)
		}
		t.Rows = append(t.Rows, Row{Key: str(d["ID"]), Data: d, Cells: []Cell{
			c(str(d["ID"])), DialogState(num(d["state"])), c(uriUser(str(d["from_uri"]))), c(uriUser(str(d["to_uri"]))), c(dur), c(str(d["callid"])),
		}})
	}
	return t, nil
}

func fetchContacts(ctx context.Context, cli *Client, _ map[string]any) (*Table, error) {
	ans, err := cli.Op(ctx, "ul.dump", nil)
	if err != nil {
		return nil, err
	}
	t := &Table{Columns: []string{i18n.N("AOR"), i18n.N("CONTACT"), i18n.N("EXPIRES"), i18n.N("RECEIVED"), i18n.N("USER-AGENT"), i18n.N("SOCKET")}}
	for _, dx := range asList(asMap(ans)["Domains"]) {
		dom := asMap(dx)
		table := str(dom["name"])
		if table == "" {
			table = "location"
		}
		for _, ax := range asList(dom["AORs"]) {
			aor := asMap(ax)
			for _, cx := range asList(aor["Contacts"]) {
				ct := asMap(cx)
				exp := c(orDash(str(ct["Expires"])))
				if e, err := strconv.ParseInt(str(ct["Expires"]), 10, 64); err == nil {
					exp = c(fmt.Sprintf("%ds", e))
					if e < 60 {
						exp.Level = LevelWarn
					}
				}
				data := merge(ct, map[string]any{"table": table, "AOR": aor["AOR"]})
				t.Rows = append(t.Rows, Row{Key: str(aor["AOR"]) + "|" + str(ct["Contact"]), Data: data, Cells: []Cell{
					c(str(aor["AOR"])), c(str(ct["Contact"])), exp, c(orDash(str(ct["Received"]))), c(str(ct["User-agent"])), c(str(ct["Socket"])),
				}})
			}
		}
	}
	return t, nil
}

func fetchProcesses(ctx context.Context, cli *Client, _ map[string]any) (*Table, error) {
	ans, err := cli.Op(ctx, "core.ps", nil)
	if err != nil {
		return nil, err
	}
	levels := map[string]any{}
	if cli.Supports("core.log_level").OK {
		if lv, err := cli.Op(ctx, "core.log_level", nil); err == nil {
			for _, p := range asList(asMap(lv)["Processes"]) {
				levels[str(asMap(p)["PID"])] = asMap(p)["Log level"]
			}
		}
	}
	t := &Table{Columns: []string{i18n.N("ID"), i18n.N("PID"), i18n.N("TYPE"), i18n.N("LOG LEVEL")}}
	for _, x := range asList(asMap(ans)["Processes"]) {
		p := asMap(x)
		pid := str(p["PID"])
		level := c("-")
		if l, ok := levels[pid]; ok {
			level = c(str(l))
			if num(l) > 3 {
				level.Level = LevelWarn
			}
		}
		t.Rows = append(t.Rows, Row{Key: pid, Data: merge(p, map[string]any{"Log level": levels[pid]}),
			Cells: []Cell{c(str(p["ID"])), c(pid), c(str(p["Type"])), level}})
	}
	return t, nil
}

func fetchDispatcher(ctx context.Context, cli *Client, _ map[string]any) (*Table, error) {
	ans, err := cli.Op(ctx, "ds.list", nil)
	if err != nil {
		return nil, err
	}
	root := asMap(ans)
	parts := asList(root["PARTITIONS"])
	if parts == nil {
		parts = asList(root["Partitions"])
	}
	if parts == nil {
		parts = []any{merge(root, map[string]any{"name": "default"})}
	}
	t := &Table{Columns: []string{i18n.N("PARTITION"), i18n.N("SET"), i18n.N("URI"), i18n.N("STATE"), i18n.N("WEIGHT"), i18n.N("PRIO"), i18n.N("ATTRS"), i18n.N("DESCRIPTION")}}
	for _, px := range parts {
		part := asMap(px)
		sets := asList(part["SETS"])
		if sets == nil {
			sets = asList(part["Sets"])
		}
		for _, sx := range sets {
			set := asMap(sx)
			for _, dx := range asList(set["Destinations"]) {
				d := asMap(dx)
				attrs := str(d["attributes"])
				if attrs == "" {
					attrs = str(d["attrs"])
				}
				data := merge(d, map[string]any{"partition": str(part["name"]), "set": str(set["id"])})
				t.Rows = append(t.Rows, Row{Key: str(part["name"]) + "/" + str(set["id"]) + "/" + str(d["URI"]), Data: data, Cells: []Cell{
					c(str(part["name"])), c(str(set["id"])), c(str(d["URI"])), StateCell(str(d["state"])),
					c(str(d["weight"])), c(str(d["priority"])), c(attrs), c(str(d["description"])),
				}})
			}
		}
	}
	return t, nil
}

func fetchGateways(ctx context.Context, cli *Client, _ map[string]any) (*Table, error) {
	partitions := []string{""}
	if cli.Supports("dr.reload_status").OK {
		st, err := cli.Op(ctx, "dr.reload_status", nil)
		if err != nil {
			return nil, err
		}
		if ps, ok := asMap(st)["Partitions"]; ok {
			partitions = nil
			for _, p := range asList(ps) {
				partitions = append(partitions, str(asMap(p)["name"]))
			}
		}
	}
	t := &Table{Columns: []string{i18n.N("PARTITION"), i18n.N("ID"), i18n.N("IP"), i18n.N("STATE"), i18n.N("ATTRS")}}
	for _, part := range partitions {
		ans, err := cli.Op(ctx, "dr.gw_status", Args{"partition": part})
		if err != nil {
			if e, ok := err.(*MIError); ok && e.Code == 404 { // partition exists, data not loaded
				continue
			}
			return nil, err
		}
		for _, gx := range asList(asMap(ans)["Gateways"]) {
			g := asMap(gx)
			t.Rows = append(t.Rows, Row{Key: part + "/" + str(g["ID"]), Data: merge(g, map[string]any{"partition": part}), Cells: []Cell{
				c(orDash(part)), c(str(g["ID"])), c(str(g["IP"])), StateCell(str(g["State"])), c(str(g["ATTRS"])),
			}})
		}
	}
	return t, nil
}

func fetchRTPEngine(ctx context.Context, cli *Client, _ map[string]any) (*Table, error) {
	ans, err := cli.Op(ctx, "rtpengine.show", nil)
	if err != nil {
		return nil, err
	}
	t := &Table{Columns: []string{i18n.N("SET"), i18n.N("URL"), i18n.N("STATE"), i18n.N("WEIGHT"), i18n.N("RECHECK")}}
	for _, sx := range asList(ans) {
		set := asMap(sx)
		for _, nx := range asList(set["Nodes"]) {
			n := asMap(nx)
			state := cl("enabled", LevelOK)
			if num(n["disabled"]) != 0 {
				state = cl("disabled", LevelErr)
			}
			t.Rows = append(t.Rows, Row{Key: str(set["Set"]) + "/" + str(n["url"]), Data: merge(n, map[string]any{"Set": set["Set"]}), Cells: []Cell{
				c(str(set["Set"])), c(str(n["url"])), state, c(str(n["weight"])), c(str(n["recheck_ticks"])),
			}})
		}
	}
	return t, nil
}

func fetchClusterer(ctx context.Context, cli *Client, _ map[string]any) (*Table, error) {
	ans, err := cli.Op(ctx, "clusterer.list", nil)
	if err != nil {
		return nil, err
	}
	t := &Table{Columns: []string{i18n.N("CLUSTER"), i18n.N("NODE"), i18n.N("URL"), i18n.N("LINK"), i18n.N("STATE"), i18n.N("NEXT HOP"), i18n.N("DESCRIPTION")}}
	for _, cx := range asList(asMap(ans)["Clusters"]) {
		cluster := asMap(cx)
		for _, nx := range asList(cluster["Nodes"]) {
			n := asMap(nx)
			t.Rows = append(t.Rows, Row{Key: str(cluster["cluster_id"]) + "/" + str(n["node_id"]), Data: merge(n, map[string]any{"cluster_id": cluster["cluster_id"]}), Cells: []Cell{
				c(str(cluster["cluster_id"])), c(str(n["node_id"])), c(str(n["url"])), StateCell(str(n["link_state"])),
				StateCell(str(n["state"])), c(str(n["next_hop"])), c(str(n["description"])),
			}})
		}
	}
	return t, nil
}

func fetchShtags(ctx context.Context, cli *Client, _ map[string]any) (*Table, error) {
	ans, err := cli.Op(ctx, "clusterer.shtags", nil)
	if err != nil {
		return nil, err
	}
	t := &Table{Columns: []string{i18n.N("TAG"), i18n.N("CLUSTER"), i18n.N("STATE")}}
	for _, x := range asList(ans) {
		s := asMap(x)
		t.Rows = append(t.Rows, Row{Key: str(s["Cluster"]) + "/" + str(s["Tag"]), Data: s,
			Cells: []Cell{c(str(s["Tag"])), c(str(s["Cluster"])), StateCell(str(s["State"]))}})
	}
	return t, nil
}

func fetchCommands(_ context.Context, cli *Client, _ map[string]any) (*Table, error) {
	t := &Table{Columns: []string{i18n.N("MI COMMAND")}}
	h := cli.Info()
	var names []string
	for k := range h.Commands {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		t.Rows = append(t.Rows, Row{Key: n, Cells: cells(n), Data: map[string]any{"command": n}})
	}
	return t, nil
}

func field(r *Row, k string) any { return asMap(r.Data)[k] }

func logLevel(delta int64) func(*Row) Args {
	return func(r *Row) Args {
		level := int64(3)
		if l := field(r, "Log level"); l != nil {
			level = num(l)
		}
		level = min(4, max(-3, level+delta))
		return Args{"pid": str(field(r, "PID")), "level": level}
	}
}

func init() {
	Resources = []*Resource{
		{Name: "allstats", Aliases: []string{"counters"}, Title: i18n.N("All statistics"), Help: i18n.N("get_statistics all as a table, rates between refreshes; x resets one"),
			Op: "stats.get", Fetch: fetchStats, Actions: []Action{
				{Key: "x", Label: i18n.N("reset"), Op: "stats.reset", Args: func(r *Row) Args { return Args{"name": r.Key} }},
			}},
		{Name: "dialogs", Aliases: []string{"dlg", "dialog", "calls"}, Title: i18n.N("Dialogs"), Help: i18n.N("dlg_list"), Op: "dialog.list", Fetch: fetchDialogs,
			Actions: []Action{
				{Key: "D", Label: i18n.N("end dialog"), Op: "dialog.end", Args: func(r *Row) Args { return Args{"id": str(field(r, "ID"))} }},
			}},
		{Name: "contacts", Aliases: []string{"ul", "usrloc", "regs", "location"}, Title: i18n.N("Contacts"), Help: i18n.N("ul_dump"), Op: "ul.dump", Fetch: fetchContacts,
			Actions: []Action{
				{Key: "D", Label: i18n.N("remove contact"), Op: "ul.rm_contact", Args: func(r *Row) Args {
					return Args{"table": str(field(r, "table")), "aor": str(field(r, "AOR")), "contact": str(field(r, "Contact"))}
				}},
				{Key: "X", Label: i18n.N("remove AOR"), Op: "ul.rm", Args: func(r *Row) Args {
					return Args{"table": str(field(r, "table")), "aor": str(field(r, "AOR"))}
				}},
			}},
		{Name: "processes", Aliases: []string{"ps", "proc"}, Title: i18n.N("Processes"), Help: i18n.N("ps with log levels"), Op: "core.ps", Fetch: fetchProcesses,
			Actions: []Action{
				{Key: "+", Label: i18n.N("log level +1"), Op: "core.set_log_level", Args: logLevel(1)},
				{Key: "-", Label: i18n.N("log level -1"), Op: "core.set_log_level", Args: logLevel(-1)},
			}},
		{Name: "dispatcher", Aliases: []string{"ds"}, Title: i18n.N("Dispatcher"), Help: i18n.N("ds_list"), Op: "ds.list", Fetch: fetchDispatcher,
			Actions: []Action{
				{Key: "T", Label: i18n.N("toggle state"), Op: "ds.set_state", Args: func(r *Row) Args {
					state := "a"
					if strings.HasPrefix(strings.ToLower(str(field(r, "state"))), "active") {
						state = "i"
					}
					part := str(field(r, "partition"))
					if part == "default" {
						part = ""
					}
					return Args{"state": state, "set": str(field(r, "set")), "uri": str(field(r, "URI")), "partition": part}
				}, Verb: func(r *Row) string {
					return enableVerb(strings.HasPrefix(strings.ToLower(str(field(r, "state"))), "active"))
				}},
				{Key: "R", Label: i18n.N("ds_reload"), Op: "ds.reload"},
			}},
		{Name: "gateways", Aliases: []string{"gw", "dr", "drouting"}, Title: i18n.N("Gateways"), Help: i18n.N("drouting gateways (dr_gw_status)"), Op: "dr.gw_status", Fetch: fetchGateways,
			Actions: []Action{
				{Key: "T", Label: i18n.N("toggle state"), Op: "dr.gw_set", Args: func(r *Row) Args {
					status := 1
					if str(field(r, "State")) == "Active" {
						status = 0
					}
					return Args{"partition": str(field(r, "partition")), "id": str(field(r, "ID")), "status": status}
				}, Verb: func(r *Row) string { return enableVerb(str(field(r, "State")) == "Active") }},
				{Key: "R", Label: i18n.N("dr_reload"), Op: "dr.reload"},
			}},
		{Name: "rtpengine", Aliases: []string{"rtp"}, Title: i18n.N("RTPengine"), Help: i18n.N("rtpengine_show"), Op: "rtpengine.show", Fetch: fetchRTPEngine,
			Actions: []Action{
				{Key: "T", Label: i18n.N("toggle"), Op: "rtpengine.enable", Args: func(r *Row) Args {
					enable := 0
					if num(field(r, "disabled")) != 0 {
						enable = 1
					}
					return Args{"url": str(field(r, "url")), "enable": enable}
				}, Verb: func(r *Row) string { return enableVerb(num(field(r, "disabled")) == 0) }},
			}},
		{Name: "clusterer", Aliases: []string{"cl", "cluster"}, Title: i18n.N("Clusterer"), Help: i18n.N("clusterer_list"), Op: "clusterer.list", Fetch: fetchClusterer},
		{Name: "shtags", Aliases: []string{"sharing", "tags"}, Title: i18n.N("Sharing tags"), Help: i18n.N("clusterer_list_shtags"), Op: "clusterer.shtags", Fetch: fetchShtags},
		{Name: "status", Aliases: []string{"sr"}, Title: i18n.N("Status/Report"), Help: i18n.N("sr_list_status (3.2+)"), Op: "sr.list_status", Fetch: generic("sr.list_status")},
		{Name: "tcp", Aliases: []string{"conns"}, Title: i18n.N("TCP connections"), Help: i18n.N("list_tcp_conns"), Op: "tcp.list", Fetch: generic("tcp.list")},
		{Name: "commands", Aliases: []string{"mi", "which", "cmd"}, Title: i18n.N("MI commands"), Help: i18n.N("MI which; enter runs a command"),
			Op: "core.which", Fetch: fetchCommands},
	}
}

// Stat finds a statistic of a get_statistics answer by its name without
// the group ("active_dialogs" for "dialog:active_dialogs").
func Stat(ans any, name string) (int64, bool) {
	for k, v := range asMap(ans) {
		if _, short, _ := strings.Cut(k, ":"); short == name {
			return num(v), true
		}
	}
	return 0, false
}

// ActiveDialogs is dialog:active_dialogs of the host.
func (c *Client) ActiveDialogs(ctx context.Context) (int64, error) {
	ans, err := c.Op(ctx, "stats.get", Args{"names": []string{"dialog:"}})
	if err != nil {
		return 0, err
	}
	n, ok := Stat(ans, "active_dialogs")
	if !ok {
		return 0, errors.New(i18n.T("no dialog statistics: is the dialog module loaded?"))
	}
	return n, nil
}

// ProcLevel is the log level of one OpenSIPS process.
type ProcLevel struct {
	PID, Type string
	Level     int64
}

// ProcessLevels reads the log level of every process (MI log_level).
func (c *Client) ProcessLevels(ctx context.Context) ([]ProcLevel, error) {
	ans, err := c.Op(ctx, "core.log_level", nil)
	if err != nil {
		return nil, err
	}
	var out []ProcLevel
	for _, x := range asList(asMap(ans)["Processes"]) {
		p := asMap(x)
		out = append(out, ProcLevel{PID: str(p["PID"]), Type: str(p["Type"]), Level: num(p["Log level"])})
	}
	return out, nil
}

// LogLevel is a log level and how many processes run with it.
type LogLevel struct {
	Level     int64
	Processes int
}

// LogLevels groups the processes of the host by log level.
func LogLevels(procs []ProcLevel) []LogLevel {
	count := map[int64]int{}
	for _, p := range procs {
		count[p.Level]++
	}
	var out []LogLevel
	for l, n := range count {
		out = append(out, LogLevel{Level: l, Processes: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Processes > out[j].Processes })
	return out
}

// SetLogLevel sets the log level of one process, of all with pid "".
func (c *Client) SetLogLevel(ctx context.Context, pid string, level int64) error {
	_, err := c.Op(ctx, "core.set_log_level", Args{"pid": pid, "level": level})
	return err
}

// XlogLevel reads the level of xlog() calls (MI xlog_level).
func (c *Client) XlogLevel(ctx context.Context) (int64, error) {
	ans, err := c.Op(ctx, "core.xlog_level", nil)
	if err != nil {
		return 0, err
	}
	// {"xLog Level": 2}; the key is not the same in every release
	for _, v := range asMap(ans) {
		return num(v), nil
	}
	return num(ans), nil
}

// SetXlogLevel sets the level of xlog() calls.
func (c *Client) SetXlogLevel(ctx context.Context, level int64) error {
	_, err := c.Op(ctx, "core.set_xlog_level", Args{"level": level})
	return err
}

// MinLogLevel and MaxLogLevel bound the OpenSIPS log levels: alert to debug.
const (
	MinLogLevel = -3
	MaxLogLevel = 4
)

// LevelName is the OpenSIPS name of a log level.
func LevelName(l int64) string {
	switch {
	case l <= -3:
		return "alert"
	case l == -2:
		return "crit"
	case l == -1:
		return "err"
	case l == 0, l == 1:
		return "warn"
	case l == 2:
		return "notice"
	case l == 3:
		return "info"
	}
	return "debug"
}

// StatValues reads a get_statistics answer: "group:name" to its value.
func StatValues(ans any) map[string]int64 {
	out := map[string]int64{}
	for k, v := range asMap(ans) {
		out[k] = num(v)
	}
	return out
}

// sipMethods are the bits of a contact's methods, as METHOD_* in
// parser/msg_parser.h.
var sipMethods = []string{"INVITE", "CANCEL", "ACK", "BYE", "INFO", "OPTIONS", "UPDATE", "REGISTER",
	"MESSAGE", "SUBSCRIBE", "NOTIFY", "PRACK", "REFER", "PUBLISH", "OTHER"}

// Methods reads the methods mask of a contact (ul_dump "Methods"): what its
// Contact methods parameter or the Allow header of the REGISTER listed.
// all is true for ALL_METHODS, set when the UA sent neither.
func Methods(mask int64) (names []string, all bool) {
	if mask == -1 || uint32(mask) == 0xFFFFFFFF {
		return nil, true
	}
	for i, m := range sipMethods {
		if mask&(1<<i) != 0 {
			names = append(names, m)
		}
	}
	return names, false
}

// ContactFlag is a flag of a usrloc contact (ul_dump "Flags"), as in
// modules/usrloc/ucontact.h.
type ContactFlag struct {
	Bit  int64
	Name string
}

var contactFlags = []ContactFlag{{1, "FL_MEM"}, {2, "FL_EXTRA_HOP"}, {4, "FL_PN_ON"}}

// ContactFlags splits the flags of a contact; rest are bits it does not know.
func ContactFlags(f int64) (flags []ContactFlag, rest int64) {
	for _, cf := range contactFlags {
		if f&cf.Bit != 0 {
			flags = append(flags, cf)
			f &^= cf.Bit
		}
	}
	return flags, f
}
