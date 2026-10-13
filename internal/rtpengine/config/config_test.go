package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLabelsForms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yamlText := `hosts:
- {name: a, address: a, labels: "prod"}
- {name: b, address: b, labels: "prod, dc=dc1"}
- {name: c, address: c, labels: [prod, dc1]}
- {name: d, address: d, labels: {env: prod, dc: dc1}}
- {name: e, address: e}
`
	if err := os.WriteFile(path, []byte(yamlText), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Labels{
		{"prod": ""},
		{"prod": "", "dc": "dc1"},
		{"prod": "", "dc1": ""},
		{"env": "prod", "dc": "dc1"},
		nil,
	}
	for i, h := range cfg.Hosts {
		if len(h.Labels) != len(want[i]) {
			t.Fatalf("%s: %v, want %v", h.Name, h.Labels, want[i])
		}
		for k, v := range want[i] {
			if got, ok := h.Labels[k]; !ok || got != v {
				t.Fatalf("%s: %v, want %v", h.Name, h.Labels, want[i])
			}
		}
	}
}
