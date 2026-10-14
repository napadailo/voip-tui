package config

import (
	"os"
	"os/exec"
	"testing"
)

// The sample printed by -sample-config must load: Parse rejects unknown fields.
func TestSampleConfigLoads(t *testing.T) {
	out, err := exec.Command("go", "run", "../../../cmd/opensips-tui", "-sample-config").Output()
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 5 || cfg.Hosts[1].SSHOptions[0] != "ProxyJump=bastion.example.com" || !cfg.Hosts[1].Sudo {
		t.Fatalf("%+v", cfg.Hosts)
	}
	for i, via := range []string{ViaCLI, ViaCLI, ViaFIFO, ViaHTTP, ViaDirect} {
		if cfg.Hosts[i].Via != via {
			t.Fatalf("%s: via %q, want %q", cfg.Hosts[i].Name, cfg.Hosts[i].Via, via)
		}
	}
	if c := cfg.Hosts[4]; c.MI != "http://192.0.2.13:8888/mi" || cfg.Hosts[2].FIFO != DefaultFIFO {
		t.Fatalf("%+v", c)
	}
	if _, err := Load(t.TempDir() + "/missing.yaml"); err != nil {
		t.Fatal(err)
	}
	bad := t.TempDir() + "/bad.yaml"
	_ = os.WriteFile(bad, []byte("hosts:\n  - name: a\n    sssh: typo\n"), 0o600)
	if _, err := Load(bad); err == nil {
		t.Fatal("unknown field accepted")
	}
}
