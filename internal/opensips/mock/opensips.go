// Package mock emulates an OpenSIPS host behind ssh: the probe shell script,
// opensips-cli -x mi and opensipsctl fifo, for tests and opensips-tui-mock.
// State is live: an ended dialog disappears, a disabled gateway stays disabled,
// counters grow.
package mock

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/napadailo/voip-tui/internal/opensips"
)

var coreCommands = []string{"version", "uptime", "which", "ps", "log_level", "xlog_level", "get_statistics", "list_statistics",
	"reset_statistics", "list_tcp_conns", "mem_pkg_dump", "mem_shm_dump", "reload_routes", "debug", "kill", "shm_check"}

var moduleCommands = map[string][]string{
	"dialog":     {"dlg_list", "dlg_list_ctx", "dlg_end_dlg", "dlg_db_sync", "profile_get_size", "list_all_profiles"},
	"usrloc":     {"ul_dump", "ul_show_contact", "ul_rm", "ul_rm_contact", "ul_add", "ul_flush", "ul_sync"},
	"dispatcher": {"ds_list", "ds_set_state", "ds_reload", "ds_push_script_attrs"},
	"drouting":   {"dr_reload", "dr_reload_status", "dr_gw_status", "dr_carrier_status", "dr_number_routing"},
	"rtpengine":  {"rtpengine_show", "rtpengine_enable", "rtpengine_reload"},
	"clusterer":  {"clusterer_list", "clusterer_list_shtags", "clusterer_reload", "clusterer_set_status"},
	"tm":         {"t_uac_dlg", "t_uac_cancel", "t_hash", "t_reply"},
}

// Named MI 3.x params the emulator accepts: catches mismatches with the capability table.
var namedParams = map[string][]string{
	"dlg_end_dlg":      {"dialog_id", "extra_hdrs"},
	"ul_rm_contact":    {"table_name", "aor", "contact"},
	"ul_rm":            {"table_name", "aor"},
	"ds_set_state":     {"state", "group", "address", "partition"},
	"ds_reload":        {"partition"},
	"dr_gw_status":     {"partition_name", "gw_id", "status"},
	"dr_reload":        {"partition_name"},
	"rtpengine_enable": {"url", "enable"},
	"log_level":        {"level", "pid"},
	"xlog_level":       {"level"},
}

var (
	names  = []string{"Alice", "Bob", "Carol", "Dave", "Erin", "Frank", "Grace", "Heidi", "Ivan", "Judy"}
	agents = []string{"Yealink SIP-T46U 108.86.0.20", "Grandstream GXP2170 1.0.11.23", "Zoiper rv2.10.19.4",
		"Linphone/5.2.5 (belle-sip/5.2.94)", "Cisco-SIPGateway/IOS-15.x", "MicroSIP/3.21.3"}
)

type obj = map[string]any

// Host is an emulated OpenSIPS. Fields are exported so the state survives between
// opensips-tui-mock processes as JSON.
type Host struct {
	Name     string
	Version  string
	Binary   string
	Modules  []string
	Without  []string // modules that are not loaded, MI transports included
	Started  int64
	Seed     int64
	Xlog     int64 // xlog_level
	Counters map[string]float64
	Ticks    map[string]int64
	Dialogs  []obj
	AORs     []obj
	Dests    []obj
	Gateways []obj
	Engines  []obj
	Procs    []obj

	mu  sync.Mutex
	rnd *rand.Rand
}

// NewHost creates a host; version "" means 3.4.7.
func NewHost(name, version, binary string, without []string) *Host {
	if version == "" {
		version = "3.4.7"
	}
	if binary == "" {
		binary = version
	}
	seed := int64(0)
	for _, ch := range name {
		seed = seed*31 + int64(ch)
	}
	h := &Host{Name: name, Version: version, Binary: binary, Seed: seed, Counters: map[string]float64{}, Ticks: map[string]int64{}}
	h.rnd = rand.New(rand.NewSource(seed))
	h.Without = without
	h.Started = time.Now().Unix() - 3600 - h.rnd.Int63n(40*86400)
	skip := map[string]bool{}
	for _, w := range without {
		skip[w] = true
	}
	for m := range moduleCommands {
		if !skip[m] {
			h.Modules = append(h.Modules, m)
		}
	}
	sort.Strings(h.Modules)
	for i := 0; i < 12+h.rnd.Intn(18); i++ {
		h.Dialogs = append(h.Dialogs, h.dialog())
	}
	h.AORs = h.contacts()
	h.Dests = h.destinations()
	h.Gateways = h.gateways()
	h.Engines = []obj{
		{"set": 0, "url": "udp:10.10.0.11:2223", "disabled": 0, "weight": 1, "recheck_ticks": 0},
		{"set": 0, "url": "udp:10.10.0.12:2223", "disabled": 0, "weight": 1, "recheck_ticks": 0},
		{"set": 1, "url": "udp:10.20.0.11:2223", "disabled": 1, "weight": 1, "recheck_ticks": 60},
	}
	h.Procs = h.processes()
	h.Xlog = 2 // notice
	return h
}

// pid is the PID of the main process, the first one.
func (h *Host) pid() int {
	if len(h.Procs) == 0 {
		return 0
	}
	return toInt(h.Procs[0]["PID"])
}

func (h *Host) random() *rand.Rand {
	if h.rnd == nil {
		h.rnd = rand.New(rand.NewSource(h.Seed + time.Now().UnixNano()))
	}
	return h.rnd
}

func (h *Host) version() opensips.Version { v, _ := opensips.ParseVersion(h.Version); return v }

// number is a fictional one: 555-0100…0199 is reserved for fiction in the NANP.
func (h *Host) number() string {
	r := h.random()
	return fmt.Sprintf("1%d55501%02d", []int{202, 312, 415, 617, 718}[r.Intn(5)], r.Intn(100))
}

// sdp is an audio offer or answer from addr:port.
func sdp(addr string, port int) string {
	return fmt.Sprintf("v=0\r\no=- 3912 3912 IN IP4 %[1]s\r\ns=-\r\nc=IN IP4 %[1]s\r\nt=0 0\r\n"+
		"m=audio %[2]d RTP/AVP 8 0 101\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:101 telephone-event/8000\r\na=sendrecv\r\n", addr, port)
}

func (h *Host) dialog() obj {
	r := h.random()
	hEntry, hID := 1+r.Intn(4095), 1+r.Intn(60000)
	start := time.Now().Unix() - int64(2+r.Intn(3000))
	state := []int{4, 4, 4, 4, 2, 1, 3}[r.Intn(7)]
	from, to := h.number(), h.number()
	ts := int64(0)
	if state >= 3 {
		ts = start
	}
	d := obj{
		"ID": fmt.Sprint(int64(hEntry)<<32 | int64(hID)), "h_entry": hEntry, "h_id": hID, "state": state,
		"user_flags": 0, "timestart": ts, "timeout": start + 7200,
		"callid":   fmt.Sprintf("%016x@10.0.%d.%d", r.Uint64(), r.Intn(10), 2+r.Intn(248)),
		"from_uri": "sip:" + from + "@sip.example.com", "to_uri": "sip:" + to + "@sip.example.com",
		"caller_tag": fmt.Sprintf("%08x", r.Uint32()), "caller_contact": fmt.Sprintf("sip:%s@10.0.1.%d:5060", from, 2+r.Intn(248)),
		"caller_cseq": "1", "caller_bind_addr": "udp:10.0.0.1:5060",
		"caller_sdp": sdp(fmt.Sprintf("10.0.1.%d", 2+r.Intn(248)), 20000+2*r.Intn(5000)), "caller_sent_sdp": "",
		"CALLEES": []any{obj{"callee_tag": fmt.Sprintf("%08x", r.Uint32()),
			"callee_contact": fmt.Sprintf("sip:%s@10.0.2.%d:5060", to, 2+r.Intn(248)), "callee_cseq": "1",
			"callee_bind_addr": "udp:10.0.0.1:5060", "callee_sdp": "", "callee_sent_sdp": ""}},
	}
	// once answered, the SDP went both ways, through rtpengine at 10.10.0.11
	if state >= 3 {
		d["caller_sent_sdp"] = sdp("10.10.0.11", 30000+2*r.Intn(5000))
		callee := d["CALLEES"].([]any)[0].(obj)
		callee["callee_sdp"] = sdp(fmt.Sprintf("10.0.2.%d", 2+r.Intn(248)), 20000+2*r.Intn(5000))
		callee["callee_sent_sdp"] = sdp("10.10.0.11", 30000+2*r.Intn(5000))
	}
	return d
}

func (h *Host) contacts() []obj {
	r := h.random()
	var aors []obj
	for i, name := range names {
		aor := fmt.Sprint(1000 + i)
		var cts []any
		for j := 0; j < []int{1, 1, 1, 2}[r.Intn(4)]; j++ {
			tr := []string{"udp", "tcp", "tls"}[r.Intn(3)]
			port := 5060
			if tr == "tls" {
				port = 5061
			}
			received := ""
			if r.Intn(10) < 4 {
				received = fmt.Sprintf("sip:198.51.100.%d:%d", 1+r.Intn(250), 1024+r.Intn(60000)) // documentation range (RFC 5737)
			}
			cts = append(cts, obj{
				"Contact":   fmt.Sprintf("sip:%s@192.168.%d.%d:%d;transport=%s", aor, 1+r.Intn(50), 2+r.Intn(248), []int{5060, 5062, 41234, 52000}[r.Intn(4)], tr),
				"ContactID": fmt.Sprint(r.Int63n(1 << 48)), "Expires": 30 + r.Intn(3570), "Q": "",
				"Callid": fmt.Sprintf("%x", r.Int63n(1<<40)), "Cseq": 1 + r.Intn(900), "User-agent": agents[r.Intn(len(agents))],
				"Received": received, "State": "CS_SYNC", "Flags": 0, "Cflags": "",
				"Socket": fmt.Sprintf("%s:10.0.0.1:%d", tr, port), "Methods": 8063, "Attr": name,
			})
		}
		aors = append(aors, obj{"AOR": aor, "Contacts": cts})
	}
	return aors
}

func (h *Host) destinations() []obj {
	var out []obj
	for _, set := range []int{1, 2} {
		for i := 1; i <= 3; i++ {
			state := "Active"
			if set == 2 && i == 3 {
				state = "Probing"
			}
			out = append(out, obj{"partition": "default", "set": set, "URI": fmt.Sprintf("sip:10.3%d.0.%d:5060", set-1, i),
				"state": state, "weight": 1, "priority": 0, "attrs": fmt.Sprintf("node=%d%d", set, i),
				"description": fmt.Sprintf("media-%d-%d", set, i)})
		}
	}
	return out
}

func (h *Host) gateways() []obj {
	var out []obj
	for i, carrier := range []string{"carrier-a", "carrier-b", "carrier-c", "carrier-d", "carrier-e"} {
		state := "Active"
		if carrier == "carrier-d" {
			state = "Inactive"
		}
		out = append(out, obj{"partition": "external", "ID": carrier, "IP": fmt.Sprintf("203.0.113.%d:5060", 10+i), // RFC 5737
			"State": state, "ATTRS": "carrier=" + carrier + ";cps=50"})
	}
	for i := 1; i <= 2; i++ {
		out = append(out, obj{"partition": "internal", "ID": fmt.Sprintf("pbx%d", i), "IP": fmt.Sprintf("10.40.0.%d:5060", i), "State": "Active", "ATTRS": ""})
	}
	return out
}

func (h *Host) processes() []obj {
	types := []string{"attendant", "HTTPD 0.0.0.0:8888", "MI FIFO", "time_keeper", "timer",
		"SIP receiver udp:10.0.0.1:5060", "SIP receiver udp:10.0.0.1:5060", "SIP receiver udp:10.0.0.1:5060",
		"SIP receiver udp:10.0.0.1:5060", "TCP receiver", "TCP receiver", "TCP main", "Timer handler", "Bin receiver bin:10.0.0.1:5566"}
	pid := 2400 + h.random().Intn(2000)
	var out []obj
	for i, t := range types {
		out = append(out, obj{"ID": i, "PID": pid + i, "Type": t, "Log level": 3})
	}
	return out
}

func (h *Host) tick(name string, base, rate float64) int64 {
	now := time.Now().UnixMilli()
	if _, ok := h.Counters[name]; !ok {
		h.Counters[name] = base
		h.Ticks[name] = now
	}
	elapsed := float64(now-h.Ticks[name]) / 1000
	h.Counters[name] += rate * elapsed * (0.6 + 0.8*h.random().Float64())
	h.Ticks[name] = now
	return int64(h.Counters[name])
}

func (h *Host) has(module string) bool {
	for _, m := range h.Modules {
		if m == module {
			return true
		}
	}
	return false
}

func (h *Host) statistics() obj {
	r := h.random()
	var shm int64 = 268435456
	st := obj{
		"core:rcv_requests": h.tick("rcv_requests", 18e6, 120), "core:rcv_replies": h.tick("rcv_replies", 21e6, 140),
		"core:fwd_requests": h.tick("fwd_requests", 9e6, 60), "core:drop_requests": h.tick("drop_requests", 1200, 0.05),
		"core:err_requests": h.tick("err_requests", 300, 0.01), "core:slow_messages": h.tick("slow_messages", 12, 0.001),
		"core:timestamp":   time.Now().Unix(),
		"shmem:total_size": shm, "shmem:used_size": int64(float64(shm) * (0.18 + 0.04*r.Float64())),
		"shmem:real_used_size": int64(float64(shm) * (0.23 + 0.04*r.Float64())), "shmem:max_used_size": int64(float64(shm) * 0.41),
		"shmem:free_size": int64(float64(shm) * 0.75), "shmem:fragments": 4000 + r.Intn(2000),
		"load:load": 2 + r.Intn(13), "load:load1m": 3 + r.Intn(9), "load:load10m": 8,
		"net:waiting_udp": r.Intn(4), "net:waiting_tcp": 0, "net:waiting_tls": 0,
		"sl:1xx_replies": h.tick("sl_1xx", 1000, 0.1), "sl:200_replies": h.tick("sl_200", 9e5, 8),
		"sl:4xx_replies": h.tick("sl_4xx", 3e4, 0.6), "sl:5xx_replies": h.tick("sl_5xx", 900, 0.02),
	}
	if h.has("tm") {
		st["tm:received_replies"] = h.tick("tm_rpl", 7e6, 50)
		st["tm:inuse_transactions"] = 10 + r.Intn(70)
		st["tm:2xx_transactions"] = h.tick("tm_2xx", 3e6, 25)
		st["tm:4xx_transactions"] = h.tick("tm_4xx", 4e5, 3)
		st["tm:5xx_transactions"] = h.tick("tm_5xx", 9e3, 0.08)
		st["tm:timeout_finalresponse"] = h.tick("tm_to", 2e3, 0.01)
	}
	if h.has("dialog") {
		early := 0
		for _, d := range h.Dialogs {
			if toInt(d["state"]) < 3 {
				early++
			}
		}
		st["dialog:active_dialogs"] = len(h.Dialogs)
		st["dialog:early_dialogs"] = early
		st["dialog:processed_dialogs"] = h.tick("dlg_processed", 6e5, 2)
		st["dialog:failed_dialogs"] = h.tick("dlg_failed", 9e4, 0.3)
	}
	if h.has("usrloc") {
		n := 0
		for _, a := range h.AORs {
			n += len(a["Contacts"].([]any))
		}
		st["usrloc:registered_users"] = len(h.AORs)
		st["usrloc:location-users"] = len(h.AORs)
		st["usrloc:location-contacts"] = n
	}
	for _, p := range h.Procs {
		st[fmt.Sprintf("pkmem:%v-real_used_size", p["ID"])] = 300000 + r.Intn(600000)
		st[fmt.Sprintf("pkmem:%v-total_size", p["ID"])] = 16777216
	}
	return st
}

// Commands lists MI commands of loaded modules.
func (h *Host) Commands() []string {
	cmds := append([]string{}, coreCommands...)
	for _, m := range h.Modules {
		cmds = append(cmds, moduleCommands[m]...)
	}
	if !h.version().Less(opensips.V("3.2")) {
		cmds = append(cmds, "sr_get_status", "sr_list_status", "sr_list_reports", "sr_list_identifiers")
	}
	return cmds
}

// Failure is an MI error.
type Failure struct {
	Code    int
	Message string
}

func (f *Failure) Error() string { return fmt.Sprintf("%d: %s", f.Code, f.Message) }

// Call runs an MI command with command-line args (k=v named or positional).
func (h *Host) Call(command string, args []string) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	known := false
	for _, c := range h.Commands() {
		known = known || c == command
	}
	if !known {
		return nil, &Failure{-32601, "Method not found"}
	}
	named := map[string]string{}
	var pos []string
	allowed, hasNamed := namedParams[command]
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if ok && hasNamed {
			valid := false
			for _, x := range allowed {
				valid = valid || x == k
			}
			if !valid {
				return nil, &Failure{-32602, "Invalid params"}
			}
			named[k] = v
		} else {
			pos = append(pos, a)
		}
	}
	return h.call(command, named, pos)
}

func (h *Host) call(command string, named map[string]string, pos []string) (any, error) {
	now := time.Now()
	switch command {
	case "version":
		return obj{"Server": fmt.Sprintf("OpenSIPS (%s (x86_64/linux))", h.Version)}, nil
	case "uptime":
		const f = "Mon Jan 02 15:04:05 2006"
		return obj{"Now": now.Format(f), "Up since": time.Unix(h.Started, 0).Format(f),
			"Up time": fmt.Sprintf("%d [sec]", now.Unix()-h.Started)}, nil
	case "which":
		return toAny(h.Commands()), nil
	case "ps":
		var ps []any
		for _, p := range h.Procs {
			ps = append(ps, obj{"ID": p["ID"], "PID": p["PID"], "Type": p["Type"]})
		}
		return obj{"Processes": ps}, nil
	case "xlog_level":
		if lvl, ok := named["level"]; ok {
			h.Xlog = int64(toInt(lvl))
			return obj{"New xLog Level": h.Xlog}, nil
		}
		return obj{"xLog Level": h.Xlog}, nil
	case "log_level":
		if lvl, ok := named["level"]; ok {
			found := false
			for _, p := range h.Procs {
				if pid, ok := named["pid"]; !ok || fmt.Sprint(p["PID"]) == pid {
					p["Log level"] = toInt(lvl)
					found = true
				}
			}
			if !found {
				return nil, &Failure{404, "Processes not found"}
			}
			return "OK", nil
		}
		var ps []any
		for _, p := range h.Procs {
			ps = append(ps, obj{"PID": p["PID"], "Log level": p["Log level"], "Type": p["Type"]})
		}
		return obj{"Processes": ps}, nil
	case "get_statistics":
		st := h.statistics()
		if len(pos) == 0 || contains(pos, "all") {
			return st, nil
		}
		out := obj{}
		for _, w := range pos {
			for k, v := range st {
				_, short, _ := strings.Cut(k, ":")
				if (strings.HasSuffix(w, ":") && strings.HasPrefix(k, w)) || short == w {
					out[k] = v
				}
			}
		}
		return out, nil
	case "reset_statistics":
		for _, n := range pos {
			_, short, _ := strings.Cut(n, ":")
			for k := range h.Counters {
				if strings.HasSuffix(short, strings.TrimPrefix(k, "sl_")) || k == short {
					h.Counters[k] = 0
				}
			}
		}
		return "OK", nil
	case "dlg_list":
		return obj{"Dialogs": toAnyObj(h.Dialogs)}, nil
	case "dlg_end_dlg":
		if len(pos) > 0 {
			return nil, &Failure{-32602, "Invalid params"}
		}
		for i, d := range h.Dialogs {
			if d["ID"] == named["dialog_id"] || d["callid"] == named["dialog_id"] {
				h.Dialogs = append(h.Dialogs[:i], h.Dialogs[i+1:]...)
				return "OK", nil
			}
		}
		return nil, &Failure{404, "Requested Dialog not found"}
	case "ul_dump":
		return obj{"Domains": []any{obj{"name": "location", "hash_size": 512, "AORs": toAnyObj(h.AORs)}}}, nil
	case "ul_rm_contact", "ul_rm":
		for i, a := range h.AORs {
			if a["AOR"] != named["aor"] {
				continue
			}
			if command == "ul_rm" {
				h.AORs = append(h.AORs[:i], h.AORs[i+1:]...)
				return "OK", nil
			}
			cts := a["Contacts"].([]any)
			for j, ct := range cts {
				if ct.(obj)["Contact"] == named["contact"] {
					a["Contacts"] = append(cts[:j], cts[j+1:]...)
					if len(a["Contacts"].([]any)) == 0 {
						h.AORs = append(h.AORs[:i], h.AORs[i+1:]...)
					}
					return "OK", nil
				}
			}
			return nil, &Failure{404, "Contact not found"}
		}
		return nil, &Failure{404, "AOR not found"}
	case "ds_list":
		sets := map[int][]any{}
		var ids []int
		for _, d := range h.Dests {
			s := toInt(d["set"])
			if _, ok := sets[s]; !ok {
				ids = append(ids, s)
			}
			sets[s] = append(sets[s], obj{"URI": d["URI"], "state": d["state"], "first_hit_counter": h.random().Intn(9000),
				"weight": d["weight"], "priority": d["priority"], "attributes": d["attrs"], "description": d["description"]})
		}
		var out []any
		for _, s := range ids {
			out = append(out, obj{"id": s, "Destinations": sets[s]})
		}
		return obj{"PARTITIONS": []any{obj{"name": "default", "SETS": out}}}, nil
	case "ds_set_state":
		for _, d := range h.Dests {
			if fmt.Sprint(d["set"]) == named["group"] && d["URI"] == named["address"] {
				switch strings.ToLower(named["state"])[:1] {
				case "a":
					d["state"] = "Active"
				case "i":
					d["state"] = "Inactive"
				case "p":
					d["state"] = "Probing"
				}
				return "OK", nil
			}
		}
		return nil, &Failure{404, "destination not found"}
	case "dr_reload_status":
		date := time.Unix(h.Started+60, 0).Format("Mon Jan 02 15:04:05 2006")
		return obj{"Partitions": []any{obj{"name": "external", "Date": date}, obj{"name": "internal", "Date": date}}}, nil
	case "dr_gw_status":
		part := named["partition_name"]
		if part != "" && part != "external" && part != "internal" {
			return nil, &Failure{400, "Partition not found"}
		}
		var gws []obj
		for _, g := range h.Gateways {
			if part == "" || g["partition"] == part {
				gws = append(gws, g)
			}
		}
		if id, ok := named["gw_id"]; ok {
			for _, g := range gws {
				if g["ID"] == id {
					if st, ok := named["status"]; ok {
						g["State"] = "Disabled MI"
						if st == "1" {
							g["State"] = "Active"
						}
						return "OK", nil
					}
					return obj{"State": g["State"]}, nil
				}
			}
			return nil, &Failure{404, "Destination ID not found"}
		}
		var out []any
		for _, g := range gws {
			out = append(out, obj{"ID": g["ID"], "IP": g["IP"], "State": g["State"], "ATTRS": g["ATTRS"]})
		}
		return obj{"Gateways": out}, nil
	case "rtpengine_show":
		sets := map[int][]any{}
		var ids []int
		for i, e := range h.Engines {
			s := toInt(e["set"])
			if _, ok := sets[s]; !ok {
				ids = append(ids, s)
			}
			sets[s] = append(sets[s], obj{"url": e["url"], "index": i, "disabled": e["disabled"], "weight": e["weight"], "recheck_ticks": e["recheck_ticks"]})
		}
		var out []any
		for _, s := range ids {
			out = append(out, obj{"Set": s, "Nodes": sets[s]})
		}
		return out, nil
	case "rtpengine_enable":
		for _, e := range h.Engines {
			if e["url"] == named["url"] {
				e["disabled"], e["recheck_ticks"] = 1, 60
				if named["enable"] == "1" {
					e["disabled"], e["recheck_ticks"] = 0, 0
				}
				return "OK", nil
			}
		}
		return nil, &Failure{404, "rtpengine not found"}
	case "clusterer_list":
		peer := "edge-n2"
		if h.Name == "edge-n2" {
			peer = "edge-n1"
		}
		return obj{"Clusters": []any{
			obj{"cluster_id": 1, "Nodes": []any{obj{"node_id": 2, "db_id": 2, "url": "bin:10.0.0.2:5566", "link_state": "Up", "state": "enabled", "next_hop": "2", "description": peer}}},
			obj{"cluster_id": 2, "Nodes": []any{obj{"node_id": 3, "db_id": 3, "url": "bin:10.1.0.3:5566", "link_state": "Down", "state": "enabled", "next_hop": "none", "description": "dr-site"}}},
		}}, nil
	case "clusterer_list_shtags":
		vip := "backup"
		if strings.HasSuffix(h.Name, "1") {
			vip = "active"
		}
		return []any{obj{"Tag": "vip", "Cluster": 1, "State": vip}, obj{"Tag": "dlg", "Cluster": 1, "State": "active"}}, nil
	case "list_tcp_conns":
		var out []any
		for i, t := range []string{"tls", "tls", "tcp", "wss"} {
			out = append(out, obj{"ID": 101 + i, "Type": t, "State": 0, "Remote": fmt.Sprintf("192.168.%d.20:%d", i+1, 40001+i),
				"Local": "10.0.0.1:5061", "Lifetime": now.Add(10 * time.Minute).Format("2006-01-02 15:04:05"), "Alias port": 0})
		}
		return obj{"Connections": out}, nil
	case "sr_list_status":
		return []any{
			obj{"Group": "core", "Identifiers": []any{obj{"Name": "main", "Readiness": true, "Status": 3, "Details": "running"}}},
			obj{"Group": "drouting", "Identifiers": []any{
				obj{"Name": "external", "Readiness": true, "Status": 1, "Details": "data available"},
				obj{"Name": "internal", "Readiness": true, "Status": 1, "Details": "data available"}}},
		}, nil
	}
	return "OK", nil
}

// Fifo answers opensipsctl fifo (2.x text MI).
func (h *Host) Fifo(command string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !contains(h.Commands(), command) {
		return "", &Failure{500, "command not available"}
	}
	switch command {
	case "version":
		return fmt.Sprintf("Server:: OpenSIPS (%s (x86_64/linux))", h.Version), nil
	case "uptime":
		return fmt.Sprintf("Now:: %s\nUp since:: %s\nUp time:: %d [sec]", time.Now().Format(time.ANSIC),
			time.Unix(h.Started, 0).Format(time.ANSIC), time.Now().Unix()-h.Started), nil
	case "which":
		return strings.Join(h.Commands(), "\n"), nil
	case "ps":
		var b strings.Builder
		for _, p := range h.Procs {
			fmt.Fprintf(&b, "Process:: ID=%v PID=%v Type=%v\n", p["ID"], p["PID"], p["Type"])
		}
		return strings.TrimSpace(b.String()), nil
	case "dlg_list":
		var b strings.Builder
		for _, d := range h.Dialogs {
			fmt.Fprintf(&b, "dialog:: hash=%v:%v\n\tstate:: %v\n\tcallid:: %v\n", d["h_entry"], d["h_id"], d["state"], d["callid"])
		}
		return strings.TrimSpace(b.String()), nil
	}
	return "", nil
}

// MarshalState serializes the host (opensips-tui-mock keeps it between runs).
func (h *Host) MarshalState() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return json.Marshal(h)
}

// LoadHost restores a host saved by MarshalState.
func LoadHost(data []byte) (*Host, error) {
	h := &Host{}
	if err := json.Unmarshal(data, h); err != nil {
		return nil, err
	}
	return h, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func toAny(l []string) []any {
	out := make([]any, len(l))
	for i, s := range l {
		out[i] = s
	}
	return out
}

func toAnyObj(l []obj) []any {
	out := make([]any, len(l))
	for i, o := range l {
		out[i] = o
	}
	return out
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	case int64:
		return int(x)
	case string:
		var n int
		fmt.Sscan(x, &n)
		return n
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	}
	return 0
}
