package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/napadailo/voip-tui/tui/theme"
)

// qosPoint is one RTCP report block about a stream: jitter and loss as
// the receiver saw them, the RTT when the block has LSR and DLSR.
type qosPoint struct {
	t                 time.Time
	jitter, loss, rtt float64 // ms, %, ms
	hasRTT            bool
	lost              int64 // cumulative
}

// qosRate is the send rate of a stream between two sender reports.
type qosRate struct {
	t    time.Time
	kbps float64
}

// qosStream is the media of one SSRC as one receiver reported it.
type qosStream struct {
	ssrc     uint32
	from, to string // the way the media goes
	points   []qosPoint
	rates    []qosRate
}

// rtpClock turns ia_jitter, counted in RTP clock ticks, into ms: 8 kHz,
// the clock of G.711 and G.729.
const rtpClock = 8.0

// ntpShort is the middle 32 bits of the NTP time of t, the unit of LSR.
func ntpShort(t time.Time) uint32 {
	sec := uint64(t.Unix() + 2208988800)
	frac := uint64(t.Nanosecond()) << 32 / 1e9
	return uint32(sec<<16 | frac>>16)
}

// qosStreams reads the RTCP reports of a call into streams. A report block
// is about media from source_ssrc to the reporter, so the media goes the
// other way than the RTCP; a sender report gives the rate of its own
// SSRC, the way the RTCP goes.
func qosStreams(data []map[string]any, name func(ip string, port any) string) []*qosStream {
	type report struct {
		SSRC   uint32 `json:"ssrc"`
		Type   int    `json:"type"`
		Sender *struct {
			Packets float64 `json:"packets"`
			Octets  float64 `json:"octets"`
		} `json:"sender_information"`
		Blocks []struct {
			Source   uint32  `json:"source_ssrc"`
			Fraction float64 `json:"fraction_lost"`
			Lost     int64   `json:"packets_lost"`
			Jitter   float64 `json:"ia_jitter"`
			LSR      uint32  `json:"lsr"`
			DLSR     uint32  `json:"dlsr"`
		} `json:"report_blocks"`
	}
	sort.SliceStable(data, func(i, j int) bool { return itemTime(data[i]).Before(itemTime(data[j])) })
	byKey := map[string]*qosStream{}
	var order []*qosStream
	stream := func(key string, ssrc uint32, from, to string) *qosStream {
		s := byKey[key]
		if s == nil {
			s = &qosStream{ssrc: ssrc, from: from, to: to}
			byKey[key] = s
			order = append(order, s)
		}
		return s
	}
	type sent struct {
		t      time.Time
		octets float64
	}
	last := map[uint32]sent{}
	rates := map[uint32][]qosRate{}
	senders := map[uint32][2]string{}
	for _, m := range data {
		var r report
		if json.Unmarshal([]byte(str(m, "raw")), &r) != nil {
			continue
		}
		t := itemTime(m)
		src, dst := name(str(m, "srcIp"), m["srcPort"]), name(str(m, "dstIp"), m["dstPort"])
		if r.Sender != nil && r.SSRC != 0 {
			senders[r.SSRC] = [2]string{src, dst}
			if p, ok := last[r.SSRC]; ok && t.After(p.t) && r.Sender.Octets >= p.octets {
				rates[r.SSRC] = append(rates[r.SSRC], qosRate{t, (r.Sender.Octets - p.octets) * 8 / 1000 / t.Sub(p.t).Seconds()})
			}
			last[r.SSRC] = sent{t, r.Sender.Octets}
		}
		for _, b := range r.Blocks {
			p := qosPoint{t: t, jitter: b.Jitter / rtpClock, loss: b.Fraction * 100 / 256, lost: b.Lost}
			if b.LSR != 0 {
				if d := int32(ntpShort(t) - b.LSR - b.DLSR); d >= 0 && d < 10*65536 {
					p.rtt, p.hasRTT = float64(d)*1000/65536, true
				}
			}
			s := stream(fmt.Sprintf("%d>%d", b.Source, r.SSRC), b.Source, dst, src)
			s.points = append(s.points, p)
		}
	}
	// a sender nobody reported on is a stream of its own
	for ssrc, ends := range senders {
		found := false
		for _, s := range order {
			found = found || s.ssrc == ssrc
		}
		if !found {
			stream(fmt.Sprintf("%d>", ssrc), ssrc, ends[0], ends[1])
		}
	}
	for _, s := range order {
		s.rates = rates[s.ssrc]
	}
	return order
}

// mosOf estimates MOS from the simplified E-model: the delay is half the
// RTT, jitter counts twice and 10 ms of codec delay, each % of loss costs
// 2.5 of R.
func mosOf(p qosPoint) float64 {
	delay := p.rtt/2 + 2*p.jitter + 10
	r := 93.2 - 2.5*p.loss
	if delay < 160 {
		r -= delay / 40
	} else {
		r -= (delay - 120) / 10
	}
	r = math.Max(0, math.Min(100, r))
	return 1 + 0.035*r + 7e-6*r*(r-60)*(100-r)
}

// metric is one line of the chart: the values over time, how to pick the
// worse of two and how to colour one.
type metric struct {
	worst  func(a, b float64) float64 // the worse of two, for a column
	warn   func(v float64) tcell.Color
	values []struct {
		t time.Time
		v float64
	}
}

func (m *metric) add(t time.Time, v float64) {
	m.values = append(m.values, struct {
		t time.Time
		v float64
	}{t, v})
}

// line draws the metric as a thin line on width columns from t0 to t1,
// each column coloured by the worst value in it. A column no report fell
// in takes the nearest report when it is less than gap away, else it is a
// ·: reports missing.
func (m *metric) line(t0, t1 time.Time, width int, gap time.Duration) string {
	cols := make([]float64, width)
	has := make([]bool, width)
	span := t1.Sub(t0)
	at := func(c int) time.Time {
		if width < 2 {
			return t0
		}
		return t0.Add(time.Duration(float64(span) * float64(c) / float64(width-1)))
	}
	for _, x := range m.values {
		c := 0
		if span > 0 {
			c = int(math.Round(float64(x.t.Sub(t0)) / float64(span) * float64(width-1)))
		}
		c = max(0, min(width-1, c))
		if has[c] {
			cols[c] = m.worst(cols[c], x.v)
		} else {
			cols[c], has[c] = x.v, true
		}
	}
	filled := append([]bool(nil), has...)
	for c := range cols {
		if has[c] {
			continue
		}
		best := time.Duration(math.MaxInt64)
		for _, x := range m.values {
			if d := (x.t.Sub(at(c))).Abs(); d < best {
				best, cols[c] = d, x.v
			}
		}
		filled[c] = best <= gap
	}
	var b strings.Builder
	for c := range cols {
		if !filled[c] {
			b.WriteString(theme.Tag(theme.Dim) + "·")
			continue
		}
		b.WriteString(theme.Tag(m.warn(cols[c])) + "─")
	}
	return b.String() + "[-]"
}

func higher(a, b float64) float64 { return math.Max(a, b) }
func lower(a, b float64) float64  { return math.Min(a, b) }

func above(warn, bad float64) func(float64) tcell.Color {
	return func(v float64) tcell.Color {
		switch {
		case v >= bad:
			return theme.Err
		case v >= warn:
			return theme.Warn
		}
		return theme.OK
	}
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
