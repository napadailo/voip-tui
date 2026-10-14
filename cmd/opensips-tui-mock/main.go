// opensips-tui-mock emulates OpenSIPS hosts for opensips-tui.
//
// With -confdir it writes a config of hosts of different OpenSIPS releases
// and ways to MI, then serves mi_http for the host reached with via: direct:
//
//	go run ./cmd/opensips-tui-mock -confdir /tmp/opensips-demo
//
// The other hosts go through opensips-tui-mock itself as ssh (ssh.command
// in that config): it takes ssh arguments and answers as the host the
// target names: mock-3.1.10 runs 3.1.10, pkg3.4.9 is a newer installed
// package, no<module> leaves a module out (nohttp, nofifo: mi_http,
// mi_fifo), "down" is unreachable. -W 127.0.0.1:8888 is the tunnel to
// mi_http. State (dialogs, contacts, gateway states) is kept between runs
// in $OPENSIPS_TUI_MOCK_STATE.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/napadailo/voip-tui/internal/opensips/mock"
)

func main() {
	dir := os.Getenv("OPENSIPS_TUI_MOCK_STATE")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "opensips-tui-mock")
	}
	if len(os.Args) > 1 && (os.Args[1] == "-confdir" || os.Args[1] == "--confdir") {
		demo(dir)
		return
	}
	if tunnel, rc := mock.TunnelMain(os.Args[1:], dir, os.Stdin, os.Stdout, os.Stderr); tunnel {
		os.Exit(rc) // ssh -W to mi_http
	}
	res, err := mock.SSHMain(os.Args[1:], dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "opensips-tui-mock:", err)
		os.Exit(255)
	}
	fmt.Fprint(os.Stdout, res.Out)
	fmt.Fprint(os.Stderr, res.Err)
	os.Exit(res.RC)
}

// demo writes the config of the demo hosts and serves mi_http for the one
// reached with via: direct.
func demo(stateDir string) {
	confDir := flag.String("confdir", "", "write opensips-tui.yaml for the emulated hosts here")
	listen := flag.String("listen", mock.DemoListen, "mi_http address of the host with via: direct")
	flag.Parse()
	self, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	if self, err = filepath.Abs(self); err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*confDir, 0o755); err != nil {
		log.Fatal(err)
	}
	path, _ := filepath.Abs(filepath.Join(*confDir, "opensips-tui.yaml"))
	if err := os.WriteFile(path, []byte(mock.DemoYAML(self, ln.Addr().String())), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("mock OpenSIPS hosts: ssh through %s, mi_http of %s on %s", self, mock.DemoDirect, ln.Addr())
	// the opensips-tui built next to the emulator, not another one in $PATH
	tool := "opensips-tui"
	if p := filepath.Join(filepath.Dir(self), tool); fileExists(p) {
		tool = p
	}
	log.Printf("config written to %s, run: %s -config %s", path, tool, path)
	log.Fatal(mock.ServeMI(ln, mock.DemoDirect, stateDir))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
