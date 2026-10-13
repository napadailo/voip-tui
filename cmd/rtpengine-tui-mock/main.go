// rtpengine-tui-mock serves fake rtpengine listen-cli endpoints that answer in
// the format of a chosen release.
//
//	go run ./cmd/rtpengine-tui-mock -listen 127.0.0.1:9900 -version mr12.5.1.3
//	go run ./cmd/rtpengine-tui-mock -fleet -confdir /tmp/rtpe   # four releases + config
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/napadailo/voip-tui/internal/rtpengine/mock"
)

type instance struct {
	name, addr, version string
}

func main() {
	listen := flag.String("listen", "127.0.0.1:9900", "listen-cli address")
	version := flag.String("version", "mr13.3.1.1", "rtpengine release to emulate")
	fleet := flag.Bool("fleet", false, "run four releases (mr6.5, mr9.5, mr11.5, mr13.3) on ports 9901-9904")
	confDir := flag.String("confdir", "", "write sample rtpengine.conf, and rtpengine-recording.conf per instance here")
	seed := flag.Int64("seed", time.Now().UnixNano(), "random seed for generated calls")
	flag.Parse()

	instances := []instance{{name: "rtpe-mock", addr: *listen, version: *version}}
	if *fleet {
		instances = []instance{
			{"rtpe-legacy", "127.0.0.1:9901", "mr6.5.9.12"},
			{"rtpe-old", "127.0.0.1:9902", "mr9.5.6.2"},
			{"rtpe-lts", "127.0.0.1:9903", "mr11.5.1.30"},
			{"rtpe-new", "127.0.0.1:9904", "mr13.3.1.4"},
		}
	}
	var cfg strings.Builder
	cfg.WriteString("# rtpengine-tui config for the mock\nrefreshSeconds: 3\nhosts:\n")
	for i, in := range instances {
		srv := mock.New(in.version, *seed+int64(i))
		if _, err := srv.Listen(in.addr); err != nil {
			log.Fatal(err)
		}
		log.Printf("mock rtpengine %s (%s) listen-cli on %s", in.name, srv.Version, in.addr)
		fmt.Fprintf(&cfg, "  - name: %s\n    address: local\n    via: direct\n    cli: %s\n    version: %s\n", in.name, in.addr, in.version)
		if *confDir != "" {
			dir, _ := filepath.Abs(filepath.Join(*confDir, in.name))
			conf, rec, err := mock.WriteConfs(dir, mock.ConfOpts{CLI: in.addr})
			if err != nil {
				log.Fatal(err)
			}
			fmt.Fprintf(&cfg, "    configFile: %s\n    recordingConfigFile: %s\n", conf, rec)
			logPath := filepath.Join(dir, "rtpengine.log")
			if err := srv.WriteLog(logPath); err != nil {
				log.Fatal(err)
			}
			fmt.Fprintf(&cfg, "    logCommand: grep -hF -- {callid} %s\n", logPath)
		}
	}
	if *confDir != "" {
		path := filepath.Join(*confDir, "rtpengine-tui.yaml")
		if err := os.WriteFile(path, []byte(cfg.String()), 0o644); err != nil {
			log.Fatal(err)
		}
		log.Printf("config written to %s, run: rtpengine-tui -config %s", path, path)
	} else {
		fmt.Print(cfg.String())
	}
	select {}
}
