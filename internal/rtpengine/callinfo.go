package rtpengine

import (
	"net"
	"regexp"
	"strconv"
	"strings"
)

// CallInfo is `call <id> info` (mr10.5+) or `list sessions <id>` parsed.
// Layouts follow daemon/cli.c: a one-line "callid: … | …" header up to
// mr9.4, key: value lines after; tags with label/branch since mr9.5,
// subscriptions since mr10.5, uts/kts last-packet times since mr12.5.
type CallInfo struct {
	CallID      string
	Deleted     bool
	Created     int64 // unix seconds
	Proxy       string
	TOS         string
	LastSignal  int64 // unix seconds
	Keyspace    string
	RedisUpdate int64 // unix microseconds, 0 = never (mr12.5+)
	Foreign     bool
	Recording   string // "" when not printed (before mr12.5)
	Legs        []CallLeg
	Extra       []KV // lines that did not parse, kept for display
}

// CallLeg is one monologue (a "--- Tag" block).
type CallLeg struct {
	Tag          string
	Type         string // FROM_TAG, TO_TAG, UNKNOWN…
	Label        string // caller/callee when the proxy sets labels
	Branch       string
	Duration     float64  // seconds since the monologue was created
	Dialogue     string   // "in dialogue with" (before mr10.5)
	SubscribedTo []string // tags this leg receives media from
	Subscribers  []string // tags receiving media from this leg
	Aliases      []string
	Media        []CallMedia
}

// CallMedia is one m= line of a leg.
type CallMedia struct {
	Index    int
	Type     string // audio, video…
	Protocol string // RTP/AVP, RTP/SAVPF…
	Codec    string // PCMA/8000, "" when unknown
	Streams  []CallStream
}

// CallStream is one "Port" line: what rtpengine received from the peer on
// one of its local ports.
type CallStream struct {
	Local, Peer string // address:port
	RTCP        bool
	SSRC        string
	Packets     uint64
	Bytes       uint64
	Errors      uint64
	// LastPacket is the last packet handled in userspace (ts/uts),
	// LastKernel the last packet counted at all, kernel module included
	// (kts, mr12.5+). kts ahead of uts means the kernel forwards the stream.
	LastPacket int64
	LastKernel int64
	HasKernel  bool
	Delay      string // "min/avg/max" when built with delay measurement
}

// Last returns the time of the most recent packet on the stream.
func (s CallStream) Last() int64 { return max(s.LastPacket, s.LastKernel) }

// Kernel reports whether the kernel module forwarded the latest packets.
func (s CallStream) Kernel() bool {
	return s.HasKernel && s.LastKernel > s.LastPacket
}

var (
	callTagLineRe = regexp.MustCompile(`^--- Tag '([^']*)',? type: ([^,]*), (?:label '([^']*)', branch '([^']*)', )?callduration ([0-9.]+)\s*(?:, in dialogue with '([^']*)')?`)
	callSubRe     = regexp.MustCompile(`^---\s+(subscribed to|subscription of)(?: media with monologue tag)? '([^']*)'`)
	callAliasRe   = regexp.MustCompile(`'([^']*)'`)
	callMediaHdRe = regexp.MustCompile(`^------ Media #(\d+) \((\S+) over ([^)]*)\) using (.*)$`)
	// "-------- Port" since mr7.5; "------ Media #1," with the media index
	// inline before that
	callStreamRe = regexp.MustCompile(`^-+ (?:Port|Media #(\d+),)\s+(\S+):(\d+)\s*<>\s*(\S+):(\d+)\s*(\(RTCP\))?, (?:SSRC ([0-9a-fA-F]+), )?(?:in )?(\d+) p, (\d+) b, (\d+) e, (\d+) (?:ts|last_packet|uts)(?: (\d+) kts)?(?:, ([0-9.]+) delay_min, ([0-9.]+) delay_avg, ([0-9.]+) delay_max)?`)
	callKVLineRe = regexp.MustCompile(`^([a-z_ ]+):\s*(.*)$`)
)

// ParseCallInfo parses the reply to `call <id> info` / `list sessions <id>`.
// It returns nil when the reply has no callid (e.g. "Call ID not found").
func ParseCallInfo(out string) *CallInfo {
	ci := &CallInfo{}
	var leg *CallLeg
	var media *CallMedia
	for _, raw := range strings.Split(out, "\n") {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		switch {
		case strings.HasPrefix(t, "callid:") && strings.Contains(t, " | "):
			for _, part := range strings.Split(t, "|") {
				if k, v, ok := strings.Cut(part, ":"); ok {
					ci.set(strings.TrimSpace(k), strings.TrimSpace(v))
				}
			}
		case callTagLineRe.MatchString(t):
			m := callTagLineRe.FindStringSubmatch(t)
			d, _ := strconv.ParseFloat(m[5], 64)
			ci.Legs = append(ci.Legs, CallLeg{Tag: m[1], Type: strings.TrimSpace(m[2]), Label: m[3], Branch: m[4], Duration: d, Dialogue: m[6]})
			leg, media = &ci.Legs[len(ci.Legs)-1], nil
		case leg != nil && callSubRe.MatchString(t):
			m := callSubRe.FindStringSubmatch(t)
			if m[1] == "subscribed to" {
				leg.SubscribedTo = appendNew(leg.SubscribedTo, m[2])
			} else {
				leg.Subscribers = appendNew(leg.Subscribers, m[2])
			}
		case leg != nil && strings.HasPrefix(t, "---") && strings.Contains(t, "Aliases:"):
			for _, m := range callAliasRe.FindAllStringSubmatch(t, -1) {
				leg.Aliases = append(leg.Aliases, m[1])
			}
		case leg != nil && callMediaHdRe.MatchString(t):
			m := callMediaHdRe.FindStringSubmatch(t)
			idx, _ := strconv.Atoi(m[1])
			codec := strings.TrimSpace(m[4])
			if codec == "unknown codec" {
				codec = ""
			}
			leg.Media = append(leg.Media, CallMedia{Index: idx, Type: m[2], Protocol: m[3], Codec: codec})
			media = &leg.Media[len(leg.Media)-1]
		case leg != nil && callStreamRe.MatchString(t):
			m := callStreamRe.FindStringSubmatch(t)
			s := CallStream{
				Local: m[2] + ":" + m[3], Peer: m[4] + ":" + m[5], RTCP: m[6] != "", SSRC: m[7],
				Packets: atou(m[8]), Bytes: atou(m[9]), Errors: atou(m[10]),
				LastPacket: int64(atou(m[11])),
			}
			if m[12] != "" {
				s.LastKernel, s.HasKernel = int64(atou(m[12])), true
			}
			if m[13] != "" {
				s.Delay = m[13] + "/" + m[14] + "/" + m[15]
			}
			if m[1] != "" { // old layout: the media index is on every stream line
				idx, _ := strconv.Atoi(m[1])
				if media == nil || media.Index != idx {
					leg.Media = append(leg.Media, CallMedia{Index: idx})
					media = &leg.Media[len(leg.Media)-1]
				}
			}
			if media == nil {
				leg.Media = append(leg.Media, CallMedia{Index: 1})
				media = &leg.Media[len(leg.Media)-1]
			}
			media.Streams = append(media.Streams, s)
		case leg == nil && callKVLineRe.MatchString(t):
			m := callKVLineRe.FindStringSubmatch(t)
			if !ci.set(m[1], strings.TrimSpace(m[2])) {
				ci.Extra = append(ci.Extra, KV{Key: m[1], Value: m[2]})
			}
		default:
			if k, v, ok := strings.Cut(t, ":"); ok && !strings.HasPrefix(t, "-") {
				ci.Extra = append(ci.Extra, KV{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
			} else {
				ci.Extra = append(ci.Extra, KV{Value: t})
			}
		}
	}
	if ci.CallID == "" {
		return nil
	}
	return ci
}

// set stores a header field, reporting whether the key is known.
func (ci *CallInfo) set(k, v string) bool {
	switch k {
	case "callid":
		ci.CallID = v
	case "deletionmark":
		ci.Deleted = v == "yes"
	case "created":
		ci.Created = int64(atou(v))
	case "proxy":
		ci.Proxy = v
	case "tos":
		ci.TOS = v
	case "last_signal":
		ci.LastSignal = int64(atou(v))
	case "redis_keyspace":
		ci.Keyspace = v
	case "last redis update":
		ci.RedisUpdate = int64(atou(v))
	case "foreign":
		ci.Foreign = v == "yes"
	case "recording":
		ci.Recording = v
	default:
		return false
	}
	return true
}

// Now estimates the daemon's clock when it printed the reply: a monologue's
// callduration runs from call creation, packet times are taken from the same
// clock. Using it instead of the local clock keeps ages right when the
// clocks of this machine and the node differ.
func (ci *CallInfo) Now() int64 {
	now := ci.LastSignal
	for _, l := range ci.Legs {
		now = max(now, ci.Created+int64(l.Duration))
		for _, m := range l.Media {
			for _, s := range m.Streams {
				now = max(now, s.Last())
			}
		}
	}
	return now
}

// LegName is the label of the leg with tag, else a shortened tag.
func (ci *CallInfo) LegName(tag string) string {
	for _, l := range ci.Legs {
		if l.Tag == tag {
			return l.Name()
		}
	}
	return shortTag(tag)
}

// Name is the label of the leg, else its tag type, else a shortened tag.
func (l CallLeg) Name() string {
	switch {
	case l.Label != "":
		return l.Label
	case l.Type == "FROM_TAG":
		return "from-tag"
	case l.Type == "TO_TAG":
		return "to-tag"
	}
	return shortTag(l.Tag)
}

func shortTag(t string) string {
	if len(t) > 12 {
		return t[:8] + "…"
	}
	return t
}

func appendNew(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func atou(s string) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return n
}

// InterfaceFor names the rtpengine interface (from `list interfaces`) that
// owns a local address:port of a stream. Several interfaces may share an
// address with different port ranges, so the port decides when it can.
func InterfaceFor(ifaces []Interface, endpoint string) string {
	i := strings.LastIndexByte(endpoint, ':')
	if i < 0 {
		return ""
	}
	ip := net.ParseIP(strings.Trim(endpoint[:i], "[]"))
	port, _ := strconv.Atoi(endpoint[i+1:])
	if ip == nil {
		return ""
	}
	var byAddr []string
	for _, f := range ifaces {
		if a := net.ParseIP(f.Address); a == nil || !a.Equal(ip) {
			continue
		}
		if f.PortMax > 0 && port >= f.PortMin && port <= f.PortMax {
			return f.Name
		}
		byAddr = appendNew(byAddr, f.Name)
	}
	if len(byAddr) == 1 {
		return byAddr[0]
	}
	return ""
}
