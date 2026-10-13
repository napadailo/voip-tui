package rtpengine_test

import (
	"testing"
	"time"

	"github.com/napadailo/voip-tui/internal/rtpengine"
)

func TestProxyHost(t *testing.T) {
	for in, want := range map[string]string{
		"10.40.0.8:60899":    "10.40.0.8",
		"[2001:db8::5]:5060": "2001:db8::5",
		"2001:db8::5:5060":   "2001:db8::5",
		"10.40.0.8":          "10.40.0.8",
		"":                   "",
	} {
		if got := rtpengine.ProxyHost(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestGroupByProxy(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	got := rtpengine.GroupByProxy([]rtpengine.Session{
		{CallID: "a", Proxy: "10.0.0.1:40001", Created: t0},
		{CallID: "b", Proxy: "10.0.0.1:40002", Created: t0.Add(time.Minute), Foreign: true},
		{CallID: "c", Proxy: "10.0.0.1:40001", Created: t0.Add(2 * time.Minute), Deleted: true},
		{CallID: "d", Proxy: "10.0.0.2:5060", Created: t0.Add(time.Second)},
	})
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	p := got[0]
	if p.Proxy != "10.0.0.1" || p.Calls != 3 || p.Own != 2 || p.Foreign != 1 || p.Deleting != 1 || p.Ports != 2 ||
		!p.Oldest.Equal(t0) || !p.Newest.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("%+v", p)
	}
	if got[1].Proxy != "10.0.0.2" || got[1].Calls != 1 || got[1].Ports != 1 {
		t.Fatalf("%+v", got[1])
	}
}
