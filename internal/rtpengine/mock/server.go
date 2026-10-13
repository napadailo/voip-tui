// Package mock is a fake rtpengine listen-cli that answers in the format of
// a chosen release, used for tests and for trying rtpengine-tui without
// rtpengine. Command availability follows the release table of the
// rtpengine package; reply layouts follow daemon/cli.c of each branch.
package mock

import (
	"bufio"
	"fmt"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/napadailo/voip-tui/internal/rtpengine"
)

type call struct {
	id       string
	created  time.Time
	proxy    string
	foreign  bool
	deleted  bool
	keyspace int
	codec    string
	peer     string
	oneWay   bool // the callee sends no RTP
}

// Server emulates one rtpengine instance.
type Server struct {
	Version rtpengine.Version
	// Freeze stops calls from coming and going (tests).
	Freeze bool

	mu        sync.Mutex
	caps      rtpengine.Caps
	rnd       *rand.Rand
	calls     map[string]*call
	started   time.Time
	lastTick  time.Time
	managed   int
	timeouts  int
	forced    int
	start     map[string]string // params start
	cur       map[string]string // params current
	keyspaces []int
	loglevel  int            // before mr9.5: one level
	levels    map[string]int // mr9.5+: per subsystem
	logFile   string         // see WriteLog
	ip        string
	Requests  int
}

// New creates a server emulating version (e.g. "mr12.5.1.3") with
// generated calls.
func New(version string, seed int64) *Server {
	v, err := rtpengine.ParseVersion(version)
	if err != nil {
		v, _ = rtpengine.ParseVersion("mr13.3.1.1")
	}
	s := &Server{
		Version:  v,
		caps:     rtpengine.CapsForVersion(v),
		rnd:      rand.New(rand.NewSource(seed)),
		calls:    map[string]*call{},
		started:  time.Now().Add(-time.Duration(86400*3+int(seed%5000)) * time.Second),
		loglevel: 6,
		levels:   map[string]int{},
		ip:       fmt.Sprintf("10.20.%d.%d", 10+seed%40, 11+seed%30),
	}
	s.lastTick = time.Now()
	s.start = map[string]string{
		"maxsessions": "-1", "maxcpu": "0.0", "maxload": "0.00", "maxbw": "0", "maxopenfiles": "65536",
		"timeout": "60", "silenttimeout": "3600", "offertimeout": "3600", "finaltimeout": "0",
		"deletedelay": "30", "controltos": "184", "redisallowederrors": "-1", "redisdisabletime": "10",
		"redisconnecttimeout": "1000", "rediscmdtimeout": "0",
	}
	s.cur = map[string]string{}
	for k, v := range s.start {
		s.cur[k] = v
	}
	// somebody tuned these at runtime, so they differ from rtpengine.conf
	s.cur["silenttimeout"] = "1800"
	s.cur["maxsessions"] = "2000"
	s.managed = 40000 + s.rnd.Intn(90000)
	for i := 0; i < 12+s.rnd.Intn(25); i++ {
		s.addCall(time.Now().Add(-time.Duration(s.rnd.Intn(2400)) * time.Second))
	}
	s.keyspaces = []int{1}
	return s
}

var codecs = []string{"PCMA/8000", "PCMU/8000", "opus/48000/2", "G722/8000", "telephone-event/8000"}

func (s *Server) addCall(at time.Time) {
	id := fmt.Sprintf("%08x-%04x-%04x@%s", s.rnd.Uint32(), s.rnd.Intn(0xffff), s.rnd.Intn(0xffff), []string{"pbx1.example.com", "10.10.0.14", "sbc.example.com"}[s.rnd.Intn(3)])
	c := &call{
		id: id, created: at, proxy: fmt.Sprintf("10.10.0.%d:%d", 5+s.rnd.Intn(4), 40001+s.rnd.Intn(4)), // NG from one of the proxy workers
		foreign: s.rnd.Intn(7) == 0, keyspace: 1, codec: codecs[s.rnd.Intn(len(codecs)-1)], oneWay: s.rnd.Intn(5) == 0,
	}
	// unused draw: it keeps the random sequence, so a seed gives the same calls (seed 7 in demo-gif)
	s.rnd.Intn(255)
	c.peer = fmt.Sprintf("203.0.113.%d", 1+s.rnd.Intn(250)) // documentation range (RFC 5737)
	if c.foreign {
		c.keyspace = 2
	}
	s.calls[id] = c
	s.managed++
	s.logCall(c)
}

// tick lets calls come and go between refreshes.
func (s *Server) tick() {
	if s.Freeze {
		return
	}
	now := time.Now()
	steps := int(now.Sub(s.lastTick) / (700 * time.Millisecond))
	if steps == 0 {
		return
	}
	s.lastTick = now
	for i := 0; i < min(steps, 20); i++ {
		if s.rnd.Intn(3) > 0 {
			s.addCall(now)
		}
		for id := range s.calls {
			if s.rnd.Intn(30) == 0 {
				s.logf(now, "INFO", id, "Received command 'delete' from %s", s.calls[id].proxy)
				delete(s.calls, id)
				break
			}
		}
	}
}

// Listen serves the CLI on addr until the listener is closed.
func (s *Server) Listen(addr string) (net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	go s.Serve(l)
	return l, nil
}

// Serve accepts CLI connections: one command line, one reply, close.
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			line, err := bufio.NewReaderSize(c, 1024).ReadString('\n')
			if err != nil && line == "" {
				return
			}
			_, _ = c.Write([]byte(s.Handle(strings.TrimSpace(line))))
		}()
	}
}

func (s *Server) at(maj, min int) bool { return s.Version.AtLeast(maj, min) }

func (s *Server) unknown(rest string) string {
	if s.at(13, 2) {
		f := strings.Fields(rest)
		if len(f) == 0 {
			return "Incomplete command: \n"
		}
		return "Unknown command: " + f[0] + "\n"
	}
	return "Unknown or incomplete command:" + rest + "\n"
}

// Handle answers one CLI line.
func (s *Server) Handle(line string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	s.tick()
	f := strings.Fields(line)
	if len(f) == 0 {
		return s.unknown("")
	}
	cmd, args := f[0], f[1:]
	switch cmd {
	case "help":
		if !s.caps.Has("help") {
			return s.unknown(line)
		}
		return s.help(s.topCommands())
	case "usage":
		if !s.caps.Has("usage") {
			return s.unknown(line)
		}
		return "list numsessions\nlist sessions <callid> | all | own | foreign\n…\n"
	case "list", "get":
		if len(args) == 0 {
			return "More parameters required.\n"
		}
		return s.list(args[0], args[1:])
	case "set":
		if len(args) == 0 {
			return "More parameters required.\n"
		}
		if args[0] == "help" {
			return s.subHelp("set ")
		}
		return s.set(args[0], args[1:])
	case "params":
		if len(args) == 0 {
			return "More parameters required.\n"
		}
		return s.params(args[0])
	case "terminate":
		if len(args) == 0 {
			return "More parameters required.\n"
		}
		return s.terminate(args[0])
	case "debug":
		if !s.caps.Has("debug") || len(args) < 2 {
			return s.unknown(line)
		}
		return s.debug(args[0], args[1] == "on")
	case "call":
		if !s.caps.Has("call") {
			return s.unknown(line)
		}
		if len(args) < 2 {
			return "More parameters required.\n"
		}
		switch args[1] {
		case "info":
			return s.details(args[0])
		case "terminate":
			return s.terminate(args[0])
		case "debug":
			return s.debug(args[0], len(args) > 2 && args[2] == "on")
		}
		return s.unknown(strings.Join(args[1:], " "))
	case "active", "standby":
		if !s.caps.Has(cmd) {
			return s.unknown(line)
		}
		for _, c := range s.calls {
			c.foreign = cmd == "standby"
		}
		if cmd == "standby" {
			return "Ok, all calls set to 'foreign (standby)'\n"
		}
		return "Ok, all calls set to 'owned (active)'\n"
	case "kslist":
		var b strings.Builder
		b.WriteString("\nSubscribed-on keyspaces:\n")
		for i, k := range s.keyspaces {
			fmt.Fprintf(&b, "keyspace[%d] = %d\n", i, k)
		}
		return b.String()
	case "ksadd", "ksrm":
		if len(args) == 0 {
			return "More parameters required.\n"
		}
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return "Fail adding keyspace " + args[0] + " to redis notifications; no digits found\n"
		}
		if cmd == "ksadd" {
			s.keyspaces = append(s.keyspaces, n)
			return fmt.Sprintf("Success adding keyspace %d to redis notifications.\n", n)
		}
		for i, k := range s.keyspaces {
			if k == n {
				s.keyspaces = append(s.keyspaces[:i], s.keyspaces[i+1:]...)
				return fmt.Sprintf("Successfully unsubscribed from keyspace %d.\nSuccessfully removed all foreign calls for keyspace %d.\n", n, n)
			}
		}
		return fmt.Sprintf("Keyspace %d is not among redis notifications.\n", n)
	}
	return s.unknown(line)
}

func (s *Server) topCommands() []string {
	set := map[string]bool{}
	for _, c := range s.caps.Commands() {
		set[strings.Fields(c)[0]] = true
	}
	set["get"] = true
	var out []string
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func (s *Server) help(cmds []string) string {
	var b strings.Builder
	if s.at(13, 2) {
		for _, c := range cmds {
			fmt.Fprintf(&b, "%-49s : %s\n", c, "…")
		}
		return b.String()
	}
	b.WriteString("Available sub-commands at this level:\n")
	for _, c := range cmds {
		fmt.Fprintf(&b, "\t%s\n", c)
	}
	return b.String()
}

func (s *Server) subHelp(prefix string) string {
	var cmds []string
	for _, c := range s.caps.Commands() {
		if strings.HasPrefix(c, prefix) {
			cmds = append(cmds, strings.TrimPrefix(c, prefix))
		}
	}
	return s.help(cmds)
}

func (s *Server) list(what string, args []string) string {
	if what == "help" {
		return s.subHelp("list ")
	}
	if !s.caps.Has("list " + what) {
		return s.unknown(what)
	}
	switch what {
	case "numsessions":
		own, foreign := 0, 0
		for _, c := range s.calls {
			if c.foreign {
				foreign++
			} else {
				own++
			}
		}
		out := fmt.Sprintf("Current sessions own: %d\nCurrent sessions foreign: %d\nCurrent sessions total: %d\n", own, foreign, own+foreign)
		if s.at(9, 5) {
			out += fmt.Sprintf("Current transcoded media: %d\nCurrent sessions ipv4 only media: %d\nCurrent sessions ipv6 only media: 0\nCurrent sessions ip mixed  media: 0\n", own/5, own+foreign)
		}
		return out
	case "sessions":
		which := "all"
		if len(args) > 0 {
			which = args[0]
		}
		switch which {
		case "all", "own", "foreign":
			return s.sessions(which)
		}
		return s.details(which)
	case "totals":
		return s.totals()
	case "counters":
		n := len(s.calls)
		return fmt.Sprintf("\nCurrent per-second counters:\n\n Packets per second (userspace)                  :%d\n Bytes per second (userspace)                    :%d\n Errors per second (userspace)                   :0\n Packets per second (kernel)                     :%d\n Bytes per second (kernel)                       :%d\n Errors per second (kernel)                      :0\n",
			n*12, n*2064, n*88, n*15136)
	case "maxsessions":
		return "Maximum sessions configured on rtpengine: " + s.cur[what] + "\n"
	case "maxcpu":
		return "Maximum CPU usage configured on rtpengine: " + s.cur[what] + "\n"
	case "maxload":
		return "Maximum load average configured on rtpengine: " + s.cur[what] + "\n"
	case "maxbw":
		return "Maximum bandwidth configured on rtpengine: " + s.cur[what] + "\n"
	case "maxopenfiles":
		return "Maximum open-files configured on rtpengine: " + s.cur[what] + "; cat /proc/4242/limits\n"
	case "timeout", "silenttimeout", "offertimeout", "finaltimeout":
		return strings.ToUpper(strings.TrimSuffix(what, "timeout")+map[bool]string{true: "", false: "_"}[what == "timeout"]) + "TIMEOUT=" + s.cur[what] + "\n"
	case "loglevel":
		if s.at(9, 5) {
			var b strings.Builder
			for _, sub := range logSubsystems {
				if len(args) > 0 && args[0] != sub {
					continue
				}
				if len(args) > 0 {
					fmt.Fprintf(&b, "%d\n", s.level(sub))
				} else {
					fmt.Fprintf(&b, "%s = %d\n", sub, s.level(sub))
				}
			}
			return b.String()
		}
		return strconv.Itoa(s.loglevel) + "\n"
	case "loglevels":
		return "core - Everything that isn't part of another subsystem\nspandsp - Log messages generated by SpanDSP directly\nffmpeg - Log messages generated by ffmpeg directly\ntranscoding - Media and RTP transcoding\ncodec - Codec negotiation\nrtcp - RTCP processing\nice - ICE negotiation\ncrypto - Negotiation of SRTP, crypto suites, DTLS, SDES\nsrtp - SRTP encryption and decryption\ninternals - Noisy low-level internals\nhttp - HTTP, HTTPS, Websockets\ncontrol - Control protocols including SDP exchanges, CLI\n"
	case "interfaces":
		return s.interfaces()
	case "transcoders":
		return "PCMA/8000 <> opus/48000/2: 3 transcoders\n     150 packets/s\n     25800 bytes/s\n     24000 samples/s\n" +
			"PCMU/8000 <> G722/8000: 1 transcoders\n     50 packets/s\n     8600 bytes/s\n     8000 samples/s\n"
	case "jsonstats":
		return s.jsonStats()
	case "redisallowederrors", "redisdisabletime", "redisconnecttimeout", "rediscmdtimeout", "controltos", "deletedelay":
		return s.cur[what] + "\n"
	}
	return s.unknown(what)
}

// interfaces renders `list interfaces` in the layout of the emulated release
// (daemon/cli.c cli_incoming_list_interfaces).
type mockIface struct {
	name, addr         string
	min, max, used     int
	packets, bytes     int
	mos, loss, jit, rt float64
	sampled            bool
}

// ifaces are the interfaces of `list interfaces` and jsonstats.
func (s *Server) ifaces() []mockIface {
	n := len(s.calls)
	return []mockIface{
		{"pub", s.ip, 30000, 40000, n * 4, s.managed * 5100, s.managed * 877000, 42, 1, 3, 18, true},
		// no RTCP seen yet: averages print as nan
		{"priv", "10.10.0.2", 20000, 29999, n * 2, s.managed * 2500, s.managed * 430000, 0, 0, 0, 0, false},
	}
}

func (s *Server) interfaces() string {
	n := len(s.calls)
	var b strings.Builder
	for _, i := range s.ifaces() {
		total := i.max - i.min + 1
		fmt.Fprintf(&b, "Interface '%s' address '%s' (IPv4)\n", i.name, i.addr)
		fmt.Fprintf(&b, " Port range: %5d - %5d\n", i.min, i.max)
		fmt.Fprintf(&b, " Ports used: %5d / %5d (%5.1f%%)\n", i.used, total, float64(i.used)*100/float64(total))
		switch {
		case !s.at(11, 5):
			fmt.Fprintf(&b, " Last port used: %5d\n", i.min+2*n)
		case s.at(12, 5):
			avg := func(v float64) string {
				if !i.sampled {
					return "nan"
				}
				return fmt.Sprintf("%3.0f", v)
			}
			mos := "nan"
			if i.sampled {
				mos = fmt.Sprintf("%3.1f", i.mos/10)
			}
			fmt.Fprintf(&b, " Packets/bytes/errors:\n")
			fmt.Fprintf(&b, "  Ingress:   %10d / %10d / %10d\n", i.packets, i.bytes, i.packets/14000000)
			fmt.Fprintf(&b, "  Egress:    %10d / %10d / %10d\n", i.packets-i.packets/90, i.bytes-i.bytes/90, 0)
			fmt.Fprintf(&b, " Packets lost/duplicates: %10d / %10d\n", i.packets/2000, i.packets/90000)
			fmt.Fprintf(&b, " MOS:    avg %s, packet loss avg %s%%\n", mos, avg(i.loss))
			fmt.Fprintf(&b, " Jitter: avg %s (measured %s)\n", avg(i.jit), avg(i.jit-1))
			fmt.Fprintf(&b, " RTT:    e2e %s, dsct %s\n", avg(i.rt), avg(i.rt/3))
		}
		if s.at(13, 3) {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (s *Server) sessionIDs(which string) []string {
	var ids []string
	for id, c := range s.calls {
		if which == "own" && c.foreign || which == "foreign" && !c.foreign {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func yn(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (s *Server) sessions(which string) string {
	ids := s.sessionIDs(which)
	if len(ids) == 0 {
		if which == "all" {
			return "No sessions on this media relay.\n"
		}
		return "No " + which + " sessions on this media relay.\n"
	}
	var b strings.Builder
	for _, id := range ids {
		c := s.calls[id]
		if s.at(12, 5) {
			fmt.Fprintf(&b, "ID: %60s | del:%s | creat:%12d | prx:%s | redis:%2d | frgn:%s | rec:%s\n",
				id, yn(c.deleted), c.created.Unix(), c.proxy, c.keyspace, yn(c.foreign), "no")
		} else {
			fmt.Fprintf(&b, "callid: %60s | deletionmark:%4s | created:%12d | proxy:%s | redis_keyspace:%d | foreign:%s\n",
				id, yn(c.deleted), c.created.Unix(), c.proxy, c.keyspace, yn(c.foreign))
		}
	}
	return b.String()
}

func (s *Server) details(id string) string {
	c, ok := s.calls[id]
	if !ok {
		if s.at(13, 2) {
			return "\nCall ID not found (" + id + ").\n\n"
		}
		return "\nCall Id not found (" + id + ").\n\n"
	}
	var b strings.Builder
	if s.at(8, 5) {
		fmt.Fprintf(&b, "\ncallid: %s\ndeletionmark: %s\ncreated: %d\nproxy: %s\ntos: 184\nlast_signal: %d\nredis_keyspace: %d\n",
			id, yn(c.deleted), c.created.Unix(), c.proxy, c.created.Unix()+2, c.keyspace)
		if s.at(12, 5) {
			upd := int64(0)
			if c.keyspace > 0 {
				upd = time.Now().UnixMicro() - 300_000
			}
			fmt.Fprintf(&b, "last redis update: %d\n", upd)
		}
		fmt.Fprintf(&b, "foreign: %s\n", yn(c.foreign))
		if s.at(12, 5) {
			fmt.Fprintf(&b, "recording: no\n")
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "\ncallid: %60s | deletionmark:%4s | created:%12d  | proxy:%s | tos:184 | last_signal:%d | redis_keyspace:%d | foreign:%s\n\n",
			id, yn(c.deleted), c.created.Unix(), c.proxy, c.created.Unix()+2, c.keyspace, yn(c.foreign))
	}
	now := time.Now().Unix()
	dur := time.Since(c.created).Seconds()
	tags := []string{fmt.Sprintf("%x", s.rnd.Uint32()), fmt.Sprintf("%x", s.rnd.Uint32())}
	types, labels := []string{"FROM_TAG", "TO_TAG"}, []string{"caller", "callee"}
	for i, t := range tags {
		other := tags[1-i]
		// the callee answers a few seconds after the offer
		legDur := dur - float64(i)*min(dur, 4)
		switch {
		case !s.at(8, 5):
			fmt.Fprintf(&b, "--- Tag '%s' type: %s, callduration %.6f , in dialogue with '%s'\n", t, types[i], legDur, other)
		case !s.at(10, 5):
			fmt.Fprintf(&b, "--- Tag '%s', type: %s, label '%s', branch '', callduration %.6f, in dialogue with '%s'\n", t, types[i], labels[i], legDur, other)
		default:
			fmt.Fprintf(&b, "--- Tag '%s', type: %s, label '%s', branch '', callduration %.6f\n", t, types[i], labels[i], legDur)
		}
		switch {
		case s.at(12, 5):
			fmt.Fprintf(&b, "---     subscribed to media with monologue tag '%s' (index: 1)\n", other)
			fmt.Fprintf(&b, "---     subscription of media with monologue tag '%s' (index: 1)\n", other)
		case s.at(10, 5):
			fmt.Fprintf(&b, "---     subscribed to '%s'\n---     subscription of '%s'\n", other, other)
		}
		peer, port := c.peer, 16384+2*i
		if i == 0 {
			peer, port = strings.Split(c.proxy, ":")[0], 4000
		}
		// the caller side comes in from the proxy network on priv, the callee
		// side goes out on pub (see interfaces)
		local, lp := s.ip, 30000+s.rnd.Intn(5000)*2
		if i == 0 {
			local, lp = "10.10.0.2", 20000+s.rnd.Intn(4999)*2
		}
		// every fifth call has one-way audio: the callee sends nothing
		pk := int(legDur * 50)
		if i == 1 && c.oneWay {
			pk = 0
		}
		rtcp := pk / 250
		ssrc := s.rnd.Uint32()
		last, lastRTCP := now, now-int64(s.rnd.Intn(5))
		if pk == 0 {
			last, lastRTCP = 0, 0
		}
		if s.at(8, 5) {
			fmt.Fprintf(&b, "------ Media #1 (audio over RTP/AVP) using %s\n", c.codec)
		}
		for k, st := range []struct {
			port, pk, bytes int
			last            int64
			rtcp            string
		}{{lp, pk, pk * 172, last, ""}, {lp + 1, rtcp, rtcp * 100, lastRTCP, " (RTCP)"}} {
			switch {
			case s.at(12, 5):
				// userspace sees the first packets, the kernel module forwards the rest
				uts := min(st.last, c.created.Unix()+int64(i)*4+1)
				if st.rtcp != "" {
					uts = st.last
				}
				fmt.Fprintf(&b, "-------- Port %15s:%-5d <> %15s:%-5d%s, SSRC %x, %d p, %d b, 0 e, %d uts %d kts\n", local, st.port, peer, port+k, st.rtcp, ssrc, st.pk, st.bytes, uts, st.last)
			case s.at(8, 5):
				fmt.Fprintf(&b, "-------- Port %15s:%-5d <> %15s:%-5d%s, SSRC %x, %d p, %d b, 0 e, %d ts\n", local, st.port, peer, port+k, st.rtcp, ssrc, st.pk, st.bytes, st.last)
			default:
				fmt.Fprintf(&b, "------ Media #1, %15s:%-5d <> %15s:%-5d%s, %d p, %d b, 0 e, %d last_packet\n", local, st.port, peer, port+k, st.rtcp, st.pk, st.bytes, st.last)
			}
		}
	}
	return b.String()
}

func (s *Server) totals() string {
	n := len(s.calls)
	up := int(time.Since(s.started).Seconds())
	return fmt.Sprintf(`
Statistics over lifetime:

 Uptime of rtpengine                             :%d seconds
 Total managed sessions                          :%d
 Total rejected sessions                         :0
 Total timed-out sessions via TIMEOUT            :%d
 Total timed-out sessions via SILENT_TIMEOUT     :%d
 Total timed-out sessions via FINAL_TIMEOUT      :0
 Total timed-out sessions via OFFER_TIMEOUT      :%d
 Total regular terminated sessions               :%d
 Total forced terminated sessions                :%d
 Total relayed packets                           :%d
 Total relayed packet errors                     :%d
 Total number of 1 way streams                   :%d
 Average call duration                           :97.412

Statistics over currently running sessions:

 Owned sessions                                  :%d
 Total sessions                                  :%d
 Packets per second (userspace)                  :%d
 Bytes per second (userspace)                    :%d
 Errors per second (userspace)                   :0

Control statistics:

 Proxy      Offer      Answer     Delete     Ping       List       Query      Errors
 10.10.0.5  %-10d %-10d %-10d %-10d 0          0          %d
`, up, s.managed, s.managed/900+s.timeouts, s.managed/3000, s.managed/7000, s.managed-s.managed/50, s.managed/60+s.forced,
		s.managed*5100, s.managed/700, s.managed/400, n, n, n*100, n*17200,
		s.managed, s.managed-50, s.managed-20, s.managed*20, s.managed/1000)
}

var setNames = map[string]string{
	"maxsessions": "maxsessions", "maxcpu": "maxcpu", "maxload": "maxload", "maxbw": "maxbw",
	"maxopenfiles": "open_files", "timeout": "timeout", "silenttimeout": "silent-timeout",
	"offertimeout": "offer-timeout", "finaltimeout": "final-timeout", "deletedelay": "delete-delay",
	"controltos": "control-tos", "redisallowederrors": "redis-allowed-errors",
	"redisdisabletime": "redis-disable-time", "redisconnecttimeout": "redis-connect-timeout",
	"rediscmdtimeout": "redis-cmd-timeout",
}

func (s *Server) set(what string, args []string) string {
	if !s.caps.Has("set " + what) {
		return s.unknown(what)
	}
	if len(args) == 0 {
		return "More parameters required.\n"
	}
	val := args[0]
	switch what {
	case "loglevel":
		if s.at(9, 5) {
			// as cli.c from mr9.5: an optional subsystem, no range check, an
			// unknown subsystem changes nothing and still succeeds
			sub := ""
			if val[0] < '0' || val[0] > '9' {
				if len(args) < 2 {
					return "More parameters required.\n"
				}
				sub, val = val, args[1]
			}
			n, _ := strconv.Atoi(val)
			for _, name := range logSubsystems {
				if sub == "" || sub == name {
					s.levels[name] = n
				}
			}
			return fmt.Sprintf("Success setting loglevel to %d\n", n)
		}
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > 7 {
			return "Invalid log level '" + val + "', must be number between 1 and 7\n"
		}
		s.loglevel = n
		return fmt.Sprintf("Success setting loglevel to %d\n", n)
	case "redisdisable":
		return "Success setting redis-disable to " + val + "\n"
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return "Fail setting " + setNames[what] + " to " + val + "; no digits found\n"
	}
	switch what {
	case "maxsessions":
		if f < -1 {
			return fmt.Sprintf("Fail setting maxsessions to %d; either positive or -1 values allowed\n", int(f))
		}
		if f == -1 {
			s.cur[what] = "-1"
			return "Success setting maxsessions to -1; disable feature\n"
		}
	case "maxcpu":
		s.cur[what] = strconv.FormatFloat(f, 'f', 1, 64)
		return "Success setting maxcpu to " + s.cur[what] + "\n"
	case "maxload":
		s.cur[what] = strconv.FormatFloat(f, 'f', 2, 64)
		return "Success setting maxload to " + s.cur[what] + "\n"
	case "controltos":
		if f < 0 || f > 255 {
			return fmt.Sprintf("Invalid control-tos value %d, must be between 0 and 255\n", int(f))
		}
	}
	s.cur[what] = strconv.Itoa(int(f))
	return fmt.Sprintf("Success setting %s to %d\n", setNames[what], int(f))
}

// paramName renders a tunable the way `params` prints it in this release.
func (s *Server) paramName(k string) (string, func(string) string) {
	id := func(v string) string { return v }
	opt := map[string]string{
		"maxsessions": "max-sessions", "timeout": "timeout", "silenttimeout": "silent-timeout",
		"offertimeout": "offer-timeout", "finaltimeout": "final-timeout", "deletedelay": "delete-delay",
		"controltos": "control-tos", "redisallowederrors": "redis-allowed-errors",
		"redisdisabletime": "redis-disable-time", "redisconnecttimeout": "redis-connect-timeout",
		"rediscmdtimeout": "redis-cmd-timeout",
	}[k]
	if opt == "" {
		return "", id
	}
	if !s.at(9, 5) {
		return opt, id
	}
	name := strings.ReplaceAll(opt, "-", "_")
	if s.at(13, 0) {
		switch k {
		case "timeout", "silenttimeout", "offertimeout", "finaltimeout", "deletedelay", "redisdisabletime":
			return name + "_us", func(v string) string {
				n, _ := strconv.Atoi(v)
				return strconv.Itoa(n * 1_000_000)
			}
		}
	}
	return name, id
}

func (s *Server) params(which string) string {
	src := s.cur
	switch which {
	case "start":
		src = s.start
	case "current":
	case "diff":
		var b strings.Builder
		keys := make([]string, 0, len(s.cur))
		for k := range s.cur {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s.cur[k] != s.start[k] {
				name, _ := s.paramName(k)
				if name == "" {
					name = k
				}
				fmt.Fprintf(&b, "%s: %s => %s\n", name, s.start[k], s.cur[k])
			}
		}
		return b.String()
	case "revert":
		for k, v := range s.start {
			s.cur[k] = v
		}
		return ""
	default:
		return s.unknown(which)
	}
	var b strings.Builder
	if s.at(9, 5) {
		fmt.Fprintf(&b, "kernel_table = 0\n")
	} else {
		fmt.Fprintf(&b, "log-level = %d\ntable = 0\n", s.loglevel)
	}
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if name, conv := s.paramName(k); name != "" {
			fmt.Fprintf(&b, "%s = %s\n", name, conv(src[k]))
		}
	}
	tos := "default_tos"
	if !s.at(9, 5) {
		tos = "tos"
	}
	fmt.Fprintf(&b, "%s = 184\n", tos)
	if s.at(13, 2) {
		fmt.Fprintf(&b, "[max-cpu = %s]\n[max-load = %s]\n", src["maxcpu"], src["maxload"])
	}
	fmt.Fprintf(&b, "interface[0] = pub\\%s\n", s.ip)
	for i, k := range s.keyspaces {
		fmt.Fprintf(&b, "keyspace[%d] = %d\n", i, k)
	}
	return b.String()
}

func (s *Server) terminate(what string) string {
	switch what {
	case "all", "own", "foreign":
		for _, id := range s.sessionIDs(what) {
			s.logf(time.Now(), "INFO", id, "Call terminated by operator")
			delete(s.calls, id)
			s.forced++
		}
		if what == "all" {
			return "All calls terminated by operator.\n"
		}
		return "All " + what + " calls terminated by operator.\n"
	}
	if _, ok := s.calls[what]; !ok {
		return "\nCall Id not found (" + what + ").\n\n"
	}
	s.logf(time.Now(), "INFO", what, "Call terminated by operator")
	delete(s.calls, what)
	s.forced++
	return "\nCall Id (" + what + ") successfully terminated by operator.\n\n"
}

func (s *Server) debug(id string, on bool) string {
	// rtpengine only flags the call: the state is not shown anywhere
	if _, ok := s.calls[id]; !ok {
		return "\nCall Id not found (" + id + ").\n\n"
	}
	state := "Disabled"
	if on {
		state = "Enabled"
		s.logf(time.Now(), "DEBUG", id, "Debug logging enabled for this call")
	}
	return state + " debugging for call '" + id + "'\n"
}

// CallIDs returns current Call-IDs, sorted (for tests).
func (s *Server) CallIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionIDs("all")
}

// Get returns a runtime tunable (for tests).
func (s *Server) Get(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur[name]
}

// logSubsystems are the log subsystems of mr9.5+, in rtpengine's order.
var logSubsystems = []string{"core", "spandsp", "ffmpeg", "transcoding", "codec", "rtcp", "ice", "crypto", "srtp", "internals", "http", "control"}

// level is the log level of a subsystem (mr9.5+), 6 until set.
func (s *Server) level(sub string) int {
	if n, ok := s.levels[sub]; ok {
		return n
	}
	return s.loglevel
}

// LogLevel returns the level of a subsystem, "" for the one level before
// mr9.5 (for tests).
func (s *Server) LogLevel(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sub == "" {
		return s.loglevel
	}
	return s.level(sub)
}
