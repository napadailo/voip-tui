package homer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/napadailo/voip-tui/i18n"
)

// SearchSpec is a user-facing search description that can be re-resolved
// (relative time ranges move forward on every refresh).
type SearchSpec struct {
	Key     string            // 1_call
	Filters []Filter          // field conditions
	Smart   string            // smartinput expression
	Since   time.Duration     // relative range; used when From is zero
	From    time.Time         // absolute range start
	To      time.Time         // absolute range end (zero = now)
	Limit   int               //
	Nodes   []string          // empty = all nodes
	OrLogic bool              //
	Extra   map[string]string // unknown key=value pairs (reported to the user)
}

// Filter is one field condition as typed by the user.
type Filter struct {
	Field string // as typed: from_user, data_header.from_user, callid
	Value string // homer syntax: "100", "!=100", "10%", "a;b"
}

// Relative reports whether the range follows "now".
func (s SearchSpec) Relative() bool { return s.From.IsZero() }

// Range resolves the time window at the given moment.
func (s SearchSpec) Range(now time.Time) (time.Time, time.Time) {
	if !s.From.IsZero() {
		to := s.To
		if to.IsZero() {
			to = now
		}
		return s.From, to
	}
	since := s.Since
	if since <= 0 {
		since = time.Hour
	}
	return now.Add(-since), now
}

// RangeLabel describes the window for headers/titles.
func (s SearchSpec) RangeLabel(loc *time.Location) string {
	if s.Relative() {
		return i18n.Tf("last %s", FormatDuration(s.Since))
	}
	to := i18n.T("now")
	if !s.To.IsZero() {
		to = s.To.In(loc).Format("2006-01-02 15:04")
	}
	return s.From.In(loc).Format("2006-01-02 15:04") + " → " + to
}

// Window says the time window in the syntax ParseWindow reads: 1h back
// from now, or from..to with to empty for now.
func (s SearchSpec) Window(loc *time.Location) string {
	if s.Relative() {
		return FormatDuration(s.Since)
	}
	w := s.From.In(loc).Format("2006-01-02 15:04:05") + ".."
	if !s.To.IsZero() {
		w += s.To.In(loc).Format("2006-01-02 15:04:05")
	}
	return w
}

// ParseWindow sets the time window of spec: a duration back from now (1h),
// or from..to with each side a time ParseTime reads (15:00..15:30,
// -3h..-1h, 2026-10-21 16:00..); to left out is now, as is a time alone.
func ParseWindow(s string, spec *SearchSpec, loc *time.Location, now time.Time) error {
	s = strings.TrimSpace(s)
	from, to, ranged := strings.Cut(s, "..")
	if !ranged {
		if d, err := ParseDuration(s); err == nil {
			spec.Since, spec.From, spec.To = d, time.Time{}, time.Time{}
			return nil
		}
	}
	f, err := ParseTime(from, loc, now)
	if err != nil {
		return err
	}
	var t time.Time
	if strings.TrimSpace(to) != "" {
		if t, err = ParseTime(to, loc, now); err != nil {
			return err
		}
		if !t.After(f) {
			return errors.New(i18n.T("time range is empty: to must be after from"))
		}
	}
	spec.Since, spec.From, spec.To = 0, f, t
	return nil
}

// String renders the spec back into command syntax.
func (s SearchSpec) String() string {
	var parts []string
	for _, f := range s.Filters {
		v := f.Value
		if strings.HasPrefix(v, "!=") {
			parts = append(parts, f.Field+"!="+quoteIfNeeded(strings.TrimPrefix(v, "!=")))
		} else {
			parts = append(parts, f.Field+"="+quoteIfNeeded(v))
		}
	}
	if s.Relative() {
		parts = append(parts, "since="+FormatDuration(s.Since))
	} else {
		parts = append(parts, "from="+quoteIfNeeded(s.From.Format("2006-01-02 15:04:05")))
		if !s.To.IsZero() {
			parts = append(parts, "to="+quoteIfNeeded(s.To.Format("2006-01-02 15:04:05")))
		}
	}
	if s.Limit > 0 {
		parts = append(parts, "limit="+strconv.Itoa(s.Limit))
	}
	if len(s.Nodes) > 0 {
		parts = append(parts, "node="+strings.Join(s.Nodes, ","))
	}
	if s.OrLogic {
		parts = append(parts, "or")
	}
	if s.Smart != "" {
		parts = append(parts, "where "+s.Smart)
	}
	return strings.Join(parts, " ")
}

func quoteIfNeeded(v string) string {
	if strings.ContainsAny(v, " \t\"") {
		return strconv.Quote(v)
	}
	return v
}

// ParseSearchArgs parses "from_user=100 method=INVITE since=2h limit=50 where <smart>".
// Reserved keys: since/last, from, to, limit, node(s), or. Everything after the
// word "where" is passed verbatim as a smartinput expression.
func ParseSearchArgs(args string, base SearchSpec, loc *time.Location, now time.Time) (SearchSpec, error) {
	spec := base
	spec.Filters = append([]Filter(nil), base.Filters...)
	spec.Extra = map[string]string{}

	if idx := whereIndex(args); idx >= 0 {
		spec.Smart = strings.TrimSpace(args[idx+len("where"):])
		args = args[:idx]
	}
	tokens, err := splitArgs(args)
	if err != nil {
		return spec, err
	}
	for _, tok := range tokens {
		if strings.EqualFold(tok, "or") {
			spec.OrLogic = true
			continue
		}
		if strings.EqualFold(tok, "and") {
			spec.OrLogic = false
			continue
		}
		key, val, neg, ok := splitKV(tok)
		if !ok {
			// bare duration like "15m" is a shortcut for since=15m
			if d, err := ParseDuration(tok); err == nil {
				spec.Since, spec.From, spec.To = d, time.Time{}, time.Time{}
				continue
			}
			return spec, fmt.Errorf(i18n.T("cannot parse %q: expected field=value"), tok)
		}
		switch strings.ToLower(key) {
		case "since", "last":
			d, err := ParseDuration(val)
			if err != nil {
				return spec, err
			}
			spec.Since, spec.From, spec.To = d, time.Time{}, time.Time{}
		case "from":
			t, err := ParseTime(val, loc, now)
			if err != nil {
				return spec, err
			}
			spec.From = t
		case "to":
			t, err := ParseTime(val, loc, now)
			if err != nil {
				return spec, err
			}
			spec.To = t
		case "limit":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return spec, fmt.Errorf(i18n.T("bad limit %q"), val)
			}
			spec.Limit = n
		case "node", "nodes":
			spec.Nodes = splitList(val)
		default:
			if neg {
				val = "!=" + val
			}
			spec.Filters = upsertFilter(spec.Filters, Filter{Field: key, Value: val})
		}
	}
	if !spec.From.IsZero() && !spec.To.IsZero() && !spec.To.After(spec.From) {
		return spec, errors.New(i18n.T("time range is empty: to must be after from"))
	}
	return spec, nil
}

func upsertFilter(fs []Filter, f Filter) []Filter {
	for i := range fs {
		if fs[i].Field == f.Field {
			if f.Value == "" {
				return append(fs[:i], fs[i+1:]...)
			}
			fs[i] = f
			return fs
		}
	}
	if f.Value == "" {
		return fs
	}
	return append(fs, f)
}

func whereIndex(s string) int {
	inQuote := false
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			inQuote = !inQuote
		}
		if inQuote {
			continue
		}
		if (i == 0 || s[i-1] == ' ') && len(s) >= i+5 && strings.EqualFold(s[i:i+5], "where") &&
			(len(s) == i+5 || s[i+5] == ' ') {
			return i
		}
	}
	return -1
}

func splitKV(tok string) (key, val string, neg, ok bool) {
	if i := strings.Index(tok, "!="); i > 0 {
		return tok[:i], tok[i+2:], true, true
	}
	if i := strings.Index(tok, "="); i > 0 {
		return tok[:i], tok[i+1:], false, true
	}
	return "", "", false, false
}

func splitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote, has := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			has = true
		case unicode.IsSpace(r) && !inQuote:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if inQuote {
		return nil, errors.New(i18n.T("unterminated quote"))
	}
	if has {
		out = append(out, cur.String())
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseDuration accepts Go durations plus d (days) and w (weeks): 15m, 2h, 1d, 1w, 1h30m.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "-"))
	if s == "" {
		return 0, errors.New(i18n.T("empty duration"))
	}
	var total time.Duration
	rest := s
	for rest != "" {
		i := 0
		for i < len(rest) && (rest[i] >= '0' && rest[i] <= '9' || rest[i] == '.') {
			i++
		}
		if i == 0 {
			return 0, fmt.Errorf(i18n.T("bad duration %q"), s)
		}
		num, err := strconv.ParseFloat(rest[:i], 64)
		if err != nil {
			return 0, fmt.Errorf(i18n.T("bad duration %q"), s)
		}
		j := i
		for j < len(rest) && (rest[j] < '0' || rest[j] > '9') && rest[j] != '.' {
			j++
		}
		unit := rest[i:j]
		var mult time.Duration
		switch unit {
		case "w":
			mult = 7 * 24 * time.Hour
		case "d":
			mult = 24 * time.Hour
		case "h":
			mult = time.Hour
		case "m":
			mult = time.Minute
		case "s":
			mult = time.Second
		case "ms":
			mult = time.Millisecond
		default:
			return 0, fmt.Errorf(i18n.T("bad duration unit %q in %q (use s, m, h, d, w)"), unit, s)
		}
		total += time.Duration(num * float64(mult))
		rest = rest[j:]
	}
	if total <= 0 {
		return 0, fmt.Errorf(i18n.T("duration must be positive: %q"), s)
	}
	return total, nil
}

// FormatDuration renders a duration compactly: 90m -> 1h30m, 48h -> 2d.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	var b strings.Builder
	if days := d / (24 * time.Hour); days > 0 {
		fmt.Fprintf(&b, "%dd", days)
		d -= days * 24 * time.Hour
	}
	if h := d / time.Hour; h > 0 {
		fmt.Fprintf(&b, "%dh", h)
		d -= h * time.Hour
	}
	if m := d / time.Minute; m > 0 {
		fmt.Fprintf(&b, "%dm", m)
		d -= m * time.Minute
	}
	if s := d / time.Second; s > 0 {
		fmt.Fprintf(&b, "%ds", s)
	}
	return b.String()
}

// ParseTime accepts RFC3339, "2006-01-02 15:04[:05]", "15:04[:05]" (today),
// unix seconds/milliseconds and relative "-2h".
func ParseTime(s string, loc *time.Location, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "now" {
		return now, nil
	}
	if strings.HasPrefix(s, "-") {
		d, err := ParseDuration(s)
		if err != nil {
			return time.Time{}, err
		}
		return now.Add(-d), nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1e12 {
			return time.UnixMilli(n), nil
		}
		return time.Unix(n, 0), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			n := now.In(loc)
			return time.Date(n.Year(), n.Month(), n.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc), nil
		}
	}
	return time.Time{}, fmt.Errorf(i18n.T("cannot parse time %q (use 2006-01-02 15:04, 15:04, RFC3339, unix or -2h)"), s)
}

// ResolveField maps a user-typed field to a mapping field id.
// It accepts the full id, the part after "data_header."/"protocol_header." or the field title.
func ResolveField(name string, m *Mapping) (Field, bool) {
	builtin := map[string]Field{
		"sid":         {ID: "sid", Type: "string", Name: "Session ID"},
		"raw":         {ID: "raw", Type: "string", Name: "Raw"},
		"id":          {ID: "id", Type: "integer", Name: "ID"},
		"create_date": {ID: "create_date", Type: "string", Name: "Date"},
	}
	if m != nil {
		lname := strings.ToLower(name)
		for _, f := range m.Fields {
			if strings.EqualFold(f.ID, name) {
				return f, true
			}
		}
		for _, f := range m.Fields {
			if i := strings.LastIndex(f.ID, "."); i >= 0 && strings.EqualFold(f.ID[i+1:], name) {
				return f, true
			}
		}
		for _, f := range m.Fields {
			if strings.ToLower(f.Name) == lname {
				return f, true
			}
		}
	}
	if f, ok := builtin[strings.ToLower(name)]; ok {
		return f, true
	}
	return Field{}, false
}

// BuildQuery turns a spec into a concrete API query at time now.
// Unknown fields are returned so the caller can warn: homer-app silently drops them.
func BuildQuery(spec SearchSpec, m *Mapping, allNodes []string, now time.Time) (Query, []string) {
	q := Query{Key: spec.Key, Smart: spec.Smart, OrLogic: spec.OrLogic, Limit: spec.Limit}
	if m != nil {
		q.HepID = m.HepID
	} else if i := strings.Index(spec.Key, "_"); i > 0 {
		q.HepID, _ = strconv.Atoi(spec.Key[:i])
	}
	q.From, q.To = spec.Range(now)
	q.Nodes = spec.Nodes
	if len(q.Nodes) == 0 {
		q.Nodes = allNodes
	}
	var unknown []string
	for _, f := range spec.Filters {
		field, ok := ResolveField(f.Field, m)
		if !ok {
			unknown = append(unknown, f.Field)
			continue
		}
		typ := field.Type
		if typ == "" {
			typ = "string"
		}
		q.Conditions = append(q.Conditions, Condition{Name: field.ID, Value: f.Value, Type: typ, HepID: q.HepID})
	}
	return q, unknown
}
