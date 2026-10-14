package config

import "testing"

// Labels come as a map, a list or a string, as in rtpengine-tui; without
// currentHost the first host is current; enter opens dialogs by default.
func TestLabelsCurrentAndDefaults(t *testing.T) {
	cfg := &Config{}
	err := Parse([]byte(`hosts:
  - {name: a, labels: {dc: dc1, env: prod}}
  - {name: b, labels: [prod, dc=dc2]}
  - {name: c, labels: "lab, edge"}
`), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"dc=dc1,env=prod", "dc=dc2,prod", "edge,lab"} {
		if got := cfg.Hosts[i].Labels.String(); got != want {
			t.Errorf("%s: labels %q, want %q", cfg.Hosts[i].Name, got, want)
		}
	}
	if h, ok := cfg.Current(); !ok || h.Name != "a" {
		t.Fatalf("current %v %v, want the first host", h, ok)
	}
	cfg.CurrentHost = "c"
	if h, _ := cfg.Current(); h.Name != "c" {
		t.Fatalf("current %s, want c", h.Name)
	}
	if cfg.DefaultView != "dialogs" {
		t.Fatalf("default view %q", cfg.DefaultView)
	}
	if err := Parse([]byte("hosts:\n  - {name: x, labels: {a: [1]}}\n"), &Config{}); err == nil {
		t.Fatal("a nested label value must be refused")
	}
}
