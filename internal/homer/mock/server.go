// Package mock is a fake homer-app (/api/v3) with generated SIP traffic,
// used for tests and for trying homer-tui without a real Homer.
package mock

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Record is one stored HEP message.
type Record struct {
	ID    int
	Key   string // 1_call, 1_registration, 100_default, 5_default
	Sid   string
	Time  time.Time
	Proto map[string]any // protocol_header
	Data  map[string]any // data_header
	Raw   string
	Node  string
}

// Server is the mock homer-app.
type Server struct {
	User, Password string
	Token          string // valid Auth-Token

	mu       sync.Mutex
	records  []Record
	nextID   int
	jwts     map[string]bool
	store    map[string][]map[string]any // list path -> items
	dashes   map[string]map[string]any
	imported int
}

// New creates a server with traffic generated for the last 3 hours.
func New(seed int64) *Server {
	s := &Server{
		User: "admin", Password: "sipcapture", Token: "mock-auth-token",
		jwts: map[string]bool{}, store: map[string][]map[string]any{}, dashes: map[string]map[string]any{},
	}
	s.generate(rand.New(rand.NewSource(seed)), time.Now())
	// homer-app keeps the rows in the order they came and searches without
	// ORDER BY, so a search that hits its limit gets the oldest rows
	sort.SliceStable(s.records, func(i, j int) bool { return s.records[i].Time.Before(s.records[j].Time) })
	s.seedSettings()
	return s
}

var (
	hostUA      = []string{"10.10.0.11", "10.10.0.12", "10.10.0.13", "10.10.0.14"}
	hostSBC     = "192.168.10.1"
	hostPBX     = "192.168.10.20"
	hostCarrier = "203.0.113.10"
	agents      = []string{"Yealink SIP-T46U", "Linphone/5.2", "MicroSIP/3.21", "Grandstream GXP2170"}
)

func (s *Server) add(key, sid string, t time.Time, src, dst string, sport, dport int, data map[string]any, raw string) {
	s.nextID++
	s.records = append(s.records, Record{
		ID: s.nextID, Key: key, Sid: sid, Time: t, Node: "localnode", Raw: raw, Data: data,
		Proto: map[string]any{
			"srcIp": src, "dstIp": dst, "srcPort": sport, "dstPort": dport, "protocol": 17,
			"captureId": "2001", "capturePass": "myHep", "payloadType": payloadType(key),
			"timeSeconds": t.Unix(), "timeUseconds": t.Nanosecond() / 1000, "protocolFamily": 2,
			"correlation_id": "",
		},
	})
}

func payloadType(key string) int {
	switch {
	case strings.HasPrefix(key, "1_"):
		return 1
	case strings.HasPrefix(key, "5_"):
		return 5
	case strings.HasPrefix(key, "100_"):
		return 100
	}
	return 0
}

// sipMsg builds a SIP message; media is the address its SDP offers or
// answers, the endpoint's: a proxy passes the body on as it is. No SDP
// when media is empty.
func sipMsg(first, callid, from, to, cseq, ua string, src, dst string, media string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\r\n", first)
	fmt.Fprintf(&b, "Via: SIP/2.0/UDP %s:5060;branch=z9hG4bK%x\r\n", src, rand.Int63())
	fmt.Fprintf(&b, "From: <sip:%s@example.com>;tag=%x\r\n", from, len(from)*7919)
	fmt.Fprintf(&b, "To: <sip:%s@example.com>\r\n", to)
	fmt.Fprintf(&b, "Call-ID: %s\r\n", callid)
	fmt.Fprintf(&b, "CSeq: %s\r\n", cseq)
	fmt.Fprintf(&b, "Contact: <sip:%s@%s:5060>\r\n", from, src)
	fmt.Fprintf(&b, "User-Agent: %s\r\n", ua)
	if media != "" {
		body := fmt.Sprintf("v=0\r\no=- 1 1 IN IP4 %s\r\ns=-\r\nc=IN IP4 %s\r\nt=0 0\r\nm=audio 40000 RTP/AVP 8 0 101\r\na=rtpmap:8 PCMA/8000\r\na=sendrecv\r\n", media, media)
		fmt.Fprintf(&b, "Content-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	} else {
		b.WriteString("Content-Length: 0\r\n\r\n")
	}
	return b.String()
}

func (s *Server) generate(r *rand.Rand, now time.Time) {
	start := now.Add(-3 * time.Hour)
	// calls through SBC -> PBX
	for i := 0; i < 60; i++ {
		t := start.Add(time.Duration(r.Int63n(int64(3*time.Hour - 10*time.Minute))))
		callid := fmt.Sprintf("%08x-%04x@%s", r.Uint32(), r.Intn(65535), hostSBC)
		uaIP := hostUA[r.Intn(len(hostUA))]
		ua := agents[r.Intn(len(agents))]
		from := strconv.Itoa(100 + r.Intn(20))
		to := strconv.Itoa(200 + r.Intn(20))
		outcome := r.Intn(10) // 0-6 answered, 7 busy, 8 not found, 9 cancel
		step := func(d time.Duration) { t = t.Add(d + time.Duration(r.Intn(15))*time.Millisecond) }
		hdr := func(method string) map[string]any {
			return map[string]any{"method": method, "callid": callid, "from_user": from, "to_user": to,
				"ruri_user": to, "ruri_domain": "example.com", "user_agent": ua, "cseq": "1 INVITE", "from_tag": "a1", "to_tag": ""}
		}
		send := func(src, dst, method, first string, sdp bool, cseq string) {
			media := ""
			switch {
			case sdp && method == "INVITE":
				media = uaIP // the caller offers, the SBC passes it on
			case sdp:
				media = hostPBX // the PBX answers
			}
			raw := sipMsg(first, callid, from, to, cseq, ua, src, dst, media)
			h := hdr(method)
			h["cseq"] = cseq
			s.add("1_call", callid, t, src, dst, 5060, 5060, h, raw)
		}
		inv := "INVITE sip:" + to + "@example.com SIP/2.0"
		send(uaIP, hostSBC, "INVITE", inv, true, "1 INVITE")
		step(3 * time.Millisecond)
		send(hostSBC, uaIP, "100", "SIP/2.0 100 Trying", false, "1 INVITE")
		step(2 * time.Millisecond)
		send(hostSBC, hostPBX, "INVITE", inv, true, "1 INVITE")
		step(4 * time.Millisecond)
		send(hostPBX, hostSBC, "100", "SIP/2.0 100 Trying", false, "1 INVITE")
		switch {
		case outcome == 7 || outcome == 8:
			code := "486 Busy Here"
			if outcome == 8 {
				code = "404 Not Found"
			}
			step(300 * time.Millisecond)
			send(hostPBX, hostSBC, code[:3], "SIP/2.0 "+code, false, "1 INVITE")
			step(2 * time.Millisecond)
			send(hostSBC, hostPBX, "ACK", "ACK sip:"+to+"@example.com SIP/2.0", false, "1 ACK")
			step(2 * time.Millisecond)
			send(hostSBC, uaIP, code[:3], "SIP/2.0 "+code, false, "1 INVITE")
			step(3 * time.Millisecond)
			send(uaIP, hostSBC, "ACK", "ACK sip:"+to+"@example.com SIP/2.0", false, "1 ACK")
		case outcome == 9:
			step(800 * time.Millisecond)
			send(hostPBX, hostSBC, "180", "SIP/2.0 180 Ringing", false, "1 INVITE")
			send(hostSBC, uaIP, "180", "SIP/2.0 180 Ringing", false, "1 INVITE")
			step(4 * time.Second)
			send(uaIP, hostSBC, "CANCEL", "CANCEL sip:"+to+"@example.com SIP/2.0", false, "1 CANCEL")
			send(hostSBC, uaIP, "200", "SIP/2.0 200 OK", false, "1 CANCEL")
			send(hostSBC, hostPBX, "CANCEL", "CANCEL sip:"+to+"@example.com SIP/2.0", false, "1 CANCEL")
			send(hostPBX, hostSBC, "487", "SIP/2.0 487 Request Terminated", false, "1 INVITE")
			send(hostSBC, uaIP, "487", "SIP/2.0 487 Request Terminated", false, "1 INVITE")
		default:
			step(600 * time.Millisecond)
			send(hostPBX, hostSBC, "180", "SIP/2.0 180 Ringing", false, "1 INVITE")
			step(2 * time.Millisecond)
			send(hostSBC, uaIP, "180", "SIP/2.0 180 Ringing", false, "1 INVITE")
			step(time.Duration(2+r.Intn(5)) * time.Second)
			send(hostPBX, hostSBC, "200", "SIP/2.0 200 OK", true, "1 INVITE")
			step(2 * time.Millisecond)
			send(hostSBC, uaIP, "200", "SIP/2.0 200 OK", true, "1 INVITE")
			step(20 * time.Millisecond)
			send(uaIP, hostSBC, "ACK", "ACK sip:"+to+"@example.com SIP/2.0", false, "1 ACK")
			step(2 * time.Millisecond)
			send(hostSBC, hostPBX, "ACK", "ACK sip:"+to+"@example.com SIP/2.0", false, "1 ACK")
			talk := time.Duration(10+r.Intn(170)) * time.Second
			// RTCP every 5s both directions
			for rt := t.Add(5 * time.Second); rt.Before(t.Add(talk)); rt = rt.Add(5 * time.Second) {
				for dir := 0; dir < 2; dir++ {
					src, dst := uaIP, hostPBX
					if dir == 1 {
						src, dst = dst, src
					}
					lost := 0
					if r.Intn(8) == 0 {
						lost = r.Intn(12)
					}
					// LSR is the sender report 5 s ago, DLSR leaves an RTT of 20-80 ms
					lsr := ntpShort(rt.Add(-5 * time.Second))
					dlsr := ntpShort(rt) - lsr - uint32((20+r.Intn(60))*65536/1000)
					rep := fmt.Sprintf(`{"type":200,"ssrc":%d,"report_count":1,"sender_information":{"packets":%d,"octets":%d},"report_blocks":[{"source_ssrc":%d,"fraction_lost":%d,"packets_lost":%d,"ia_jitter":%d,"highest_seq_no":%d,"lsr":%d,"dlsr":%d}]}`,
						1000+dir, int(rt.Sub(t).Seconds()*50), int(rt.Sub(t).Seconds()*8000), 1001-dir, lost*2, lost, r.Intn(30), int(rt.Sub(t).Seconds()*50), lsr, dlsr)
					s.add("5_default", callid, rt, src, dst, 40001, 40001, map[string]any{"callid": callid}, rep)
				}
			}
			t = t.Add(talk)
			send(uaIP, hostSBC, "BYE", "BYE sip:"+to+"@example.com SIP/2.0", false, "2 BYE")
			step(2 * time.Millisecond)
			send(hostSBC, hostPBX, "BYE", "BYE sip:"+to+"@example.com SIP/2.0", false, "2 BYE")
			step(10 * time.Millisecond)
			send(hostPBX, hostSBC, "200", "SIP/2.0 200 OK", false, "2 BYE")
			step(2 * time.Millisecond)
			send(hostSBC, uaIP, "200", "SIP/2.0 200 OK", false, "2 BYE")
		}
		s.add("100_default", callid, t, hostPBX, hostSBC, 0, 0, map[string]any{"callid": callid},
			fmt.Sprintf("pbx: call %s -> %s finished, cause=%d", from, to, 16+outcome))
	}
	// logs of no call: no session id
	for i := 0; i < 8; i++ {
		t := start.Add(time.Duration(i)*20*time.Minute + 7*time.Minute + 13*time.Millisecond)
		s.add("100_default", "", t, hostPBX, hostSBC, 0, 0, map[string]any{}, fmt.Sprintf("pbx: dialplan reloaded, %d extensions", 40+i))
	}
	s.forwarded(r, start)
	s.talking(now)
	// registrations
	for i := 0; i < 40; i++ {
		t := start.Add(time.Duration(r.Int63n(int64(3 * time.Hour))))
		callid := fmt.Sprintf("reg-%08x@%s", r.Uint32(), hostUA[i%len(hostUA)])
		user := strconv.Itoa(100 + i%20)
		ua := agents[i%len(agents)]
		src := hostUA[i%len(hostUA)]
		hdr := func(m string) map[string]any {
			return map[string]any{"method": m, "callid": callid, "from_user": user, "to_user": user, "ruri_user": "",
				"user_agent": ua, "cseq": "1 REGISTER"}
		}
		first := "REGISTER sip:example.com SIP/2.0"
		s.add("1_registration", callid, t, src, hostSBC, 5060, 5060, hdr("REGISTER"), sipMsg(first, callid, user, user, "1 REGISTER", ua, src, hostSBC, ""))
		t = t.Add(4 * time.Millisecond)
		s.add("1_registration", callid, t, hostSBC, src, 5060, 5060, hdr("401"), sipMsg("SIP/2.0 401 Unauthorized", callid, user, user, "1 REGISTER", ua, hostSBC, src, ""))
		t = t.Add(30 * time.Millisecond)
		s.add("1_registration", callid, t, src, hostSBC, 5060, 5060, hdr("REGISTER"), sipMsg(first, callid, user, user, "2 REGISTER", ua, src, hostSBC, ""))
		t = t.Add(5 * time.Millisecond)
		code := "SIP/2.0 200 OK"
		m := "200"
		if i%9 == 0 {
			code, m = "SIP/2.0 403 Forbidden", "403"
		}
		s.add("1_registration", callid, t, hostSBC, src, 5060, 5060, hdr(m), sipMsg(code, callid, user, user, "2 REGISTER", ua, hostSBC, src, ""))
	}
	sort.Slice(s.records, func(i, j int) bool { return s.records[i].Time.Before(s.records[j].Time) })
}

func (s *Server) seedSettings() {
	s.store["/mapping/protocol"] = mappings()
	s.store["/alias"] = []map[string]any{
		{"id": 1, "guid": "a1b2c3d4-0000-4000-8000-000000000001", "alias": "SBC", "ip": hostSBC, "port": 0, "mask": 32, "captureID": "0", "status": true},
		{"id": 2, "guid": "a1b2c3d4-0000-4000-8000-000000000002", "alias": "PBX", "ip": hostPBX, "port": 0, "mask": 32, "captureID": "0", "status": true},
	}
	s.store["/users"] = []map[string]any{
		{"guid": "u-admin", "username": "admin", "firstname": "Homer", "lastname": "Admin", "email": "admin@example.com", "usergroup": "admin", "department": "Develop", "partid": 10},
		{"guid": "u-support", "username": "support", "firstname": "Homer", "lastname": "Support", "email": "support@example.com", "usergroup": "user", "department": "Support", "partid": 10},
	}
	s.store["/token/auth"] = []map[string]any{
		{"guid": "t-1", "name": "grafana", "active": true, "expire_date": time.Now().AddDate(1, 0, 0).Format(time.RFC3339), "usage_calls": 42, "limit_calls": 1000, "lastusage_date": time.Now().Format(time.RFC3339)},
	}
	s.store["/advanced"] = []map[string]any{
		{"guid": "g-1", "partid": 10, "category": "export", "param": "transaction", "data": map[string]any{"openwindow": false}},
		{"guid": "g-2", "partid": 10, "category": "system", "param": "prometheus", "data": map[string]any{"host": "http://prometheus:9090/api/v1/"}},
	}
	s.store["/hepsub/protocol"] = []map[string]any{
		{"guid": "h-1", "hepid": 1, "hep_alias": "SIP", "profile": "call", "version": 1, "mapping": map[string]any{"lookup_id": "", "source_field": "data_header.callid"}},
	}
	s.store["/user/settings"] = []map[string]any{
		{"guid": "s-1", "username": "admin", "category": "search", "param": "last", "partid": 10, "data": map[string]any{}},
	}
	s.store["/agent/subscribe"] = []map[string]any{
		{"uuid": "ag-1", "gid": 10, "host": "10.10.0.50", "port": 8080, "protocol": "http", "path": "/api/search", "node": "cgrates", "type": "cdr", "active": 1, "expire_date": time.Now().Add(time.Hour).Format(time.RFC3339)},
	}
	s.dashes["home"] = map[string]any{"id": "home", "name": "Home", "param": "home", "type": 1, "shared": false, "weight": 10, "widgets": []any{}}
	s.dashes["smartsearch"] = map[string]any{"id": "smartsearch", "name": "Search", "param": "smartsearch", "type": 1, "shared": false, "weight": 20, "widgets": []any{}}
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v3")
	if path == "/auth" && r.Method == http.MethodPost {
		s.login(w, r)
		return
	}
	if !s.authorized(r) {
		if r.Header.Get("Auth-Token") != "" { // as homer-app answers an unknown token
			writeJSON(w, 401, map[string]any{"message": "Login required"})
			return
		}
		writeJSON(w, 401, map[string]any{"statuscode": 401, "error": "Unauthorized", "message": "missing or malformed jwt"})
		return
	}
	if path == "/users/profile" && r.Header.Get("Auth-Token") != "" { // a token has no user behind it
		writeJSON(w, 400, map[string]any{"data": map[string]any{}, "message": "failed to get user profile"})
		return
	}
	if strings.HasPrefix(path, "/import/data/pcap") {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.importPCAP(w, r)
		return
	}
	if r.Header.Get("Auth-Token") != "" && r.Method != http.MethodGet && adminOnly(path) {
		writeJSON(w, 403, map[string]any{"message": "This API requires admin access. The AuthToken in use!"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case path == "/users/profile":
		writeJSON(w, 200, map[string]any{"count": 1, "data": map[string]any{"guid": "u-admin", "username": s.User, "displayname": "Homer Admin", "group": "admin", "admin": true}})
	case path == "/database/node/list":
		writeJSON(w, 200, map[string]any{"count": 1, "data": []any{map[string]any{"name": "localnode", "value": "localnode"}}})
	case path == "/modules/status":
		writeJSON(w, 200, map[string]any{"message": "Modules status", "data": map[string]any{"loki": map[string]any{"enable": false}}})
	case path == "/auth/type/list":
		writeJSON(w, 200, map[string]any{"count": 1, "data": map[string]any{"internal": map[string]any{"enable": true, "name": "Internal", "type": "internal"}}})
	case strings.HasPrefix(path, "/mapping/protocol/reset"):
		writeJSON(w, 200, map[string]any{"message": "successfully reset mapping", "data": ""})
	case path == "/search/call/data":
		s.search(w, body)
	case path == "/call/transaction":
		s.transaction(w, body)
	case path == "/call/report/qos":
		s.qos(w, body)
	case path == "/call/report/log":
		s.logs(w, body)
	case path == "/export/call/messages/pcap":
		s.exportPCAP(w, body)
	case path == "/export/call/messages/text":
		s.exportText(w, body)
	case path == "/dashboard/info":
		var list []map[string]any
		for _, d := range s.dashes {
			list = append(list, d)
		}
		sort.Slice(list, func(i, j int) bool { return fmt.Sprint(list[i]["id"]) < fmt.Sprint(list[j]["id"]) })
		writeJSON(w, 200, map[string]any{"auth": "ok", "status": "ok", "total": len(list), "data": list})
	case strings.HasPrefix(path, "/dashboard/store/"):
		s.dashboard(w, r.Method, strings.TrimPrefix(path, "/dashboard/store/"), body)
	default:
		s.crud(w, r.Method, path, body)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Username != s.User || req.Password != s.Password {
		writeJSON(w, 401, map[string]any{"statuscode": 401, "error": "Unauthorized", "message": "User couldn't be found"})
		return
	}
	s.mu.Lock()
	tok := fmt.Sprintf("mock-jwt-%d", len(s.jwts)+1)
	s.jwts[tok] = true
	s.mu.Unlock()
	writeJSON(w, 201, map[string]any{"token": tok, "scope": "u-admin", "user": map[string]any{"admin": true}})
}

func (s *Server) authorized(r *http.Request) bool {
	if r.Header.Get("Auth-Token") == s.Token {
		return true
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jwts[tok]
}

// ExpireSessions drops all JWTs (to test re-login).
func (s *Server) ExpireSessions() {
	s.mu.Lock()
	s.jwts = map[string]bool{}
	s.mu.Unlock()
}

type searchReq struct {
	Param struct {
		Search   map[string]json.RawMessage `json:"search"`
		Location struct {
			Node []string `json:"node"`
		} `json:"location"`
		OrLogic bool `json:"orlogic"`
		Limit   int  `json:"limit"`
	} `json:"param"`
	Timestamp struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	} `json:"timestamp"`
}

type cond struct {
	Name, Value, Type string
}

func (s *Server) search(w http.ResponseWriter, body []byte) {
	var req searchReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, map[string]any{"message": "request format incorrect"})
		return
	}
	from, to := time.UnixMilli(req.Timestamp.From), time.UnixMilli(req.Timestamp.To)
	var out []map[string]any
	for key, raw := range req.Param.Search {
		var conds []cond
		_ = json.Unmarshal(raw, &conds)
		limit := 200
		var filters []cond
		for _, c := range conds {
			switch c.Name {
			case "limit":
				limit, _ = strconv.Atoi(c.Value)
			case "smartinput":
				filters = append(filters, parseSmart(c.Value)...)
			default:
				filters = append(filters, c)
			}
		}
		for _, rec := range s.records {
			if rec.Key != key || rec.Time.Before(from) || rec.Time.After(to) || !contains(req.Param.Location.Node, rec.Node) {
				continue
			}
			if !matchAll(rec, filters, req.Param.OrLogic) {
				continue
			}
			out = append(out, flatten(rec))
			if len(out) >= limit {
				break
			}
		}
	}
	keys := map[string]bool{}
	var keyList []string
	for _, o := range out {
		for k := range o {
			if !keys[k] {
				keys[k] = true
				keyList = append(keyList, k)
			}
		}
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, 201, map[string]any{"total": len(out), "data": out, "keys": keyList})
}

func parseSmart(expr string) []cond {
	var out []cond
	for _, part := range strings.Split(expr, " AND ") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out = append(out, cond{Name: strings.TrimSpace(k), Value: strings.Trim(strings.TrimSpace(v), "'\""), Type: "string"})
	}
	return out
}

func fieldValue(rec Record, name string) string {
	switch name {
	case "sid":
		return rec.Sid
	case "raw":
		return rec.Raw
	case "id":
		return strconv.Itoa(rec.ID)
	}
	if k, f, ok := strings.Cut(name, "."); ok {
		src := rec.Data
		if k == "protocol_header" {
			src = rec.Proto
		}
		return fmt.Sprint(src[f])
	}
	return ""
}

func matchAll(rec Record, conds []cond, or bool) bool {
	if len(conds) == 0 {
		return true
	}
	for _, c := range conds {
		ok := matchOne(fieldValue(rec, c.Name), c.Value)
		if or && ok {
			return true
		}
		if !or && !ok {
			return false
		}
	}
	return !or
}

func matchOne(have, want string) bool {
	neg := strings.HasPrefix(want, "!=")
	want = strings.TrimPrefix(want, "!=")
	hit := false
	for _, w := range strings.Split(want, ";") {
		if strings.Contains(w, "%") {
			pat := strings.ReplaceAll(w, "%", "")
			switch {
			case strings.HasPrefix(w, "%") && strings.HasSuffix(w, "%"):
				hit = hit || strings.Contains(have, pat)
			case strings.HasPrefix(w, "%"):
				hit = hit || strings.HasSuffix(have, pat)
			default:
				hit = hit || strings.HasPrefix(have, pat)
			}
		} else if have == w {
			hit = true
		}
	}
	return hit != neg
}

func flatten(rec Record) map[string]any {
	m := map[string]any{}
	for k, v := range rec.Data {
		m[k] = v
	}
	for k, v := range rec.Proto {
		m[k] = v
	}
	m["id"] = rec.ID
	m["sid"] = rec.Sid
	m["dbnode"] = rec.Node
	m["node"] = rec.Node
	m["table"] = "hep_proto_" + rec.Key
	m["create_date"] = rec.Time.UnixMilli()
	m["aliasSrc"] = alias(rec.Proto["srcIp"], rec.Proto["srcPort"])
	m["aliasDst"] = alias(rec.Proto["dstIp"], rec.Proto["dstPort"])
	if rec.Key == "100_default" {
		m["raw"] = rec.Raw
	}
	return m
}

// ntpShort is the middle 32 bits of the NTP time of t, as RTCP has LSR.
func ntpShort(t time.Time) uint32 {
	sec := uint64(t.Unix() + 2208988800)
	frac := uint64(t.Nanosecond()) << 32 / 1e9
	return uint32(sec<<16 | frac>>16)
}

// flowMethod is the method of an arrow: of SIP its own, LOG and RTCP for
// the others, as homer-app names them.
func flowMethod(rec Record) any {
	switch rec.Key {
	case "100_default":
		return "LOG"
	case "5_default":
		return "RTCP"
	}
	return rec.Data["method"]
}

// talking adds a call answered half a minute ago and not hung up yet, to
// follow.
func (s *Server) talking(now time.Time) {
	callid := "live-0001@" + hostSBC
	t := now.Add(-30 * time.Second)
	send := func(src, dst, method, first, cseq, media string) {
		h := map[string]any{"method": method, "callid": callid, "from_user": "120", "to_user": "220", "ruri_user": "220",
			"ruri_domain": "example.com", "user_agent": agents[0], "cseq": cseq}
		s.add("1_call", callid, t, src, dst, 5060, 5060, h, sipMsg(first, callid, "120", "220", cseq, agents[0], src, dst, media))
		t = t.Add(5 * time.Millisecond)
	}
	ua := hostUA[0]
	send(ua, hostSBC, "INVITE", "INVITE sip:220@example.com SIP/2.0", "1 INVITE", ua)
	send(hostSBC, hostPBX, "INVITE", "INVITE sip:220@example.com SIP/2.0", "1 INVITE", ua)
	send(hostPBX, hostSBC, "200", "SIP/2.0 200 OK", "1 INVITE", hostPBX)
	send(hostSBC, ua, "200", "SIP/2.0 200 OK", "1 INVITE", hostPBX)
	send(ua, hostSBC, "ACK", "ACK sip:220@example.com SIP/2.0", "1 ACK", "")
	send(hostSBC, hostPBX, "ACK", "ACK sip:220@example.com SIP/2.0", "1 ACK", "")
}

// forwarded adds calls the PBX forwards out again: the first leg comes
// in through the SBC, the second goes from the PBX back to the SBC and on
// to a carrier, with its own Call-ID and the first one as correlation_id.
func (s *Server) forwarded(r *rand.Rand, start time.Time) {
	for i := 0; i < 8; i++ {
		t := start.Add(time.Duration(r.Int63n(int64(3*time.Hour - 10*time.Minute))))
		leg1 := fmt.Sprintf("fwd-%08x@%s", r.Uint32(), hostSBC)
		leg2 := fmt.Sprintf("fwd-%08x@%s", r.Uint32(), hostPBX)
		uaIP := hostUA[r.Intn(len(hostUA))]
		ua := agents[r.Intn(len(agents))]
		from, to, ext := strconv.Itoa(100+r.Intn(20)), strconv.Itoa(200+r.Intn(20)), fmt.Sprintf("7701%07d", r.Intn(10000000))
		send := func(callid, src, dst, method, first, cseq string, sdp bool) {
			callee := to
			if callid == leg2 {
				callee = ext
			}
			h := map[string]any{"method": method, "callid": callid, "from_user": from, "to_user": callee, "ruri_user": callee,
				"ruri_domain": "example.com", "user_agent": ua, "cseq": cseq, "correlation_id": leg1}
			media := ""
			if sdp { // the PBX offers and answers for itself, the SBC passes on
				switch {
				case callid == leg1 && method == "INVITE":
					media = uaIP
				case callid == leg2 && method != "INVITE":
					media = hostCarrier
				default:
					media = hostPBX
				}
			}
			s.add("1_call", callid, t, src, dst, 5060, 5060, h, sipMsg(first, callid, from, callee, cseq, ua, src, dst, media))
			t = t.Add(time.Duration(2+r.Intn(8)) * time.Millisecond)
		}
		inv := func(user string) string { return "INVITE sip:" + user + "@example.com SIP/2.0" }
		send(leg1, uaIP, hostSBC, "INVITE", inv(to), "1 INVITE", true)
		send(leg1, hostSBC, uaIP, "100", "SIP/2.0 100 Trying", "1 INVITE", false)
		send(leg1, hostSBC, hostPBX, "INVITE", inv(to), "1 INVITE", true)
		send(leg1, hostPBX, hostSBC, "100", "SIP/2.0 100 Trying", "1 INVITE", false)
		send(leg2, hostPBX, hostSBC, "INVITE", inv(ext), "1 INVITE", true)
		send(leg2, hostSBC, hostPBX, "100", "SIP/2.0 100 Trying", "1 INVITE", false)
		send(leg2, hostSBC, hostCarrier, "INVITE", inv(ext), "1 INVITE", true)
		send(leg2, hostCarrier, hostSBC, "100", "SIP/2.0 100 Trying", "1 INVITE", false)
		t = t.Add(time.Second)
		send(leg2, hostCarrier, hostSBC, "180", "SIP/2.0 180 Ringing", "1 INVITE", false)
		send(leg2, hostSBC, hostPBX, "180", "SIP/2.0 180 Ringing", "1 INVITE", false)
		send(leg1, hostPBX, hostSBC, "180", "SIP/2.0 180 Ringing", "1 INVITE", false)
		send(leg1, hostSBC, uaIP, "180", "SIP/2.0 180 Ringing", "1 INVITE", false)
		t = t.Add(time.Duration(2+r.Intn(4)) * time.Second)
		send(leg2, hostCarrier, hostSBC, "200", "SIP/2.0 200 OK", "1 INVITE", true)
		send(leg2, hostSBC, hostPBX, "200", "SIP/2.0 200 OK", "1 INVITE", true)
		send(leg1, hostPBX, hostSBC, "200", "SIP/2.0 200 OK", "1 INVITE", true)
		send(leg1, hostSBC, uaIP, "200", "SIP/2.0 200 OK", "1 INVITE", true)
		send(leg1, uaIP, hostSBC, "ACK", "ACK sip:"+to+"@example.com SIP/2.0", "1 ACK", false)
		send(leg1, hostSBC, hostPBX, "ACK", "ACK sip:"+to+"@example.com SIP/2.0", "1 ACK", false)
		send(leg2, hostPBX, hostSBC, "ACK", "ACK sip:"+ext+"@example.com SIP/2.0", "1 ACK", false)
		send(leg2, hostSBC, hostCarrier, "ACK", "ACK sip:"+ext+"@example.com SIP/2.0", "1 ACK", false)
		t = t.Add(time.Duration(10+r.Intn(60)) * time.Second)
		send(leg1, uaIP, hostSBC, "BYE", "BYE sip:"+to+"@example.com SIP/2.0", "2 BYE", false)
		send(leg1, hostSBC, hostPBX, "BYE", "BYE sip:"+to+"@example.com SIP/2.0", "2 BYE", false)
		send(leg1, hostPBX, hostSBC, "200", "SIP/2.0 200 OK", "2 BYE", false)
		send(leg1, hostSBC, uaIP, "200", "SIP/2.0 200 OK", "2 BYE", false)
		send(leg2, hostPBX, hostSBC, "BYE", "BYE sip:"+ext+"@example.com SIP/2.0", "2 BYE", false)
		send(leg2, hostSBC, hostCarrier, "BYE", "BYE sip:"+ext+"@example.com SIP/2.0", "2 BYE", false)
		send(leg2, hostCarrier, hostSBC, "200", "SIP/2.0 200 OK", "2 BYE", false)
		send(leg2, hostSBC, hostPBX, "200", "SIP/2.0 200 OK", "2 BYE", false)
	}
}

func alias(ip, port any) string {
	switch ip {
	case hostSBC:
		return "SBC"
	case hostPBX:
		return "PBX"
	}
	return fmt.Sprintf("%v:%v", ip, port)
}

func (s *Server) callRecords(body []byte, keys ...string) ([]Record, error) {
	var req searchReq
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	sids := map[string]bool{}
	for _, raw := range req.Param.Search {
		var cs struct {
			CallID []string `json:"callid"`
		}
		_ = json.Unmarshal(raw, &cs)
		for _, c := range cs.CallID {
			sids[c] = true
		}
	}
	// the legs correlated to the asked ones, both ways, as homer-app does
	// by correlation_id
	for _, rec := range s.records {
		corr, _ := rec.Data["correlation_id"].(string)
		switch {
		case corr == "":
		case sids[rec.Sid]:
			sids[corr] = true
		case sids[corr]:
			sids[rec.Sid] = true
		}
	}
	from, to := time.UnixMilli(req.Timestamp.From), time.UnixMilli(req.Timestamp.To)
	var out []Record
	for _, rec := range s.records {
		if !sids[rec.Sid] || rec.Time.Before(from) || rec.Time.After(to) || !contains(keys, rec.Key) {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *Server) transaction(w http.ResponseWriter, body []byte) {
	// homer-app puts RTCP and HEP logs of the session in the flow too,
	// in the order of time
	recs, err := s.callRecords(body, "1_call", "1_registration", "1_default", "5_default", "100_default")
	if err != nil {
		writeJSON(w, 400, map[string]any{"message": err.Error()})
		return
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Time.Before(recs[j].Time) })
	messages := []map[string]any{}
	calldata := []map[string]any{}
	hosts := map[string]any{}
	pos := 0
	for _, rec := range recs {
		m := flatten(rec)
		m["raw"] = rec.Raw
		// micro_ts is in milliseconds, as homer-app gives it; timeSeconds
		// and timeUseconds of the message keep the microseconds
		ts := rec.Time.UnixMilli()
		m["create_ts"] = ts
		m["micro_ts"] = ts
		messages = append(messages, m)
		src := fmt.Sprintf("%v:%v", rec.Proto["srcIp"], rec.Proto["srcPort"])
		dst := fmt.Sprintf("%v:%v", rec.Proto["dstIp"], rec.Proto["dstPort"])
		for _, h := range []string{src, dst} {
			if _, ok := hosts[h]; !ok {
				hosts[h] = map[string]any{"host": []string{h}, "position": pos}
				pos++
			}
		}
		calldata = append(calldata, map[string]any{
			"id": rec.ID, "sid": rec.Sid, "srcId": src, "dstId": dst, "srcIp": rec.Proto["srcIp"], "dstIp": rec.Proto["dstIp"],
			"srcPort": rec.Proto["srcPort"], "dstPort": rec.Proto["dstPort"], "method": flowMethod(rec), "method_text": flowMethod(rec),
			"create_date": rec.Time.UnixMilli(), "micro_ts": ts, "protocol": 17, "msg_color": "blue",
			"aliasSrc": alias(rec.Proto["srcIp"], rec.Proto["srcPort"]), "aliasDst": alias(rec.Proto["dstIp"], rec.Proto["dstPort"]),
			"destination": hosts[dst].(map[string]any)["position"],
		})
	}
	writeJSON(w, 201, map[string]any{"total": len(messages), "data": map[string]any{
		"messages": messages, "calldata": calldata, "hosts": hosts, "alias": map[string]any{}}, "keys": []string{}})
}

func (s *Server) qos(w http.ResponseWriter, body []byte) {
	recs, err := s.callRecords(body, "5_default")
	if err != nil {
		writeJSON(w, 400, map[string]any{"message": err.Error()})
		return
	}
	data := []map[string]any{}
	for _, rec := range recs {
		m := flatten(rec)
		m["raw"] = rec.Raw
		data = append(data, m)
	}
	writeJSON(w, 201, map[string]any{"rtcp": map[string]any{"data": data}, "rtp": map[string]any{"data": []any{}}})
}

func (s *Server) logs(w http.ResponseWriter, body []byte) {
	recs, err := s.callRecords(body, "100_default")
	if err != nil {
		writeJSON(w, 400, map[string]any{"message": err.Error()})
		return
	}
	data := []map[string]any{}
	for _, rec := range recs {
		data = append(data, flatten(rec))
	}
	writeJSON(w, 201, map[string]any{"data": data})
}

func (s *Server) exportText(w http.ResponseWriter, body []byte) {
	recs, _ := s.callRecords(body, "1_call", "1_registration", "1_default")
	var b strings.Builder
	for _, rec := range recs {
		fmt.Fprintf(&b, "%s %v:%v -> %v:%v\n\n%s\n\n", rec.Time.UTC().Format(time.RFC3339Nano),
			rec.Proto["srcIp"], rec.Proto["srcPort"], rec.Proto["dstIp"], rec.Proto["dstPort"], rec.Raw)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write([]byte(b.String()))
}

// exportPCAP writes a real libpcap file (Ethernet/IPv4/UDP).
func (s *Server) exportPCAP(w http.ResponseWriter, body []byte) {
	recs, _ := s.callRecords(body, "1_call", "1_registration", "1_default")
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, []uint32{0xa1b2c3d4})
	_ = binary.Write(&buf, binary.LittleEndian, []uint16{2, 4})
	_ = binary.Write(&buf, binary.LittleEndian, []uint32{0, 0, 65535, 1})
	for _, rec := range recs {
		payload := []byte(rec.Raw)
		pkt := udpPacket(fmt.Sprint(rec.Proto["srcIp"]), fmt.Sprint(rec.Proto["dstIp"]),
			uint16(toInt(rec.Proto["srcPort"])), uint16(toInt(rec.Proto["dstPort"])), payload)
		_ = binary.Write(&buf, binary.LittleEndian, []uint32{uint32(rec.Time.Unix()), uint32(rec.Time.Nanosecond() / 1000), uint32(len(pkt)), uint32(len(pkt))})
		buf.Write(pkt)
	}
	w.Header().Set("Content-Disposition", "attachment; filename=export.pcap")
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(buf.Bytes())
}

func udpPacket(src, dst string, sport, dport uint16, payload []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 1, 0x08, 0x00}) // ethernet
	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(20+8+len(payload)))
	ip[8], ip[9] = 64, 17
	copy(ip[12:16], net.ParseIP(src).To4())
	copy(ip[16:20], net.ParseIP(dst).To4())
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(ip[i:]))
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	binary.BigEndian.PutUint16(ip[10:], ^uint16(sum))
	b.Write(ip)
	udp := make([]byte, 8)
	binary.BigEndian.PutUint16(udp[0:], sport)
	binary.BigEndian.PutUint16(udp[2:], dport)
	binary.BigEndian.PutUint16(udp[4:], uint16(8+len(payload)))
	b.Write(udp)
	b.Write(payload)
	return b.Bytes()
}

func (s *Server) importPCAP(w http.ResponseWriter, r *http.Request) {
	f, _, err := r.FormFile("fileKey")
	if err != nil {
		writeJSON(w, 400, map[string]any{"message": "fileKey was not found"})
		return
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	n := 0
	if len(data) > 24 {
		for off := 24; off+16 <= len(data); {
			l := int(binary.LittleEndian.Uint32(data[off+8:]))
			off += 16 + l
			n++
		}
	}
	s.imported += n
	writeJSON(w, 201, map[string]any{"data": map[string]any{"good": n, "bad": 0}, "message": fmt.Sprintf("PCAP imported: %d packets", n)})
}

func (s *Server) dashboard(w http.ResponseWriter, method, id string, body []byte) {
	switch method {
	case http.MethodGet:
		d, ok := s.dashes[id]
		if !ok {
			writeJSON(w, 400, map[string]any{"message": "dashboard not found"})
			return
		}
		writeJSON(w, 200, map[string]any{"auth": "ok", "status": "ok", "total": 1, "data": d})
	case http.MethodPost, http.MethodPut:
		var d map[string]any
		if err := json.Unmarshal(body, &d); err != nil {
			writeJSON(w, 400, map[string]any{"message": err.Error()})
			return
		}
		d["id"] = id
		s.dashes[id] = d
		writeJSON(w, 200, map[string]any{"data": id, "message": "successfully saved dashboard"})
	case http.MethodDelete:
		delete(s.dashes, id)
		writeJSON(w, 200, map[string]any{"data": id, "message": "successfully deleted dashboard"})
	}
}

func (s *Server) crud(w http.ResponseWriter, method, path string, body []byte) {
	base, key := path, ""
	if _, ok := s.store[path]; !ok {
		if i := strings.LastIndex(path, "/"); i > 0 {
			base, key = path[:i], path[i+1:]
		}
	}
	items, ok := s.store[base]
	if !ok {
		writeJSON(w, 404, map[string]any{"message": "not found: " + path})
		return
	}
	keyField := "guid"
	if base == "/agent/subscribe" {
		keyField = "uuid"
	}
	find := func() int {
		for i, it := range items {
			if fmt.Sprint(it[keyField]) == key {
				return i
			}
		}
		return -1
	}
	switch {
	case method == http.MethodGet && key == "":
		writeJSON(w, 200, map[string]any{"count": len(items), "data": items})
	case method == http.MethodGet:
		if i := find(); i >= 0 {
			writeJSON(w, 200, map[string]any{"count": 1, "data": []any{items[i]}})
			return
		}
		writeJSON(w, 404, map[string]any{"message": "not found"})
	case method == http.MethodPost && key == "":
		var it map[string]any
		if err := json.Unmarshal(body, &it); err != nil {
			writeJSON(w, 400, map[string]any{"message": err.Error()})
			return
		}
		if it[keyField] == nil || it[keyField] == "" {
			it[keyField] = fmt.Sprintf("mock-%d", time.Now().UnixNano())
		}
		delete(it, "password")
		s.store[base] = append(items, it)
		if base == "/token/auth" {
			writeJSON(w, 201, map[string]any{"message": "successfully created auth token", "data": map[string]any{"token": "tok-" + fmt.Sprint(it[keyField])}})
			return
		}
		writeJSON(w, 201, map[string]any{"message": "successfully created", "data": it[keyField]})
	case method == http.MethodPut:
		i := find()
		if i < 0 {
			writeJSON(w, 404, map[string]any{"message": "not found"})
			return
		}
		var it map[string]any
		if err := json.Unmarshal(body, &it); err != nil {
			writeJSON(w, 400, map[string]any{"message": err.Error()})
			return
		}
		it[keyField] = key
		delete(it, "password")
		items[i] = it
		writeJSON(w, 201, map[string]any{"message": "successfully updated", "data": key})
	case method == http.MethodDelete:
		i := find()
		if i < 0 {
			writeJSON(w, 404, map[string]any{"message": "not found"})
			return
		}
		s.store[base] = append(items[:i], items[i+1:]...)
		writeJSON(w, 201, map[string]any{"message": "successfully deleted", "data": key})
	default:
		writeJSON(w, 405, map[string]any{"message": "method not allowed"})
	}
}

// Item returns a stored settings item (tests).
func (s *Server) Item(listPath, key string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.store[listPath] {
		if fmt.Sprint(it["guid"]) == key || fmt.Sprint(it["uuid"]) == key {
			return it
		}
	}
	return nil
}

// Count returns the number of items in a settings list (tests).
func (s *Server) Count(listPath string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.store[listPath])
}

// Sids returns session ids of a profile (tests).
func (s *Server) Sids(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, r := range s.records {
		if r.Key == key && !seen[r.Sid] {
			seen[r.Sid] = true
			out = append(out, r.Sid)
		}
	}
	return out
}

func adminOnly(path string) bool {
	for _, p := range []string{"/alias", "/mapping/protocol", "/hepsub/protocol", "/advanced", "/token/auth"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	}
	n, _ := strconv.Atoi(fmt.Sprint(v))
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func field(id, name, typ string, pos int, hide bool, defaults ...string) map[string]any {
	f := map[string]any{"id": id, "name": name, "type": typ, "index": "none", "form_type": "input", "position": pos, "hide": hide, "skip": false}
	if len(defaults) > 0 {
		f["form_default"] = defaults
	}
	return f
}

func mappings() []map[string]any {
	sip := []any{
		field("sid", "Session ID", "string", 1, false),
		field("data_header.method", "SIP Method", "string", 2, false, "INVITE", "BYE", "100", "200", "183", "CANCEL"),
		field("data_header.callid", "CallID", "string", 3, true),
		field("protocol_header.correlation_id", "Correlation ID", "string", 3, true),
		field("data_header.ruri_user", "RURI user", "string", 4, true),
		field("data_header.from_user", "SIP From user", "string", 5, false),
		field("data_header.to_user", "SIP To user", "string", 6, false),
		field("data_header.user_agent", "User Agent", "string", 7, false),
		field("data_header.cseq", "CSeq", "string", 8, true),
		field("protocol_header.srcIp", "Source IP", "string", 9, false),
		field("protocol_header.dstIp", "Destination IP", "string", 10, false),
		field("protocol_header.srcPort", "Src Port", "integer", 11, true),
		field("protocol_header.dstPort", "Dst Port", "integer", 12, true),
		field("protocol_header.captureId", "Capture ID", "string", 13, true),
	}
	// the correlation of homer-app's default mappings (migration/jsonschema),
	// the mistake of its 100_default included: data_header.callid is no SQL
	rule := func(source string, id int, profile, field string) map[string]any {
		return map[string]any{"source_field": source, "lookup_id": id, "lookup_profile": profile, "lookup_field": field, "lookup_range": []int{-300, 200}}
	}
	sipCorr := func(profile string) []any {
		return []any{rule("data_header.callid", 100, "default", "sid"), rule("data_header.callid", 5, "default", "sid"),
			rule("protocol_header.correlation_id", 1, profile, "sid")}
	}
	b2b := rule("data_header.callid", 1, "call", "data_header->>'callid'")
	b2b["input_function_js"] = "var returnData=[]; for (var i = 0; i < data.length; i++) { returnData.push(data[i]+'_b2b-1'); }; returnData;"
	corr := map[string]any{
		"call": append(sipCorr("call"), b2b), "registration": sipCorr("registration"), "default": sipCorr("default"),
		"log":  []any{rule("sid", 1, "call", "data_header.callid"), rule("sid", 1, "registration", "data_header.callid"), rule("sid", 1, "default", "data_header.callid")},
		"rtcp": map[string]any{},
	}
	row := func(guid string, hepid int, alias, profile string, fields []any) map[string]any {
		c := corr[profile]
		switch hepid {
		case 100:
			c = corr["log"]
		case 5:
			c = corr["rtcp"]
		}
		return map[string]any{"guid": guid, "hepid": hepid, "hep_alias": alias, "profile": profile, "partid": 10, "version": 1,
			"retention": 14, "partition_step": 10, "fields_mapping": fields, "correlation_mapping": c}
	}
	logFields := []any{field("sid", "Session ID", "string", 1, false), field("raw", "Message", "string", 2, false),
		field("protocol_header.srcIp", "Source IP", "string", 3, false)}
	rtcpFields := []any{field("sid", "Session ID", "string", 1, false), field("protocol_header.srcIp", "Source IP", "string", 2, false),
		field("protocol_header.dstIp", "Destination IP", "string", 3, false)}
	return []map[string]any{
		row("m-1", 1, "SIP", "call", sip),
		row("m-2", 1, "SIP", "registration", sip),
		row("m-3", 1, "SIP", "default", sip),
		row("m-4", 5, "RTCP", "default", rtcpFields),
		row("m-5", 100, "LOG", "default", logFields),
	}
}
