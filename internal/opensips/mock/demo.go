package mock

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/napadailo/voip-tui/internal/opensips"
	"github.com/napadailo/voip-tui/internal/opensips/config"
)

// Spec is an emulated host as its ssh target names it: mock-3.1.10 runs
// 3.1.10 (3.4.7 without a version), pkg3.4.9 is the installed package when
// it differs from the running OpenSIPS, no<module> leaves a module out
// (nohttp and nofifo: mi_http and mi_fifo), "down" is unreachable. Other
// words are free text.
type Spec struct {
	Version, Binary string
	Without         []string
	Unreachable     bool
}

// ParseTarget reads the Spec of an ssh target.
func ParseTarget(target string) Spec {
	s := Spec{Version: "3.4.7", Unreachable: strings.Contains(target, "down")}
	words := strings.Split(target, "-")
	for i, w := range words {
		switch {
		case strings.HasPrefix(w, "pkg"):
			if v, ok := opensips.ParseVersion(strings.Join(words[i:], "-")); ok {
				s.Binary = v.String()
			}
		case w == "nohttp":
			s.Without = append(s.Without, "mi_http")
		case w == "nofifo":
			s.Without = append(s.Without, "mi_fifo")
		case strings.HasPrefix(w, "no") && len(w) > 2:
			s.Without = append(s.Without, w[2:])
		}
	}
	if v, ok := opensips.ParseVersion(strings.Split(target, "-pkg")[0]); ok {
		s.Version = v.String()
	}
	return s
}

// DemoListen is where opensips-tui-mock -confdir serves mi_http for
// DemoDirect by default.
const DemoListen = "127.0.0.1:18888"

// DemoDirect is the demo host reached with via: direct.
const DemoDirect = "edge-direct"

// DemoYAML is the config of the demo hosts: OpenSIPS releases, modules and
// ways to MI, named by their ssh targets (see ParseTarget). sshCommand is
// the emulator that stands in for ssh, empty for the in-process one; mi is
// the mi_http address of DemoDirect.
func DemoYAML(sshCommand, mi string) string {
	var b strings.Builder
	b.WriteString("# opensips-tui config for the emulator (opensips-tui-mock -confdir)\ncurrentHost: edge-n1\nrefreshSeconds: 3\n")
	if sshCommand != "" {
		fmt.Fprintf(&b, "ssh:\n  command: %s\n", sshCommand)
	}
	b.WriteString(`hosts:
  - {name: edge-n1, ssh: mock-3.4.7, labels: {role: edge}}
  - {name: edge-n2, ssh: mock-3.4.7-pkg3.4.9, labels: {role: edge, case: restart-pending}}
  - {name: core-36, ssh: mock-3.6.1, labels: {role: core}}
  - {name: lab-31, ssh: mock-3.1.10-nodispatcher-nortpengine, labels: {role: lab, case: no-dispatcher-rtpengine}}
  - {name: core-fifo, ssh: mock-3.6.1-fifo, via: fifo, labels: {role: core}}
  - {name: core-http, ssh: mock-3.6.1-http, via: http, labels: {role: core}}
`)
	fmt.Fprintf(&b, "  - {name: %s, via: direct, mi: %q, labels: {role: edge}}\n", DemoDirect, "http://"+mi+"/mi")
	b.WriteString(`  - {name: lab-nohttp, ssh: mock-3.4.7-nohttp, via: http, labels: {role: lab, case: no-mi_http}}
  - {name: legacy-24, ssh: mock-2.4.11, labels: {role: lab, case: opensips-2.x}}
  - {name: down, ssh: mock-down, labels: {role: lab, case: unreachable}}
`)
	return b.String()
}

// Demo is the parsed DemoYAML for the in-process emulator (tests).
func Demo() *config.Config {
	cfg := &config.Config{}
	if err := config.Parse([]byte(DemoYAML("", DemoListen)), cfg); err != nil {
		panic(err)
	}
	return cfg
}

// ServeMI answers mi_http on ln as the emulated host target, with its state
// in stateDir: DemoDirect for opensips-tui-mock -confdir.
func ServeMI(ln net.Listener, target, stateDir string) error {
	var mu sync.Mutex // one request at a time: the state file is read and written per request
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			mu.Lock()
			defer mu.Unlock()
			h, save, err := loadHost(target, stateDir)
			if err != nil {
				conn.Close()
				return
			}
			h.serveHTTP(conn)
			save()
		}()
	}
}
