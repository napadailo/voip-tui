package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/napadailo/voip-tui/internal/homer"
	"github.com/napadailo/voip-tui/tui/theme"
)

// RTCP into streams: a block is media towards its reporter, the sender
// report gives the rate, LSR and DLSR the RTT; graphs keep a spike from
// flattening the rest and show missing reports.
func TestQoSStreams(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	var data []map[string]any
	add := func(sec int, src, dst string, raw string) {
		ts := t0.Add(time.Duration(sec) * time.Second)
		data = append(data, map[string]any{"timeSeconds": float64(ts.Unix()), "timeUseconds": float64(0),
			"srcIp": src, "srcPort": float64(40001), "dstIp": dst, "dstPort": float64(40001), "raw": raw})
	}
	for i := 0; i <= 24; i++ {
		if i > 10 && i < 16 {
			continue // no reports for a while
		}
		sec := i * 5
		ts := t0.Add(time.Duration(sec) * time.Second)
		jitter := 16 // 2 ms at 8 kHz
		if i == 0 {
			jitter = 80000 // a spike of 10 s
		}
		lsr := ntpShort(ts.Add(-5 * time.Second))
		dlsr := ntpShort(ts) - lsr - 40*65536/1000 // 40 ms
		add(sec, "10.0.0.2", "10.0.0.1", fmt.Sprintf(`{"type":201,"ssrc":2,"report_blocks":[{"source_ssrc":1,"fraction_lost":%d,"packets_lost":%d,"ia_jitter":%d,"lsr":%d,"dlsr":%d}]}`,
			map[bool]int{true: 26, false: 0}[i == 20], i/20*3, jitter, lsr, dlsr))
		add(sec, "10.0.0.1", "10.0.0.2", fmt.Sprintf(`{"type":200,"ssrc":1,"sender_information":{"packets":%d,"octets":%d},"report_blocks":[]}`, sec*50, sec*8000))
	}
	name := func(ip string, port any) string { return fmt.Sprintf("%s:%v", ip, port) }
	streams := qosStreams(data, name)
	if len(streams) != 1 {
		t.Fatalf("%d streams", len(streams))
	}
	s := streams[0]
	if s.ssrc != 1 || s.from != "10.0.0.1:40001" || s.to != "10.0.0.2:40001" || len(s.points) != 20 || len(s.rates) == 0 {
		t.Fatalf("stream %+v", s)
	}
	if p := s.points[1]; p.jitter != 2 || !p.hasRTT || p.rtt < 39 || p.rtt > 41 {
		t.Fatalf("point %+v", p)
	}
	if r := s.rates[0].kbps; r < 63.9 || r > 64.1 {
		t.Fatalf("rate %v", r)
	}
	// a second SSRC the same way, after a re-INVITE, joins the same line
	more := append([]map[string]any(nil), data...)
	ts := t0.Add(130 * time.Second)
	more = append(more, map[string]any{"timeSeconds": float64(ts.Unix()), "timeUseconds": float64(0), "srcIp": "10.0.0.2", "srcPort": float64(40001),
		"dstIp": "10.0.0.1", "dstPort": float64(40001), "raw": `{"type":201,"ssrc":2,"report_blocks":[{"source_ssrc":9,"fraction_lost":0,"ia_jitter":8}]}`})
	owners := mediaOwners{"10.0.0.2:40001": {"phone", 0}, "ip:10.0.0.1": {"PBX", 1}}
	plan := &mediaPlan{owners: owners}
	legs := qosLegs(qosStreams(more, name), plan)
	if len(legs) != 1 || legs[0].a != "10.0.0.2:40001" || legs[0].fwd != nil || legs[0].back == nil || len(legs[0].back.points) != 21 {
		t.Fatalf("legs %+v", legs)
	}
	out := regexp.MustCompile(`\[[^\[\]]*\]`).ReplaceAllString(qosPath(legs, plan, 140, time.UTC), "")
	for _, want := range []string{
		`(?m)^\s+phone\s+PBX\s*$`,
		`(?m)^\s+10\.0\.0\.2\s+10\.0\.0\.1\s*$`,
		`callee hears\s+│─+ no data ─+▶│`,
		`caller hears\s+│◀─+ ● \d\.\d 10\.2% ─+│`,
		`caller hears\s+● poor\s+MOS \d\.\d, loss 10\.2% \(3\), jitter [\d.]+/10000\.0 ms\s+reported by phone`,
		`callee hears\s+no reports on this way`,
		`trouble\s+→ no reports`,
		`← loss up to 10\.2%, jitter up to 10000 ms from 10:00:00, first on hop 1 \(PBX → phone\)`,
		`1 PBX → phone\s+─+·+─+\s+loss 10\.2%\s+jitter [\d.]+/10000\.0\s+RTT 40`,
	} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
}

// rtpengine puts c= after m= and the RTCP port in a=rtcp.
func TestSDPSections(t *testing.T) {
	body := "v=0\r\nc=IN IP4 192.168.20.9\r\nm=audio 15358 RTP/AVP 8 18 101\r\nc=IN IP4 192.168.20.226\r\na=rtcp:15359\r\nm=video 0 RTP/AVP 96\r\n"
	got := sdpSections(body)
	if len(got) != 1 || got[0] != (sdpSection{addr: "192.168.20.226", rtcpAddr: "192.168.20.226", port: 15358, rtcpPort: 15359}) {
		t.Fatalf("%+v", got)
	}
	if got := sdpSections("c=IN IP4 10.0.0.1\r\nm=audio 40000 RTP/AVP 0\r\na=rtcp:\r\n"); len(got) != 1 || got[0].rtcpPort != 40001 || got[0].addr != "10.0.0.1" {
		t.Fatalf("%+v", got)
	}
}

// Where does not call a way clean on hops nobody reported on; with no
// trouble either way the summary is one line.
func TestWhereBlindHops(t *testing.T) {
	good := &qosDir{from: "b", to: "a", points: []qosPoint{{t: time.Unix(0, 0), jitter: 1}}}
	good2 := &qosDir{from: "b2", to: "c", points: []qosPoint{{t: time.Unix(0, 0), jitter: 1}}}
	legs := []*qosLeg{{a: "a", b: "b", aPos: 0, bPos: 1, back: good}, {a: "b2", b: "c", aPos: 1, bPos: 2, fwd: good2}}
	name := func(s string) string { return s }
	tags := regexp.MustCompile(`\[[^\[\]]*\]`)
	if got, bad, _ := where(legs, true, name, time.UTC); bad || tags.ReplaceAllString(got, "") != "clean where reported; no reports on hop 1" {
		t.Errorf("to the callee: %q", got)
	}
	if got, bad, _ := where(legs, false, name, time.UTC); bad || tags.ReplaceAllString(got, "") != "clean where reported; no reports on hop 2" {
		t.Errorf("to the caller: %q", got)
	}
	out := tags.ReplaceAllString(qosPath(legs, &mediaPlan{owners: mediaOwners{}}, 120, time.UTC), "")
	if !regexp.MustCompile(`trouble\s+none seen\s+hops without reports: → 1; ← 2`).MatchString(out) {
		t.Errorf("summary:\n%s", out)
	}
}

// The line is coloured by MOS: the spike at the start red, the rest green.
func TestMOSLine(t *testing.T) {
	t0 := time.Unix(0, 0)
	m := &metric{worst: lower, warn: mosShade}
	m.add(t0, 1.5)
	for i := 1; i <= 9; i++ {
		m.add(t0.Add(time.Duration(i)*5*time.Second), 4.4)
	}
	got := m.line(t0, t0.Add(45*time.Second), 10, 7*time.Second)
	if !strings.HasPrefix(got, theme.Tag(theme.Err)+"─") || strings.Count(got, theme.Tag(theme.OK)+"─") != 9 {
		t.Fatalf("%q", got)
	}
}

// A relay with an inside and an outside address is one column with both
// IPs under its name.
func TestPathNodesRelay(t *testing.T) {
	owners := mediaOwners{"10.0.0.1:4000": {"caller", 0}, "10.0.0.5:5000": {"GW", 1}, "203.0.113.5:6000": {"GW", 1}, "198.51.100.9:7000": {"carrier", 2}}
	legs := []*qosLeg{{a: "10.0.0.1:4000", b: "10.0.0.5:5000", aPos: 0, bPos: 1}, {a: "203.0.113.5:6000", b: "198.51.100.9:7000", aPos: 1, bPos: 2}}
	nodes, cols := pathNodes(legs, &mediaPlan{owners: owners})
	if len(nodes) != 3 || nodes[1].name != "GW" || strings.Join(nodes[1].ips, " / ") != "10.0.0.5 / 203.0.113.5" || cols[1] != [2]int{1, 2} {
		t.Fatalf("%+v %v", nodes, cols)
	}
}

// A call that comes back: the carrier calls in through the gateway to the
// PBX, the PBX forwards it out through the gateway to the carrier again.
// The media of the second call gets lanes of its own, as in the unfolded
// flow, and every hop sits between two neighbouring columns.
func TestQoSUnfolded(t *testing.T) {
	alias := map[string]string{"198.51.100.18:5060": "CARRIER", "203.0.113.6:5060": "GW", "192.168.20.6:5060": "GW", "192.168.21.51:5060": "PBX"}
	tx := &homer.Transaction{}
	sip := func(sid, src, dst, method, media string) {
		sdp := ""
		if media != "" {
			ip, port, _ := strings.Cut(media, ":")
			sdp = "v=0\r\nm=audio " + port + " RTP/AVP 8\r\nc=IN IP4 " + ip + "\r\n"
		}
		tx.Data.Calldata = append(tx.Data.Calldata, homer.CallElement{Sid: sid, SrcID: src, DstID: dst, SrcIP: strings.Split(src, ":")[0],
			DstIP: strings.Split(dst, ":")[0], AliasSrc: alias[src], AliasDst: alias[dst], Method: method})
		first := method + " sip:x SIP/2.0"
		if method[0] >= '1' && method[0] <= '6' {
			first = "SIP/2.0 " + method + " OK"
		}
		tx.Data.Messages = append(tx.Data.Messages, map[string]any{"payloadType": float64(1), "raw": first + "\r\nCSeq: 1 INVITE\r\n\r\n" + sdp})
	}
	const carrier, gwOut, gwIn, pbx = "198.51.100.18:5060", "203.0.113.6:5060", "192.168.20.6:5060", "192.168.21.51:5060"
	sip("in", carrier, gwOut, "INVITE", "198.51.100.18:30000")
	sip("in-b", gwIn, pbx, "INVITE", "192.168.20.26:20000")
	sip("in-b", pbx, gwIn, "200", "192.168.21.51:16000")
	sip("in", gwOut, carrier, "200", "203.0.113.226:20002")
	sip("out-b", pbx, gwIn, "INVITE", "192.168.21.51:16002")
	sip("out", gwOut, carrier, "INVITE", "203.0.113.226:20004")
	sip("out", carrier, gwOut, "200", "198.51.100.19:31000")
	sip("out-b", gwIn, pbx, "200", "192.168.20.26:20006")
	v := &CallView{tx: tx}
	plan := v.mediaPlan()

	// one receiver report a hop, each about the sound towards the callee
	var data []map[string]any
	rr := func(from, to string) { // media from → to, the RTCP goes back
		fi, fp, _ := strings.Cut(from, ":")
		ti, tp, _ := strings.Cut(to, ":")
		data = append(data, map[string]any{"timeSeconds": float64(1000), "srcIp": ti, "srcPort": tp, "dstIp": fi, "dstPort": fp,
			"raw": `{"type":201,"ssrc":7,"report_blocks":[{"source_ssrc":` + fp + `,"fraction_lost":0,"ia_jitter":8}]}`})
	}
	rr("198.51.100.18:30001", "203.0.113.226:20003")
	rr("192.168.20.26:20001", "192.168.21.51:16001")
	rr("192.168.21.51:16003", "192.168.20.26:20007")
	rr("203.0.113.226:20005", "198.51.100.19:31001")
	addr := func(ip string, port any) string { return fmt.Sprintf("%s:%v", ip, port) }
	legs := qosLegs(qosStreams(data, addr), plan)
	nodes, cols := pathNodes(legs, plan)
	var names []string
	for _, n := range nodes {
		names = append(names, n.name+" "+strings.Join(n.ips, "/"))
	}
	want := []string{"CARRIER 198.51.100.18", "GW 203.0.113.226/192.168.20.26", "PBX 192.168.21.51", "GW 192.168.20.26/203.0.113.226", "CARRIER 198.51.100.19"}
	if strings.Join(names, " | ") != strings.Join(want, " | ") {
		t.Fatalf("columns\n got %v\nwant %v", names, want)
	}
	for i, l := range legs {
		if cols[i] != [2]int{i, i + 1} || l.fwd == nil || l.back != nil {
			t.Errorf("hop %d: columns %v, to the callee %v, to the caller %v", i+1, cols[i], l.fwd, l.back)
		}
	}
	out := regexp.MustCompile(`\[[^\[\]]*\]`).ReplaceAllString(qosPath(legs, plan, 160, time.UTC), "")
	if !regexp.MustCompile(`callee hears\s+(│─+ ● 4\.4 ─+▶){4}│`).MatchString(out) {
		t.Errorf("scheme:\n%s", out)
	}
}

// Every host of the call is a column, with or without RTCP: a hop the
// offer and answer set up shows no data, and a proxy the media does not
// pass has the arrow go over it.
func TestQoSPlanWithoutReports(t *testing.T) {
	alias := map[string]string{"198.51.100.1:5060": "CARRIER", "10.0.0.2:5060": "PROXY", "10.0.0.3:5060": "PBX"}
	tx := &homer.Transaction{}
	sip := func(src, dst, first, media string) {
		sdp := ""
		if media != "" {
			sdp = "v=0\r\nc=IN IP4 " + media + "\r\nm=audio 40000 RTP/AVP 8\r\n"
		}
		method, _, _ := strings.Cut(first, " ")
		if method == "SIP/2.0" {
			method = strings.Fields(first)[1]
		}
		tx.Data.Calldata = append(tx.Data.Calldata, homer.CallElement{Sid: "s", SrcID: src, DstID: dst, SrcIP: strings.Split(src, ":")[0],
			DstIP: strings.Split(dst, ":")[0], AliasSrc: alias[src], AliasDst: alias[dst], Method: method})
		tx.Data.Messages = append(tx.Data.Messages, map[string]any{"payloadType": float64(1), "raw": first + "\r\nCSeq: 1 INVITE\r\n\r\n" + sdp})
	}
	sip("198.51.100.1:5060", "10.0.0.2:5060", "INVITE sip:x SIP/2.0", "198.51.100.1")
	sip("10.0.0.2:5060", "10.0.0.3:5060", "INVITE sip:x SIP/2.0", "198.51.100.1") // the proxy passes the offer on
	sip("10.0.0.3:5060", "10.0.0.2:5060", "SIP/2.0 200 OK", "10.0.0.3")
	sip("10.0.0.2:5060", "198.51.100.1:5060", "SIP/2.0 200 OK", "10.0.0.3")
	v := &CallView{tx: tx}
	plan := v.mediaPlan()
	legs := qosLegs(nil, plan)
	nodes, cols := pathNodes(legs, plan)
	var names []string
	for _, n := range nodes {
		names = append(names, n.name+" "+strings.Join(n.ips, "/"))
	}
	if strings.Join(names, " | ") != "CARRIER 198.51.100.1 | PROXY 10.0.0.2 | PBX 10.0.0.3" || len(legs) != 1 || cols[0] != [2]int{0, 2} {
		t.Fatalf("columns %v, hops %d %v", names, len(legs), cols)
	}
	out := regexp.MustCompile(`\[[^\[\]]*\]`).ReplaceAllString(qosPath(legs, plan, 120, time.UTC), "")
	if !regexp.MustCompile(`callee hears\s+│─+ no data ─+▶│`).MatchString(out) || !strings.Contains(out, "trouble       no RTCP reports, nothing to judge by") {
		t.Errorf("scheme:\n%s", out)
	}
}

// A WebRTC client offers 0.0.0.0 and its address over ICE: its column
// takes the address the reports come from.
func TestQoSICEAddress(t *testing.T) {
	plan := &mediaPlan{owners: mediaOwners{"192.168.20.25:14407": {"WEBRTC", 0}, "ip:192.168.20.25": {"WEBRTC", 0}, "0.0.0.0:9": {"client", 1}, "ip:192.168.22.75": {"client", 1}},
		hops: [][2]string{{"192.168.20.25:14407", "0.0.0.0:9"}}, hosts: []planHost{{"WEBRTC", "192.168.20.25"}, {"client", "192.168.22.75"}}}
	st := &qosStream{from: "192.168.20.25:14406", to: "192.168.22.75:62979", points: []qosPoint{{t: time.Unix(0, 0)}}}
	legs := qosLegs([]*qosStream{st}, plan)
	nodes, _ := pathNodes(legs, plan)
	if len(legs) != 1 || legs[0].b != "192.168.22.75:62979" || legs[0].fwd == nil || len(nodes) != 2 || strings.Join(nodes[1].ips, "/") != "192.168.22.75" {
		t.Fatalf("legs %+v nodes %+v", legs[0], nodes[1])
	}
}
