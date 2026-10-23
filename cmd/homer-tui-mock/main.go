// homer-tui-mock serves a fake homer-app API with generated SIP traffic.
//
//	go run ./cmd/homer-tui-mock -listen 127.0.0.1:9080 -confdir /tmp/homer-demo
//	homer-tui -config /tmp/homer-demo/homer-tui.yaml
//
// Without -confdir: homer-tui -url http://127.0.0.1:9080 -user admin -password sipcapture
package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/napadailo/voip-tui/internal/homer/mock"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:9080", "listen address")
	confDir := flag.String("confdir", "", "write homer-tui.yaml for the emulator here")
	seed := flag.Int64("seed", time.Now().UnixNano(), "random seed for generated traffic")
	quiet := flag.Bool("quiet", false, "do not log requests")
	flag.Parse()
	srv := mock.New(*seed)
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	log.Printf("mock homer-app on %s (user %s / %s, Auth-Token %s)", url, srv.User, srv.Password, srv.Token)
	if *confDir != "" {
		if err := os.MkdirAll(*confDir, 0o755); err != nil {
			log.Fatal(err)
		}
		path, _ := filepath.Abs(filepath.Join(*confDir, "homer-tui.yaml"))
		if err := os.WriteFile(path, []byte(mock.DemoYAML(url, srv.Token)), 0o644); err != nil {
			log.Fatal(err)
		}
		// the homer-tui built next to the emulator, not another one in $PATH
		tool := "homer-tui"
		if self, err := os.Executable(); err == nil {
			if p := filepath.Join(filepath.Dir(self), tool); fileExists(p) {
				tool = p
			}
		}
		log.Printf("config written to %s, run: %s -config %s", path, tool, path)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		srv.ServeHTTP(w, r)
		if !*quiet {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
		}
	})
	log.Fatal(http.Serve(ln, handler))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
