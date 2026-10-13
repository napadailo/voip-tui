package mock

import (
	"encoding/json"
	"sort"
	"time"
)

// ngCommands are the NG commands as jsonstats names them (the short names
// of control_ng.h, lowercased), in rtpengine's order.
var ngCommands = []string{"ping", "offer", "answer", "rollback", "delete", "query", "list", "startrec", "stoprec", "pauserec",
	"startfwd", "stopfwd", "blkdtmf", "unblkdtmf", "blkmedia", "unblkmedia", "playmedia", "stopmedia", "playdtmf", "stats",
	"slnmedia", "unslnmedia", "pub", "subreq", "subans", "unsub"}

// jsonStats answers `list jsonstats` in the layout of statistics.c: one line
// of JSON, sections by release. The numbers follow the calls and do not
// draw from s.rnd.
func (s *Server) jsonStats() string {
	n := len(s.calls)
	foreign := 0
	for _, c := range s.calls {
		if c.foreign {
			foreign++
		}
	}
	uptime := int(time.Since(s.started).Seconds())
	cur := map[string]any{
		"sessionsown": n - foreign, "sessionsforeign": foreign, "sessionstotal": n, "transcodedmedia": n / 7,
		"packetrate_user": n * 12, "byterate_user": n * 12 * 172, "errorrate_user": 0,
		"packetrate_kernel": n * 88, "byterate_kernel": n * 88 * 172, "errorrate_kernel": 0,
		"packetrate": n * 100, "byterate": n * 100 * 172, "errorrate": 0,
		"media_userspace": n / 5, "media_kernel": 2*n - n/5 - 1, "media_mixed": 1,
	}
	if s.at(12, 5) {
		cur["mediacache"], cur["playercache"] = 0, 0
	}
	relayed := s.managed * 7600
	total := map[string]any{
		"uptime": uptime, "managedsessions": s.managed, "rejectedsessions": 3,
		"timeoutsessions": s.timeouts, "silenttimeoutsessions": 2, "finaltimeoutsessions": 0, "offertimeoutsessions": 41,
		"regularterminatedsessions": s.managed - s.forced - s.timeouts - 46, "forcedterminatedsessions": s.forced,
		"relayedpackets_user": relayed / 8, "relayedpacketerrors_user": 12, "relayedbytes_user": relayed / 8 * 172,
		"relayedpackets_kernel": relayed - relayed/8, "relayedpacketerrors_kernel": 0, "relayedbytes_kernel": (relayed - relayed/8) * 172,
		"relayedpackets": relayed, "relayedpacketerrors": 12, "relayedbytes": relayed * 172,
		"zerowaystreams": 17, "onewaystreams": 230,
		"avgcallduration": 94.312, "totalcallsduration": 94.312 * float64(s.managed),
		"totalcallsduration2": 0, "totalcallsduration_stddev": 61.5,
	}
	out := map[string]any{"currentstatistics": cur, "totalstatistics": total}
	if s.at(11, 5) {
		samples := s.managed * 3
		out["mos"] = map[string]any{"mos_total": 4.21 * float64(samples), "mos2_total": 0, "mos_samples_total": samples, "mos_average": 4.21, "mos_stddev": 0.18}
		vm := map[string]any{"packets_lost": relayed / 2000, "rtp_duplicates": relayed / 90000, "rtp_skips": relayed / 40000,
			"rtp_seq_resets": 9, "rtp_reordered": relayed / 70000}
		for _, m := range []struct {
			name string
			avg  float64
		}{{"jitter", 3}, {"rtt_e2e", 18}, {"rtt_dsct", 6}, {"packetloss", 0.8}, {"jitter_measured", 2}} {
			vm[m.name+"_total"], vm[m.name+"2_total"], vm[m.name+"_samples_total"] = m.avg*float64(samples), 0, samples
			vm[m.name+"_average"], vm[m.name+"_stddev"] = m.avg, m.avg/4
		}
		out["voip_metrics"] = vm
	}
	out["controlstatistics"] = s.controlStats(uptime)
	var ifaces []any
	for _, i := range s.ifaces() {
		total := i.max - i.min + 1
		ifaces = append(ifaces, map[string]any{
			"name": i.name, "address": i.addr,
			"ports":   map[string]any{"min": i.min, "max": i.max, "used": i.used, "used_pct": float64(i.used) * 100 / float64(total), "free": total - i.used, "totals": total},
			"ingress": map[string]any{"packets": i.packets, "bytes": i.bytes, "errors": i.packets / 14000000},
			"egress":  map[string]any{"packets": i.packets - i.packets/90, "bytes": i.bytes - i.bytes/90, "errors": 0},
		})
	}
	out["interfaces"] = ifaces
	// the same chains as `list transcoders`; totals since the start from mr11.5
	tr := []map[string]any{
		{"chain": "PCMA/8000 <> opus/48000/2", "num": 3, "packetrate": 150, "byterate": 25800, "samplerate": 24000},
		{"chain": "PCMU/8000 <> G722/8000", "num": 1, "packetrate": 50, "byterate": 8600, "samplerate": 8000},
	}
	if s.at(11, 5) {
		tr[0]["packets"], tr[0]["bytes"], tr[0]["samples"] = 9120000, 1568640000, 1459200000
		tr[1]["packets"], tr[1]["bytes"], tr[1]["samples"] = 2100000, 361200000, 336000000
	}
	out["transcoders"] = []any{tr[0], tr[1]}
	b, _ := json.Marshal(out)
	return string(b) + "\n"
}

// controlStats are the NG commands per proxy socket: the proxies of the
// calls, with counts that follow their calls.
func (s *Server) controlStats(uptime int) map[string]any {
	calls := map[string]int{}
	for _, c := range s.calls {
		calls[c.proxy]++
	}
	socks := make([]string, 0, len(calls))
	for p := range calls {
		socks = append(socks, p)
	}
	sort.Strings(socks)
	totals := map[string]int{}
	var proxies []any
	for i, p := range socks {
		offers := calls[p] * 61
		count := map[string]int{"ping": uptime / 30, "offer": offers, "answer": offers * 93 / 100, "delete": offers * 97 / 100, "query": calls[p]}
		proxy := map[string]any{"proxy": p, "errorcount": 0}
		if i == 0 {
			proxy["errorcount"] = 3 // a proxy sending bad requests
		}
		for _, cmd := range ngCommands {
			proxy[cmd+"count"] = count[cmd]
			proxy[cmd+"duration"] = float64(count[cmd]) * map[bool]float64{true: 0.00082, false: 0.00011}[cmd == "offer" || cmd == "answer"]
			totals[cmd] += count[cmd]
		}
		proxies = append(proxies, proxy)
	}
	cs := map[string]any{"proxies": proxies}
	for _, cmd := range ngCommands {
		cs["total"+cmd+"count"] = totals[cmd]
	}
	return cs
}
