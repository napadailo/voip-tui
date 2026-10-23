package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/napadailo/voip-tui/internal/homer/config"
	"github.com/napadailo/voip-tui/internal/homer/mock"
)

// The emulator's config loads: four hosts, labels in the forms of
// rtpengine-tui, lab current; a host needs a url and a unique name.
func TestDemoConfigAndValidation(t *testing.T) {
	cfg := &config.Config{}
	if err := config.Parse([]byte(mock.DemoYAML("http://127.0.0.1:9080", "tok")), cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 4 || cfg.Hosts[1].Token != "tok" || cfg.Hosts[1].Auth() != "token" || cfg.Hosts[0].Labels.String() != "role=lab" {
		t.Fatalf("%+v", cfg.Hosts)
	}
	if h, _ := cfg.Current(); h.Name != "lab" || h.TokenHeader != "Auth-Token" {
		t.Fatalf("current %+v", h)
	}
	h := config.Host{TokenCmd: "echo ' tok '"}
	if tok, err := h.ResolveToken(); err != nil || tok != "tok" || h.Auth() != "tokenCmd" {
		t.Fatalf("tokenCmd: %q %v %s", tok, err, h.Auth())
	}
	f := &config.Config{}
	if err := config.Parse([]byte("flow: {hide: [log, 'hep  54'], byAlias: true, unfold: false, qosGraphs: true}\nsearchFields: {1_call: [from_user, ua]}\nhosts: []\n"), f); err != nil || strings.Join(f.SearchFields["1_call"], ",") != "from_user,ua" ||
		strings.Join(f.Flow.Hide, ",") != "LOG,HEP 54" || !f.Flow.ByAlias || f.Flow.Unfold == nil || *f.Flow.Unfold || !f.Flow.QoSGraphs {
		t.Fatalf("flow: %+v %v", f.Flow, err)
	}
	for _, bad := range []string{"hosts:\n  - {name: a}\n", "flow: {hide: [MEDIA]}\n", "hosts:\n  - {name: a, url: x}\n  - {name: a, url: y}\n", "contexts: []\n"} {
		if err := config.Parse([]byte(bad), &config.Config{}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A url with a password in it is refused, as the url is on the screen; a
// config others may read with a password in it is told.
func TestConfigSecrets(t *testing.T) {
	if err := config.Parse([]byte("hosts:\n  - {name: a, url: 'https://admin:pw@homer'}\n"), &config.Config{}); err == nil || !strings.Contains(err.Error(), "username and password") {
		t.Fatalf("%v", err)
	}
	dir := t.TempDir()
	for mode, exposed := range map[os.FileMode]bool{0o644: true, 0o600: false} {
		p := filepath.Join(dir, fmt.Sprintf("c%o.yaml", mode))
		if err := os.WriteFile(p, []byte("hosts:\n  - {name: a, url: 'https://homer', username: u, password: p}\n"), mode); err != nil {
			t.Fatal(err)
		}
		os.Chmod(p, mode)
		cfg, err := config.Load(p)
		if err != nil || cfg.Exposed() != exposed {
			t.Errorf("mode %o: exposed %v, want %v (%v)", mode, cfg != nil && cfg.Exposed(), exposed, err)
		}
	}
}
