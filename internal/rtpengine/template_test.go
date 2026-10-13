package rtpengine_test

import (
	"strings"
	"testing"

	"github.com/napadailo/voip-tui/internal/rtpengine"
)

func TestParseTemplateWebRTC(t *testing.T) {
	v, _ := rtpengine.ParseVersion("mr13.3.1.4")
	// the example from the rtpengine manual
	tpl := rtpengine.ParseTemplate("WebRTC", "transport-protocol=UDP/TLS/RTP/SAVPF ICE=force trickle-ICE rtcp-mux=[offer require] no-rtcp-attribute SDES=off generate-mid", v)
	if len(tpl.Items) != 7 {
		t.Fatalf("items: %+v", tpl.Items)
	}
	for _, it := range tpl.Items {
		if it.Status != rtpengine.StatusOK || it.Meaning == "" {
			t.Errorf("%s: status %v, meaning %q, note %q", it.Token, it.Status, it.Meaning, it.Note)
		}
	}
	ice := tpl.Items[1]
	if ice.Kind != "key" || ice.Name != "ICE" || ice.Value != "force" || len(ice.Values) != 1 || !strings.Contains(ice.Values[0].Meaning, "only ICE candidate") {
		t.Fatalf("ICE: %+v", ice)
	}
	mux := tpl.Items[3]
	// list values are described by the paragraph after "- `offer`"
	if mux.Value != "offer require" || len(mux.Values) != 2 || len(mux.Values[0].Meaning) < 30 || len(mux.Values[1].Meaning) < 30 {
		t.Fatalf("rtcp-mux: %+v", mux)
	}
	if rtpengine.TemplateUse("WebRTC") != "used where the proxy passes template=WebRTC" ||
		rtpengine.TemplateUse("offer") != "applied to every offer message" ||
		rtpengine.TemplateUse("default") != "applied to every signalling message" {
		t.Fatal("TemplateUse")
	}
}

func TestParseTemplateShortcutsAndMistakes(t *testing.T) {
	v, _ := rtpengine.ParseVersion("mr13.3.1.4")
	tpl := rtpengine.ParseTemplate("x", "RTP SRTP AVPF replace-origin replace=[session-connection origin] codec-strip-EVS codec=[transcode=[PCMA PCMU] mask=[G729]] "+
		"OSRTP-accept SDES-off trcikle-ICE trickle-ice webrtc internal external flags=[generate-mid] ICE=bogus", v)
	byTok := map[string]rtpengine.TemplateItem{}
	for _, it := range tpl.Items {
		byTok[it.Token] = it
	}
	if tpl.Transport != "RTP/SAVPF" {
		t.Errorf("transport %q", tpl.Transport)
	}
	if it := byTok["replace-origin"]; it.Kind != "replace" || it.Meaning == "" || it.Status != rtpengine.StatusOK {
		t.Errorf("replace-origin: %+v", it)
	}
	// session-connection is accepted by the daemon but not documented in mr13
	if it := byTok["replace=[session-connection origin]"]; len(it.Values) != 2 || it.Values[0].Status != rtpengine.StatusOK || it.Values[1].Meaning == "" {
		t.Errorf("replace list: %+v", it)
	}
	if it := byTok["codec-strip-EVS"]; it.Name != "codec strip" || it.Value != "EVS" || it.Meaning == "" {
		t.Errorf("codec-strip: %+v", it)
	}
	if it := byTok["codec=[transcode=[PCMA PCMU] mask=[G729]]"]; len(it.Values) != 2 || it.Values[0].Meaning == "" {
		t.Errorf("codec dict: %+v", it)
	}
	if it := byTok["SDES-off"]; it.Name != "SDES" || it.Value != "off" || it.Status != rtpengine.StatusOK {
		t.Errorf("SDES-off: %+v", it)
	}
	if it := byTok["OSRTP-accept"]; it.Name != "OSRTP" || it.Meaning == "" {
		t.Errorf("OSRTP-accept: %+v", it)
	}
	if it := byTok["trcikle-ICE"]; it.Status != rtpengine.StatusUnknown {
		t.Errorf("typo not flagged: %+v", it)
	}
	// undocumented alias the daemon accepts: described by the documented spelling
	if it := byTok["trickle-ice"]; it.Status != rtpengine.StatusOK || it.Name != "trickle-ICE" || it.Meaning == "" {
		t.Errorf("alias: %+v", it)
	}
	if it := byTok["webrtc"]; it.Status != rtpengine.StatusOK {
		t.Errorf("webrtc alias: %+v", it)
	}
	if it := byTok["internal"]; it.Kind != "direction" || !strings.Contains(it.Meaning, "from-interface=internal") {
		t.Errorf("internal: %+v", it)
	}
	if it := byTok["external"]; !strings.Contains(it.Meaning, "to-interface=external") {
		t.Errorf("external: %+v", it)
	}
	if it := byTok["flags=[generate-mid]"]; len(it.Values) != 1 || it.Values[0].Meaning == "" {
		t.Errorf("flags list: %+v", it)
	}
	if it := byTok["ICE=bogus"]; it.Status != rtpengine.StatusOK || len(it.Values) != 0 {
		t.Errorf("ICE=bogus: %+v", it)
	}
}

func TestParseTemplateCaseMismatch(t *testing.T) {
	v, _ := rtpengine.ParseVersion("mr13.3.1.4")
	tpl := rtpengine.ParseTemplate("x", "Generate-MID", v)
	if it := tpl.Items[0]; it.Status != rtpengine.StatusUnknown || !strings.Contains(it.Note, "case-sensitive") {
		t.Fatalf("%+v", it)
	}
}

func TestParseTemplateSuggestsTypoFix(t *testing.T) {
	v, _ := rtpengine.ParseVersion("mr13.3.1.4")
	tpl := rtpengine.ParseTemplate("x", "strict-sorce replace-orign", v)
	if it := tpl.Items[0]; !strings.Contains(it.Note, "did you mean strict-source?") {
		t.Errorf("flag: %+v", it)
	}
	if it := tpl.Items[1]; !strings.Contains(it.Note, "did you mean origin?") {
		t.Errorf("replace: %+v", it)
	}
	if strings.Contains(tpl.Items[1].Meaning, "*") {
		t.Errorf("markdown left: %q", tpl.Items[1].Meaning)
	}
	ok := rtpengine.ParseTemplate("x", "replace-origin", v)
	if strings.Contains(ok.Items[0].Meaning, "*") {
		t.Errorf("markdown left: %q", ok.Items[0].Meaning)
	}
}
