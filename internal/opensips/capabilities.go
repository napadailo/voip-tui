package opensips

import (
	"fmt"
	"strings"

	"github.com/napadailo/voip-tui/i18n"
)

// Views and actions never call MI commands directly. They use operations
// ("dialog.end", "ul.rm_contact") that know which MI command and parameters
// implement them in each version range. On top of the version, an operation is
// checked against the host's MI "which" list: a module that is not loaded has
// no commands there, and the view says so.
//
// To support a new version difference add a Variant with the right range.

// MinCLIVersion: opensips-cli speaks JSON-RPC MI, which appeared in OpenSIPS 3.0.
// 2.x has a text MI, only opensipsctl fifo works there.
var MinCLIVersion = V("3.0")

// Params are MI parameters: []string positional or map[string]string named.
type Params any

// Args are operation arguments taken from a table row.
type Args map[string]any

// Variant implements an operation for a version range [Since, Until).
type Variant struct {
	Command string
	Params  func(Args) Params
	Since   Version
	Until   Version // zero = open-ended
}

func (v Variant) matches(ver Version) bool {
	return !ver.Less(v.Since) && (v.Until.IsZero() || ver.Less(v.Until))
}

// Operation is a logical MI action.
type Operation struct {
	Name     string
	Title    string
	Variants []Variant
	// Mutating operations change OpenSIPS state: hidden in read-only mode, always confirmed.
	Mutating bool
}

// Resolve picks the variant for a version.
func (o *Operation) Resolve(ver Version) *Variant {
	if ver.IsZero() {
		return nil
	}
	for i := range o.Variants {
		if o.Variants[i].matches(ver) {
			return &o.Variants[i]
		}
	}
	return nil
}

// Support is the result of a capability check.
type Support struct {
	OK      bool
	Reason  string
	Variant *Variant
}

func none(Args) Params { return nil }

// named maps operation args to named MI params, skipping nil/empty ones.
func named(mapping ...string) func(Args) Params {
	return func(a Args) Params {
		out := map[string]string{}
		for i := 0; i+1 < len(mapping); i += 2 {
			if v, ok := a[mapping[i+1]]; ok && v != nil && fmt.Sprint(v) != "" {
				out[mapping[i]] = fmt.Sprint(v)
			}
		}
		return out
	}
}

func one(name, title, command string, params func(Args) Params, since string, mutating bool) *Operation {
	if params == nil {
		params = none
	}
	return &Operation{Name: name, Title: title, Mutating: mutating,
		Variants: []Variant{{Command: command, Params: params, Since: V(since)}}}
}

// Operations is the capability table.
var Operations = map[string]*Operation{}

func init() {
	for _, op := range []*Operation{
		one("core.version", i18n.N("version"), "version", nil, "3.0", false),
		one("core.uptime", i18n.N("uptime"), "uptime", nil, "3.0", false),
		one("core.which", i18n.N("MI commands"), "which", nil, "3.0", false),
		one("core.ps", i18n.N("processes"), "ps", nil, "3.0", false),
		one("core.log_level", i18n.N("log levels"), "log_level", nil, "3.0", false),
		one("core.set_log_level", i18n.N("process log level"), "log_level", named("level", "level", "pid", "pid"), "3.0", true),
		// xlog_level is the level of xlog() calls in the script, one for all processes.
		one("core.xlog_level", i18n.N("xlog level"), "xlog_level", nil, "3.0", false),
		one("core.set_xlog_level", i18n.N("xlog level change"), "xlog_level", named("level", "level"), "3.0", true),
		// get_statistics takes a "statistics" array in 3.x; opensips-cli builds it from positional
		// args. A name with its group ("dialog:active_dialogs") returns nothing, a whole group is "dialog:".
		one("stats.get", i18n.N("statistics"), "get_statistics", func(a Args) Params {
			if names, ok := a["names"].([]string); ok && len(names) > 0 {
				return names
			}
			return []string{"all"}
		}, "3.0", false),
		one("stats.reset", i18n.N("reset statistic"), "reset_statistics", func(a Args) Params { return []string{fmt.Sprint(a["name"])} }, "3.0", true),
		one("dialog.list", i18n.N("dialogs"), "dlg_list", nil, "3.0", false),
		{Name: "dialog.end", Title: i18n.N("end dialog"), Mutating: true, Variants: []Variant{
			// Since 3.0 a dialog is addressed by one dialog_id: the numeric ID from dlg_list or the Call-ID.
			{Command: "dlg_end_dlg", Params: named("dialog_id", "id"), Since: MinCLIVersion},
			// 2.x takes the h_entry/h_id pair; only reachable through opensipsctl fifo.
			{Command: "dlg_end_dlg", Params: func(a Args) Params {
				return []string{fmt.Sprint(a["h_entry"]), fmt.Sprint(a["h_id"])}
			}, Since: V("1.11"), Until: MinCLIVersion},
		}},
		one("ul.dump", i18n.N("registrations"), "ul_dump", nil, "3.0", false),
		one("ul.rm_contact", i18n.N("remove contact"), "ul_rm_contact", named("table_name", "table", "aor", "aor", "contact", "contact"), "3.0", true),
		one("ul.rm", i18n.N("remove AOR"), "ul_rm", named("table_name", "table", "aor", "aor"), "3.0", true),
		one("ds.list", i18n.N("dispatcher"), "ds_list", nil, "3.0", false),
		one("ds.set_state", i18n.N("dispatcher destination state"), "ds_set_state",
			named("state", "state", "group", "set", "address", "uri", "partition", "partition"), "3.0", true),
		one("ds.reload", i18n.N("dispatcher reload"), "ds_reload", named("partition", "partition"), "3.0", true),
		one("dr.reload_status", i18n.N("drouting partitions"), "dr_reload_status", nil, "3.0", false),
		one("dr.gw_status", i18n.N("drouting gateways"), "dr_gw_status", named("partition_name", "partition"), "3.0", false),
		one("dr.gw_set", i18n.N("drouting gateway state"), "dr_gw_status",
			named("partition_name", "partition", "gw_id", "id", "status", "status"), "3.0", true),
		one("dr.reload", i18n.N("drouting reload"), "dr_reload", named("partition_name", "partition"), "3.0", true),
		one("rtpengine.show", i18n.N("rtpengine"), "rtpengine_show", nil, "3.0", false),
		one("rtpengine.enable", i18n.N("rtpengine enable/disable"), "rtpengine_enable", named("url", "url", "enable", "enable"), "3.0", true),
		one("clusterer.list", i18n.N("clusterer"), "clusterer_list", nil, "3.0", false),
		one("clusterer.shtags", i18n.N("sharing tags"), "clusterer_list_shtags", nil, "3.0", false),
		// The status/report framework appeared in 3.2.
		one("sr.list_status", i18n.N("status/report"), "sr_list_status", nil, "3.2", false),
		one("tcp.list", i18n.N("TCP connections"), "list_tcp_conns", nil, "3.0", false),
	} {
		Operations[op.Name] = op
	}
}

// Check tells whether an operation can run on a host.
func Check(name string, ver Version, commands map[string]bool, readOnly bool) Support {
	op, ok := Operations[name]
	if !ok {
		return Support{Reason: i18n.Tf("unknown operation %s", name)}
	}
	if readOnly && op.Mutating {
		return Support{Reason: i18n.T("read-only mode")}
	}
	v := op.Resolve(ver)
	if v == nil {
		if ver.IsZero() {
			return Support{Reason: i18n.T("OpenSIPS version is unknown")}
		}
		var ranges []string
		for _, x := range op.Variants {
			if x.Until.IsZero() {
				ranges = append(ranges, x.Since.Branch()+"+")
			} else {
				ranges = append(ranges, x.Since.Branch()+"-"+x.Until.Branch())
			}
		}
		return Support{Reason: i18n.Tf("%s needs OpenSIPS %s, host runs %s", i18n.T(op.Title), strings.Join(ranges, ", "), ver)}
	}
	if commands != nil && !commands[v.Command] {
		return Support{Reason: i18n.Tf("no MI command %s on the host: module not loaded", v.Command)}
	}
	return Support{OK: true, Variant: v}
}

// ProfileNotes describes what the version implies, for the info view.
func ProfileNotes(ver Version) []string {
	switch {
	case ver.IsZero():
		return []string{i18n.T("version unknown: operations are checked against MI which only")}
	case ver.Less(MinCLIVersion):
		return []string{i18n.T("MI before 3.0 is text, opensips-cli does not support it: only :mi through opensipsctl fifo")}
	}
	notes := []string{i18n.T("MI JSON-RPC through opensips-cli"), i18n.T("dlg_end_dlg by dialog_id (3.0+)")}
	if ver.Less(V("3.2")) {
		notes = append(notes, i18n.T("no status/report (appeared in 3.2)"))
	} else {
		notes = append(notes, i18n.T("status/report: sr_list_status (3.2+)"))
	}
	return notes
}
