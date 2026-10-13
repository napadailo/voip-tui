package rtpengine

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Session is one line of `list sessions`.
type Session struct {
	CallID   string
	Created  time.Time
	Proxy    string // created_from, the signalling peer
	Deleted  bool   // deletionmark: the call is in delete-delay
	Foreign  bool   // owned by another rtpengine (redis keyspace)
	Keyspace string
	Rec      string // recording, mr12.5+
	Extra    map[string]string
}

// sessionKeys maps both layouts to one set of names:
//
//	≤mr11.5  callid: X | deletionmark: no | created: 1700000000 | proxy:IP:port | redis_keyspace:0 | foreign:no
//	≥mr12.5  ID: X | del:no | creat: 1700000000 | prx:IP:port | redis: 0 | frgn:no | rec:no
var sessionKeys = map[string]string{
	"callid": "id", "id": "id",
	"deletionmark": "del", "del": "del",
	"created": "created", "creat": "created",
	"proxy": "proxy", "prx": "proxy",
	"redis_keyspace": "redis", "redis": "redis",
	"foreign": "foreign", "frgn": "foreign",
	"rec": "rec",
}

// ParseSessions parses `list sessions all|own|foreign`.
func ParseSessions(out string) []Session {
	var res []Session
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		low := strings.ToLower(line)
		if !strings.HasPrefix(low, "callid:") && !strings.HasPrefix(low, "id:") {
			continue
		}
		s := Session{Extra: map[string]string{}}
		for _, part := range strings.Split(line, " | ") {
			k, v, ok := strings.Cut(part, ":")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			switch sessionKeys[strings.ToLower(k)] {
			case "id":
				s.CallID = v
			case "del":
				s.Deleted = v == "yes"
			case "created":
				if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
					s.Created = time.Unix(n, 0)
				}
			case "proxy":
				s.Proxy = v
			case "redis":
				s.Keyspace = v
			case "foreign":
				s.Foreign = v == "yes"
			case "rec":
				s.Rec = v
			default:
				s.Extra[k] = v
			}
		}
		if s.CallID != "" {
			res = append(res, s)
		}
	}
	return res
}

// NumSessions is `list numsessions`.
type NumSessions struct {
	Own, Foreign, Total int
	// Extra holds newer counters (transcoded media, ipv4/ipv6/mixed media).
	Extra []KV
}

var numRe2 = regexp.MustCompile(`(?i)^current\s+(.+?)\s*:\s*(\d+)`)

// ParseNumSessions parses `list numsessions`.
func ParseNumSessions(out string) NumSessions {
	var n NumSessions
	for _, l := range strings.Split(out, "\n") {
		m := numRe2.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		v, _ := strconv.Atoi(m[2])
		switch strings.ToLower(m[1]) {
		case "sessions own":
			n.Own = v
		case "sessions foreign":
			n.Foreign = v
		case "sessions total":
			n.Total = v
		default:
			n.Extra = append(n.Extra, KV{Key: m[1], Value: m[2]})
		}
	}
	return n
}

// KV is a named value with an optional section, e.g. from `list totals`.
type KV struct {
	Section string
	Key     string
	Value   string
}

// ParseStats parses `list totals` and `list counters`: section headers
// ("Statistics over lifetime:") followed by " Name    :value" lines.
func ParseStats(out string) []KV {
	var res []KV
	section := ""
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, " \r")
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		k, v, ok := strings.Cut(t, ":")
		switch {
		case ok && strings.HasPrefix(line, " ") && strings.TrimSpace(v) != "":
			res = append(res, KV{Section: section, Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
		case strings.HasSuffix(t, ":"):
			section = strings.TrimSuffix(t, ":")
		case ok:
			res = append(res, KV{Section: section, Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
		default:
			section = t
		}
	}
	return res
}

// ParseParams parses `params start|current`. Up to mr8.5 lines use option
// names ("max-sessions = -1"), later releases print struct members
// ("max_sessions = -1", "timeout_us = 60000000", "[max-cpu = 0.0]").
func ParseParams(out string) []KV {
	var res []KV
	for _, raw := range strings.Split(out, "\n") {
		t := strings.TrimSpace(raw)
		t = strings.TrimSuffix(strings.TrimPrefix(t, "["), "]")
		k, v, ok := strings.Cut(t, " = ")
		if !ok {
			continue
		}
		res = append(res, KV{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
	}
	return res
}

// paramAliases maps runtime member names to config option names where the
// mechanical "_" → "-" rule does not work.
var paramAliases = map[string]string{
	"kernel_table":       "table",
	"default_tos":        "tos",
	"redis_expires_secs": "redis-expires",
	"bw_limit":           "max-bandwidth",
	"rec_method":         "recording-method",
	"rec_format":         "recording-format",
	"spooldir":           "recording-dir",
	"default_log_level":  "log-level",
	"homer_protocol":     "homer-protocol",
	"homer_id":           "homer-id",
}

// RuntimeOptions maps `params current` output onto config option names,
// converting *_us microseconds to the seconds used in rtpengine.conf.
func RuntimeOptions(params []KV) map[string]string {
	m := map[string]string{}
	for _, kv := range params {
		k, v := kv.Key, kv.Value
		if strings.HasSuffix(k, "_us") {
			k = strings.TrimSuffix(k, "_us")
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				v = strconv.FormatInt(n/1_000_000, 10)
			}
		}
		if a, ok := paramAliases[k]; ok {
			k = a
		}
		m[strings.ReplaceAll(k, "_", "-")] = v
	}
	return m
}

// ParseValue extracts the value of single-value `list` replies:
// "Maximum sessions configured on rtpengine: -1", "TIMEOUT=60", "184",
// "Maximum open-files configured on rtpengine: 65536; cat /proc/1/limits".
func ParseValue(out string) string {
	t := strings.TrimSpace(out)
	if i := strings.LastIndexByte(t, '\n'); i >= 0 {
		t = strings.TrimSpace(t[i+1:])
	}
	if i := strings.Index(t, ": "); i >= 0 {
		t = t[i+2:]
	} else if i := strings.IndexByte(t, '='); i >= 0 {
		t = t[i+1:]
	}
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

// Interface is one entry of `list interfaces` (mr7.5+). The layout grew
// over time:
//
//	mr7.5-mr10.5  Port range, Ports used, Last port used
//	mr11.5        Port range, Ports used
//	mr12.5+       + Packets/bytes/errors (Ingress, Egress), Packets
//	              lost/duplicates, MOS, Jitter, RTT
//
// Averages print as "nan" until there are samples; they are kept empty here.
type Interface struct {
	Name, Address, Family string
	PortMin, PortMax      int
	Used, Total           int
	LastPort              string

	// HasTraffic is set for mr12.5+ output with per-interface counters.
	HasTraffic          bool
	In, Out             Traffic
	Lost, Duplicates    string
	MOS, PacketLoss     string // average MOS, average packet loss in %
	Jitter, JitterMeas  string
	RTTE2E, RTTDiscrete string

	// Extra keeps lines this parser does not know (future releases).
	Extra []KV
}

// Traffic is one direction of interface counters.
type Traffic struct{ Packets, Bytes, Errors string }

var (
	ifRe      = regexp.MustCompile(`^Interface '([^']*)' address '([^']*)' \(([^)]*)\)`)
	rangeRe   = regexp.MustCompile(`^Port range:\s*(\d+)\s*-\s*(\d+)`)
	usedRe    = regexp.MustCompile(`^Ports used:\s*(\d+)\s*/\s*(\d+)`)
	lastRe    = regexp.MustCompile(`^Last port used:\s*(\d+)`)
	trafficRe = regexp.MustCompile(`^(Ingress|Egress):\s*(\S+)\s*/\s*(\S+)\s*/\s*(\S+)`)
	lostRe    = regexp.MustCompile(`^Packets lost/duplicates:\s*(\S+)\s*/\s*(\S+)`)
	mosRe     = regexp.MustCompile(`^MOS:\s*avg\s*(\S+?),\s*packet loss avg\s*(\S+?)%?$`)
	jitterRe  = regexp.MustCompile(`^Jitter:\s*avg\s*(\S+)\s*\(measured\s*(\S+?)\)`)
	rttRe     = regexp.MustCompile(`^RTT:\s*e2e\s*(\S+?),\s*dsct\s*(\S+)`)
)

// ParseInterfaces parses `list interfaces`.
func ParseInterfaces(out string) []Interface {
	var res []Interface
	for _, raw := range strings.Split(out, "\n") {
		t := strings.TrimSpace(raw)
		if m := ifRe.FindStringSubmatch(t); m != nil {
			res = append(res, Interface{Name: m[1], Address: m[2], Family: m[3]})
			continue
		}
		if len(res) == 0 || t == "" || t == "Packets/bytes/errors:" {
			continue
		}
		cur := &res[len(res)-1]
		if m := rangeRe.FindStringSubmatch(t); m != nil {
			cur.PortMin, _ = strconv.Atoi(m[1])
			cur.PortMax, _ = strconv.Atoi(m[2])
		} else if m := usedRe.FindStringSubmatch(t); m != nil {
			cur.Used, _ = strconv.Atoi(m[1])
			cur.Total, _ = strconv.Atoi(m[2])
		} else if m := lastRe.FindStringSubmatch(t); m != nil {
			cur.LastPort = m[1]
		} else if m := trafficRe.FindStringSubmatch(t); m != nil {
			tr := Traffic{Packets: m[2], Bytes: m[3], Errors: m[4]}
			if m[1] == "Ingress" {
				cur.In = tr
			} else {
				cur.Out = tr
			}
			cur.HasTraffic = true
		} else if m := lostRe.FindStringSubmatch(t); m != nil {
			cur.Lost, cur.Duplicates = m[1], m[2]
		} else if m := mosRe.FindStringSubmatch(t); m != nil {
			cur.MOS, cur.PacketLoss = num(m[1]), num(m[2])
		} else if m := jitterRe.FindStringSubmatch(t); m != nil {
			cur.Jitter, cur.JitterMeas = num(m[1]), num(m[2])
		} else if m := rttRe.FindStringSubmatch(t); m != nil {
			cur.RTTE2E, cur.RTTDiscrete = num(m[1]), num(m[2])
		} else if k, v, ok := strings.Cut(t, ":"); ok {
			cur.Extra = append(cur.Extra, KV{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
		} else {
			cur.Extra = append(cur.Extra, KV{Value: t})
		}
	}
	return res
}

// num drops "nan"/"-nan"/"inf" printed for averages without samples.
func num(s string) string {
	if _, err := strconv.ParseFloat(s, 64); err != nil || strings.Contains(strings.ToLower(s), "nan") || strings.Contains(strings.ToLower(s), "inf") {
		return ""
	}
	return s
}

// ParseKeyspaces parses `kslist`: "keyspace[0] = 1" lines or bare numbers.
func ParseKeyspaces(out string) []string {
	var res []string
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if _, v, ok := strings.Cut(t, "="); ok {
			res = append(res, strings.TrimSpace(v))
		} else if _, err := strconv.Atoi(t); err == nil {
			res = append(res, t)
		}
	}
	return res
}

// isUnknown recognises "unknown command" replies of all releases.
func isUnknown(out string) bool {
	t := strings.TrimSpace(out)
	return strings.HasPrefix(t, "Unknown or incomplete command") ||
		strings.HasPrefix(t, "Unknown command:") ||
		strings.HasPrefix(t, "Incomplete command:")
}
