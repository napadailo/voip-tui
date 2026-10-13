package rtpengine

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/napadailo/voip-tui/i18n"
)

// Daemon selects which option table a config file is checked against.
type Daemon int

const (
	DaemonRTPEngine Daemon = iota // rtpengine.conf, section [rtpengine]
	DaemonRecording               // rtpengine-recording.conf, section [rtpengine-recording]
)

func (d Daemon) String() string {
	if d == DaemonRecording {
		return "rtpengine-recording"
	}
	return "rtpengine"
}

// DefaultPath is where the packages install the config file.
func (d Daemon) DefaultPath() string { return "/etc/rtpengine/" + d.String() + ".conf" }

// Entry is one key = value line of a GLib key file.
type Entry struct {
	Section string
	Key     string
	Value   string
	Line    int
	// Overridden is set when a later line in the same section sets the key
	// again; GLib keeps the last one.
	Overridden bool
}

// ConfFile is a parsed rtpengine key file.
type ConfFile struct {
	Path     string
	Sections []string
	Entries  []Entry
}

// ParseConf parses GLib key-file syntax as rtpengine reads it: [section]
// headers, key = value, # comments. Lists stay as one ";"-separated string.
func ParseConf(path, text string) (*ConfFile, error) {
	cf := &ConfFile{Path: path}
	section := ""
	last := map[string]int{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return cf, fmt.Errorf(i18n.T("%s:%d: bad section header %q"), path, i+1, line)
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			cf.Sections = append(cf.Sections, section)
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return cf, fmt.Errorf(i18n.T("%s:%d: expected key = value, got %q"), path, i+1, line)
		}
		if section == "" {
			return cf, fmt.Errorf(i18n.T("%s:%d: key %q outside of any [section]"), path, i+1, strings.TrimSpace(k))
		}
		e := Entry{Section: section, Key: strings.TrimSpace(k), Value: strings.TrimSpace(v), Line: i + 1}
		id := section + "\x00" + e.Key
		if j, ok := last[id]; ok {
			cf.Entries[j].Overridden = true
		}
		last[id] = len(cf.Entries)
		cf.Entries = append(cf.Entries, e)
	}
	return cf, nil
}

// Get returns the effective value of key in section.
func (cf *ConfFile) Get(section, key string) (string, bool) {
	if cf == nil {
		return "", false
	}
	for i := len(cf.Entries) - 1; i >= 0; i-- {
		if e := cf.Entries[i]; e.Section == section && e.Key == key {
			return e.Value, true
		}
	}
	return "", false
}

// Status classifies a config entry.
type Status int

const (
	StatusOK Status = iota
	StatusDiffers
	StatusOverridden
	StatusOtherSection
	StatusNotInVersion
	StatusUnknown
)

func (s Status) String() string {
	return [...]string{i18n.N("ok"), i18n.N("runtime differs"), i18n.N("overridden"), i18n.N("section"), i18n.N("not in version"), i18n.N("unknown")}[s]
}

// Setting is an analysed config entry for display.
type Setting struct {
	Entry
	File    string
	Daemon  Daemon
	Role    string // main, interface, templates, other
	Runtime string // value from `params current`, rtpengine.conf only
	Status  Status
	Note    string
	Desc    string
	Arg     string
	// Template is the parsed entry of the templates section.
	Template *Template
}

// Analyze checks entries against the option table of the host version and,
// for the media daemon, against the running configuration.
//
// section is the daemon's config section (--config-section, default named
// after the daemon); runtime may be nil.
func Analyze(cf *ConfFile, d Daemon, section string, v Version, runtime map[string]string) []Setting {
	if section == "" {
		section = d.String()
	}
	ifPrefix, _ := cf.Get(section, "interfaces-config")
	templates, _ := cf.Get(section, "templates")
	var out []Setting
	for _, e := range cf.Entries {
		s := Setting{Entry: e, File: cf.Path, Daemon: d, Role: i18n.N("other")}
		switch {
		case e.Section == section:
			s.Role = i18n.N("main")
			analyzeOption(&s, v)
			if runtime != nil && s.Status == StatusOK {
				if rv, ok := runtime[e.Key]; ok {
					s.Runtime = rv
					// Only scalars compare reliably: addresses and lists are
					// printed in a different form at runtime.
					if scalar(e.Value) && scalar(rv) && !sameValue(e.Value, rv) {
						s.Status, s.Note = StatusDiffers, i18n.T("changed at runtime (CLI set/params) or not reloaded")
					}
				}
			}
		case ifPrefix != "" && strings.HasPrefix(e.Section, ifPrefix):
			s.Role, s.Status, s.Note = i18n.N("interface"), StatusOtherSection, i18n.Tf("interface section (%s*)", ifPrefix)
		case templates != "" && e.Section == templates:
			s.Role, s.Status = i18n.N("templates"), StatusOtherSection
			analyzeTemplate(&s, v)
		default:
			s.Status, s.Note = StatusOtherSection, i18n.Tf("not read by this daemon (section [%s] is used)", section)
		}
		if e.Overridden {
			s.Status, s.Note = StatusOverridden, i18n.T("set again further down, the later line wins")
		}
		out = append(out, s)
	}
	return out
}

// analyzeTemplate explains a signalling template and reports flags the host
// does not know.
func analyzeTemplate(s *Setting, v Version) {
	t := ParseTemplate(s.Key, s.Value, v)
	s.Template = &t
	s.Desc = i18n.Tf("signalling template, %s", TemplateUse(s.Key))
	if st, note := TemplateFindings(t, v); st != StatusOK {
		s.Status, s.Note = st, note
	}
}

// TemplateFindings sums up what is wrong with a parsed template on the
// host's release: StatusOK, StatusUnknown or StatusNotInVersion with a note.
func TemplateFindings(t Template, v Version) (Status, string) {
	if v.Known() && !v.AtLeast(13, 2) {
		return StatusNotInVersion, i18n.Tf("signalling templates need mr13.2+, host runs %s", v.Short())
	}
	var unknown, missing []string
	for _, it := range t.Items {
		bad := it.Status
		for _, val := range it.Values {
			bad = max(bad, val.Status)
		}
		switch bad {
		case StatusUnknown:
			unknown = append(unknown, it.Token)
		case StatusNotInVersion:
			missing = append(missing, it.Token)
		}
	}
	switch {
	case len(unknown) > 0:
		return StatusUnknown, i18n.Tf("unknown to rtpengine: %s", strings.Join(unknown, " "))
	case len(missing) > 0:
		return StatusNotInVersion, i18n.Tf("not in %s: %s", v.Short(), strings.Join(missing, " "))
	}
	return StatusOK, ""
}

func analyzeOption(s *Setting, v Version) {
	table := daemonOptions
	if s.Daemon == DaemonRecording {
		table = recordingOptions
	}
	spec, ok := table[s.Key]
	if !ok && strings.HasPrefix(s.Key, "log-level-") {
		// per-subsystem levels are generated at runtime (mr9.5+)
		s.Desc, s.Arg = i18n.Tf("log level of the %s subsystem", strings.TrimPrefix(s.Key, "log-level-")), "int"
		if v.Known() && !v.AtLeast(9, 5) {
			s.Status, s.Note = StatusNotInVersion, i18n.T("per-subsystem log levels need mr9.5+")
		}
		return
	}
	if !ok {
		s.Status, s.Note = StatusUnknown, i18n.Tf("no such option in any release %s…%s", optionBranches[0], optionBranches[len(optionBranches)-1])
		return
	}
	s.Desc, s.Arg = optionDesc(s.Daemon, s.Key, spec.Desc), spec.Arg
	if spec.Value != "" {
		s.Arg = spec.Value
	}
	if known, note := spec.availableIn(v); !known {
		s.Status, s.Note = StatusNotInVersion, note
	}
}

// availableIn checks the option against the release branches around v. A
// version between two sampled branches passes if either branch has it, so
// that only certain mismatches are reported.
func (o optionSpec) availableIn(v Version) (bool, string) {
	return availableIn(optionBranches, uint64(o.Branches), v)
}

// availableIn checks a branch bitmask (bit i = branches[i] has the item)
// against the host version.
func availableIn(branches []string, mask uint64, v Version) (bool, string) {
	if !v.Known() {
		return true, ""
	}
	lo, hi := -1, -1
	for i, b := range branches {
		bv, err := ParseVersion(b)
		if err != nil { // master
			if hi < 0 {
				hi = i
			}
			continue
		}
		c := v.Compare(bv.Major, bv.Minor)
		if c >= 0 {
			lo = i
		}
		if c <= 0 && hi < 0 {
			hi = i
		}
	}
	has := func(i int) bool { return i >= 0 && mask&(1<<i) != 0 }
	if has(lo) || has(hi) {
		return true, ""
	}
	first, last := -1, -1
	for i := range branches {
		if has(i) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	switch {
	case first == len(branches)-1:
		return false, i18n.Tf("only in unreleased master, host runs %s", v.Short())
	case first > lo:
		return false, i18n.Tf("added in %s, host runs %s", branches[first], v.Short())
	case last >= 0 && last < lo:
		return false, i18n.Tf("dropped after %s, host runs %s", branches[last], v.Short())
	}
	return false, i18n.Tf("not in %s", v.Short())
}

// optionSpec is a generated option table entry (see options_gen.go).
type optionSpec struct {
	Arg, Desc, Value string
	// Branches is a bit set over optionBranches.
	Branches uint32
}

// OptionHelp returns the description of an option, for describe views.
func OptionHelp(d Daemon, name string) (desc, arg string, branches []string) {
	table := daemonOptions
	if d == DaemonRecording {
		table = recordingOptions
	}
	o, ok := table[name]
	if !ok {
		return "", "", nil
	}
	for i, b := range optionBranches {
		if o.Branches&(1<<i) != 0 {
			branches = append(branches, b)
		}
	}
	arg = o.Arg
	if o.Value != "" {
		arg = o.Value
	}
	return optionDesc(d, name, o.Desc), arg, branches
}

// ReleaseRanges sums up the branches of an option (OptionHelp) as runs of
// consecutive releases: "since mr6.5", "mr6.5 – mr9.5, since mr12.5".
func ReleaseRanges(branches []string) string {
	have := map[string]bool{}
	for _, b := range branches {
		have[b] = true
	}
	var runs []string
	for i := 0; i < len(optionBranches); i++ {
		if !have[optionBranches[i]] {
			continue
		}
		j := i
		for j+1 < len(optionBranches) && have[optionBranches[j+1]] {
			j++
		}
		switch {
		case j == len(optionBranches)-1:
			runs = append(runs, i18n.Tf("since %s", optionBranches[i]))
		case i == j:
			runs = append(runs, optionBranches[i])
		default:
			runs = append(runs, optionBranches[i]+" – "+optionBranches[j])
		}
		i = j
	}
	return strings.Join(runs, ", ")
}

// optionTr is a translated option description and the English text it was
// translated from.
type optionTr struct{ En, Text string }

// optionDesc returns the description in the interface language. A
// translation made from another English text than the current one is
// skipped: the English text is right, the old translation may not be.
func optionDesc(d Daemon, name, en string) string {
	tables, ok := optionTranslations[i18n.Lang()]
	if !ok {
		return en
	}
	if tr, ok := tables[d][name]; ok && tr.En == en {
		return tr.Text
	}
	return en
}

// optionTranslations are the option descriptions per language and daemon.
var optionTranslations = map[string]map[Daemon]map[string]optionTr{
	"ru": {DaemonRTPEngine: daemonOptionsRU, DaemonRecording: recordingOptionsRU},
}

func sameValue(conf, runtime string) bool {
	a, b := normBool(conf), normBool(runtime)
	if a == b {
		return true
	}
	fa, ea := strconv.ParseFloat(a, 64)
	fb, eb := strconv.ParseFloat(b, 64)
	return ea == nil && eb == nil && fa == fb
}

func scalar(s string) bool {
	_, err := strconv.ParseFloat(normBool(s), 64)
	return err == nil
}

func normBool(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on":
		return "1"
	case "false", "no", "off":
		return "0"
	}
	return strings.TrimSpace(s)
}

// ListenCLI turns a listen-cli value ("9900", "localhost:2224", "[::1]:9900",
// "a;b") into a dial address.
func ListenCLI(v string) string {
	v = strings.TrimSpace(strings.Split(v, ";")[0])
	if v == "" {
		return ""
	}
	if _, err := strconv.Atoi(v); err == nil {
		return "127.0.0.1:" + v
	}
	return v
}
