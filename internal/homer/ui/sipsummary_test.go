package ui

import (
	"regexp"
	"strings"
	"testing"
)

const richInvite = "INVITE sip:70000000001@198.51.100.7:5060;user=phone SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 203.0.113.6:5060;branch=z9hG4bK-2\r\n" +
	"v: SIP/2.0/UDP 192.168.20.6:5060;branch=z9hG4bK-1;received=192.168.20.6\r\n" +
	"Record-Route: <sip:203.0.113.6;lr>, <sip:192.168.20.6;lr>\r\n" +
	"Max-Forwards: 68\r\n" +
	"f: \"Ivan Petrov\" <sip:119@example.com>;tag=a1\r\n" +
	"t: <sip:70000000001@198.51.100.7>\r\n" +
	"i: abc@192.168.20.6\r\n" +
	"CSeq: 1 INVITE\r\n" +
	"P-Asserted-Identity: <tel:+70000000002>\r\n" +
	"Diversion: <sip:200@example.com>;reason=unconditional;counter=1\r\n" +
	"Contact: <sip:119@192.168.20.6:5060>\r\n" +
	"Session-Expires: 1800;refresher=uac\r\n" +
	"Min-SE: 90\r\n" +
	"Supported: 100rel, timer, replaces\r\n" +
	"Allow: INVITE, ACK, BYE, CANCEL, PRACK, UPDATE\r\n" +
	"User-Agent: Asterisk PBX 20\r\n" +
	"c: application/sdp\r\n" +
	"\r\n" +
	"v=0\r\no=- 1 2 IN IP4 192.168.20.6\r\ns=-\r\nc=IN IP4 192.168.20.6\r\nt=0 0\r\n" +
	"m=audio 40000 RTP/AVP 8 0 101\r\na=rtpmap:101 telephone-event/8000\r\na=ptime:20\r\na=sendonly\r\n"

const byeReason = "BYE sip:119@192.168.20.6:5060 SIP/2.0\r\nVia: SIP/2.0/UDP 198.51.100.7:5060;branch=z9\r\n" +
	"Reason: Q.850;cause=16;text=\"Normal call clearing\"\r\nCSeq: 2 BYE\r\n\r\n"

// The plain view of a message: parties, the way from the first hop, media
// with codecs by name and hold, session rules, the answer and Q.850.
func TestSIPSummary(t *testing.T) {
	alias := func(h string) string {
		if strings.HasPrefix(h, "192.168.20.6") || strings.HasPrefix(h, "203.0.113.6") {
			return "GW (" + h + ")"
		}
		return h
	}
	tags := regexp.MustCompile(`\[[^\[\]]*\]`)
	plain := func(m string) string { return tags.ReplaceAllString(sipSummary(m, alias).String(), "") }
	inv := plain(richInvite)
	for _, want := range []string{
		`request URI\s+70000000001@198\.51\.100\.7:5060`, `From\s+"Ivan Petrov" 119@example\.com`, `P-Asserted-Identity\s+\+70000000002`,
		`Diversion\s+200@example\.com \(unconditional\)`, `Via\s+UDP: GW \(192\.168\.20\.6:5060\) → GW \(203\.0\.113\.6:5060\)`,
		`Record-Route\s+GW \(192\.168\.20\.6\) → GW \(203\.0\.113\.6\)`, `audio\s+192\.168\.20\.6:40000 · PCMA PCMU telephone-event · sendonly · ptime 20 · on hold`,
		`Session-Expires\s+1800;refresher=uac`,
	} {
		if !regexp.MustCompile(want).MatchString(inv) {
			t.Errorf("no %s in\n%s", want, inv)
		}
	}
	if bye := plain(byeReason); !strings.Contains(bye, "Reason  Q.850 16: Normal call clearing\n") {
		t.Errorf("reason:\n%s", bye)
	}
	if ans := plain("SIP/2.0 487 Request Terminated\r\nCSeq: 1 INVITE\r\n\r\n"); strings.Contains(ans, "Parties") ||
		!strings.Contains(ans, "487 Request Terminated - cancelled before the answer") {
		t.Errorf("answer:\n%s", ans)
	}
}
