package rtpengine_test

import (
	"strings"
	"testing"

	"github.com/napadailo/voip-tui/internal/rtpengine"
	"github.com/napadailo/voip-tui/internal/rtpengine/mock"
)

// mr13 reply in the layout of call_info, with documentation addresses (RFC 5737)
const callInfoMR13 = `
callid: e309d56d-d89f-4220-9443-bc13240bbbc0
deletionmark: no
created: 1788452437
proxy: 10.40.0.8:60899
tos: 184
last_signal: 1788452447
redis_keyspace: 2
last redis update: 1788452613335822
foreign: no
recording: no

--- Tag '980b8c48-1bf7-4b0d-ac49-e51497c70441', type: FROM_TAG, label 'caller', branch '', callduration 175.407578
---     subscribed to media with monologue tag '4980118b-47ac-4d89-af5b-6afc5d5e27cb' (index: 1)
---     subscription of media with monologue tag '4980118b-47ac-4d89-af5b-6afc5d5e27cb' (index: 1)
------ Media #1 (audio over RTP/AVP) using PCMA/8000
-------- Port      10.40.0.26:13130 <>      10.42.7.15:13356, SSRC 885aced4, 8285 p, 1424998 b, 1 e, 1788452455 uts 1788452613 kts
-------- Port      10.40.0.26:13131 <>      10.42.7.15:13357 (RTCP), SSRC 885aced4, 66 p, 6580 b, 0 e, 1788452612 uts 1788452612 kts
--- Tag '4980118b-47ac-4d89-af5b-6afc5d5e27cb', type: TO_TAG, label 'callee', branch '', callduration 165.855809
---     subscribed to media with monologue tag '980b8c48-1bf7-4b0d-ac49-e51497c70441' (index: 1)
---     subscription of media with monologue tag '980b8c48-1bf7-4b0d-ac49-e51497c70441' (index: 1)
------ Media #1 (audio over RTP/AVP) using PCMA/8000
-------- Port    203.0.113.10:16630 <>   198.51.100.24:32704, SSRC 9df0e171, 8514 p, 1464328 b, 0 e, 1788452448 uts 1788452613 kts
-------- Port    203.0.113.10:16631 <>   198.51.100.24:32705 (RTCP), SSRC 9df0e171, 66 p, 6576 b, 0 e, 1788452612 uts 1788452612 kts
`

func TestParseCallInfoMR13(t *testing.T) {
	ci := rtpengine.ParseCallInfo(callInfoMR13)
	if ci == nil {
		t.Fatal("nil")
	}
	if ci.CallID != "e309d56d-d89f-4220-9443-bc13240bbbc0" || ci.Created != 1788452437 || ci.Proxy != "10.40.0.8:60899" ||
		ci.Keyspace != "2" || ci.RedisUpdate != 1788452613335822 || ci.Foreign || ci.Recording != "no" || len(ci.Extra) != 0 {
		t.Fatalf("header: %+v", ci)
	}
	if len(ci.Legs) != 2 {
		t.Fatalf("legs: %d", len(ci.Legs))
	}
	caller, callee := ci.Legs[0], ci.Legs[1]
	if caller.Name() != "caller" || caller.Type != "FROM_TAG" || callee.Name() != "callee" || int(callee.Duration) != 165 {
		t.Fatalf("legs: %+v / %+v", caller, callee)
	}
	if ci.LegName(caller.SubscribedTo[0]) != "callee" || ci.LegName(callee.Subscribers[0]) != "caller" {
		t.Fatalf("subscriptions: %v %v", caller.SubscribedTo, callee.Subscribers)
	}
	m := caller.Media[0]
	if m.Type != "audio" || m.Protocol != "RTP/AVP" || m.Codec != "PCMA/8000" || len(m.Streams) != 2 {
		t.Fatalf("media: %+v", m)
	}
	rtp, rtcp := m.Streams[0], m.Streams[1]
	if rtp.Local != "10.40.0.26:13130" || rtp.Peer != "10.42.7.15:13356" || rtp.RTCP || rtp.SSRC != "885aced4" ||
		rtp.Packets != 8285 || rtp.Bytes != 1424998 || rtp.Errors != 1 || !rtp.Kernel() || rtp.Last() != 1788452613 {
		t.Fatalf("rtp: %+v", rtp)
	}
	// uts == kts: the latest RTCP went through userspace
	if !rtcp.RTCP || rtcp.Packets != 66 || rtcp.Kernel() {
		t.Fatalf("rtcp: %+v", rtcp)
	}
	// daemon clock: created + the longest callduration
	if now := ci.Now(); now != 1788452613 {
		t.Fatalf("now: %d", now)
	}
}

func TestParseCallInfoOlderLayouts(t *testing.T) {
	// mr9.5: no subscriptions, "in dialogue with", plain ts, delay measurement
	mr95 := `
callid: abc@host
deletionmark: no
created: 1700000000
proxy: 10.0.0.1:5060
tos: 0
last_signal: 1700000005
redis_keyspace: 0
foreign: yes

--- Tag 'aaa', type: FROM_TAG, label '', branch '', callduration 30.000000, in dialogue with 'bbb'
------ Media #1 (audio over RTP/AVP) using unknown codec
-------- Port      10.0.0.2:30000 <>       10.0.0.9:4000 , SSRC 0, 0 p, 0 b, 0 e, 1700000001 ts, 0.000010000 delay_min, 0.000020000 delay_avg, 0.000030000 delay_max
`
	ci := rtpengine.ParseCallInfo(mr95)
	if ci == nil || !ci.Foreign || len(ci.Legs) != 1 {
		t.Fatalf("mr9.5: %+v", ci)
	}
	l := ci.Legs[0]
	s := l.Media[0].Streams[0]
	if l.Name() != "from-tag" || l.Dialogue != "bbb" || l.Media[0].Codec != "" || s.Packets != 0 || s.Kernel() || s.LastPacket != 1700000001 || s.Delay == "" {
		t.Fatalf("mr9.5 leg: %+v %+v", l, s)
	}
	// mr10.5/11.5 subscription lines
	mr11 := "callid: x\n--- Tag 'a', type: FROM_TAG, label 'caller', branch '', callduration 1.5\n---     subscribed to 'b'\n---     subscription of 'b'\n"
	if ci := rtpengine.ParseCallInfo(mr11); ci == nil || ci.Legs[0].SubscribedTo[0] != "b" || ci.Legs[0].Subscribers[0] != "b" {
		t.Fatalf("mr11.5: %+v", ci)
	}
	// mr6.5: one-line header, media index on the stream line
	mr65 := `
callid:                                                    abc@host | deletionmark:  no | created:  1700000000  | proxy:10.0.0.1:5060 | tos:0 | last_signal:1700000005 | redis_keyspace:1 | foreign:no

--- Tag 'aaa' type: FROM_TAG, callduration 12.000000 , in dialogue with 'bbb'
------ Media #1,       10.0.0.2:30000 <>       10.0.0.9:4000 , 600 p, 103200 b, 0 e, 1700000011 last_packet
------ Media #1,       10.0.0.2:30001 <>       10.0.0.9:4001 (RTCP), 2 p, 100 b, 0 e, 1700000010 last_packet
`
	ci = rtpengine.ParseCallInfo(mr65)
	if ci == nil || ci.CallID != "abc@host" || ci.Keyspace != "1" || len(ci.Legs) != 1 || len(ci.Legs[0].Media) != 1 || len(ci.Legs[0].Media[0].Streams) != 2 {
		t.Fatalf("mr6.5: %+v", ci)
	}
	if !ci.Legs[0].Media[0].Streams[1].RTCP {
		t.Fatal("mr6.5 rtcp")
	}
	if rtpengine.ParseCallInfo("\nCall ID not found (x).\n\n") != nil {
		t.Fatal("not found should be nil")
	}
}

func TestParseCallInfoKeepsUnknownLines(t *testing.T) {
	ci := rtpengine.ParseCallInfo(callInfoMR13 + "\nsome new field: 1\n")
	if len(ci.Extra) != 1 || !strings.Contains(ci.Extra[0].Key+ci.Extra[0].Value, "new field") {
		t.Fatalf("extra: %+v", ci.Extra)
	}
}

// The mock prints call details in the layout of each branch; the parser has
// to understand all of them.
func TestParseCallInfoMockLayouts(t *testing.T) {
	for _, ver := range []string{"mr6.5.9.12", "mr8.5.12", "mr9.5.6.2", "mr10.5.7", "mr11.5.1.30", "mr12.5.1.3", "mr13.3.1.4"} {
		srv := mock.New(ver, 3)
		ids := srv.CallIDs()
		// "call <id> info" exists since mr10.5
		out := srv.Handle("call " + ids[0] + " info")
		if rtpengine.ParseCallInfo(out) == nil {
			out = srv.Handle("list sessions " + ids[0])
		}
		ci := rtpengine.ParseCallInfo(out)
		if ci == nil || ci.CallID != ids[0] || len(ci.Legs) != 2 || len(ci.Extra) != 0 {
			t.Fatalf("%s: %+v\n%s", ver, ci, out)
		}
		for _, l := range ci.Legs {
			if len(l.Media) != 1 || len(l.Media[0].Streams) != 2 || !l.Media[0].Streams[1].RTCP {
				t.Fatalf("%s leg %s: %+v\n%s", ver, l.Tag, l, out)
			}
		}
		if ci.Now() < ci.Created {
			t.Fatalf("%s: now %d before created %d", ver, ci.Now(), ci.Created)
		}
	}
}

func TestInterfaceFor(t *testing.T) {
	ifaces := []rtpengine.Interface{
		{Name: "pub", Address: "203.0.113.10", PortMin: 16000, PortMax: 32000},
		{Name: "priv", Address: "10.40.0.26", PortMin: 10000, PortMax: 20000},
		// two interfaces on one address, told apart by the port range
		{Name: "a", Address: "10.0.0.1", PortMin: 30000, PortMax: 34999},
		{Name: "b", Address: "10.0.0.1", PortMin: 35000, PortMax: 39999},
		{Name: "v6", Address: "2001:db8::1", PortMin: 30000, PortMax: 40000},
	}
	for ep, want := range map[string]string{
		"203.0.113.10:16630": "pub",
		"10.40.0.26:13130":   "priv",
		"10.40.0.26:9":       "priv", // outside the range, the address alone is unique
		"10.0.0.1:36000":     "b",
		"10.0.0.1:9":         "", // ambiguous
		"2001:db8::1:30010":  "v6",
		"192.0.2.1:30000":    "",
	} {
		if got := rtpengine.InterfaceFor(ifaces, ep); got != want {
			t.Errorf("%s: %q, want %q", ep, got, want)
		}
	}
}
