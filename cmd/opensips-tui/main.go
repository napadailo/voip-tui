// opensips-tui is a terminal UI over the OpenSIPS MI, through opensips-cli on
// OpenSIPS hosts reached over ssh.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/opensips/config"
	"github.com/napadailo/voip-tui/internal/opensips/ui"
	"github.com/napadailo/voip-tui/tui/theme"
)

var version = "dev"

// go install module@version does not pass -ldflags; the module version
// recorded in the binary stands in for it.
func init() {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version
	}
}

const sampleConfig = `currentHost: edge-n1  # the host to start on, default: the first one
refreshSeconds: 5
defaultView: dialogs     # view on start with one host: dialogs (default), info, contacts, stats…
theme: default           # :theme lists them, custom ones below
# language: ru          # interface language (en, ru), default: from LANG; :lang switches
readOnly: false          # true: no state-changing actions on any host
ssh:
  controlPersistSeconds: 300
  connectTimeoutSeconds: 5
hosts:
  - name: edge-n1        # ssh defaults to the name: a ~/.ssh/config alias works as is
  - name: edge-n2
    ssh: ops@edge-n2.example.com
    port: 22
    sshOptions: [ProxyJump=bastion.example.com]
    sudo: true           # sudo -n opensips-cli, usually needed for mi_fifo
    cli: /usr/bin/opensips-cli
    cliArgs: [-f, /etc/opensips/opensips-cli.cfg]
    # instance: edge     # opensips-cli -i
    labels: {dc: dc1, env: prod}   # or a plain tag: labels: prod, or a list: [prod, dc1]
    readOnly: true
  # how MI is reached: via cli (default, the two above), fifo, http or direct
  - name: core-1
    via: fifo            # mi_fifo over ssh, no opensips-cli on the host
    fifo: /tmp/opensips_fifo   # mi_fifo fifo_name and reply_dir, the defaults
    replyDir: /tmp/
    sudo: true           # sudo -n for the fifo, as for opensips-cli
  - name: core-2
    via: http            # mi_http through an ssh tunnel (ssh -W)
    mi: http://127.0.0.1:8888/mi   # as seen from the host: httpd port, mi_http root
  - name: core-3
    via: direct          # mi_http from this machine, no ssh
    mi: http://192.0.2.13:8888/mi
themes:
  noc:
    base: nord           # start from a built-in theme
    accent: "#ffaf00"
    err: "#ff5f5f"
`

func main() {
	var (
		cfgPath   = flag.String("config", config.DefaultPath(), "config file")
		hostName  = flag.String("host", "", "host to start on (default: currentHost)")
		command   = flag.String("c", "", "initial command, e.g. -c dialogs or -c 'mi get_statistics dialog:'")
		themeName = flag.String("theme", "", "color theme")
		lang      = flag.String("lang", "", "interface language (en, ru), overrides the config and LANG")
		readOnly  = flag.Bool("readonly", false, "no state-changing actions")
		showVer   = flag.Bool("version", false, "print version")
		sample    = flag.Bool("sample-config", false, "print a sample config file")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "opensips-tui %s - terminal UI over the OpenSIPS MI (opensips-cli) over ssh\n\nUsage: opensips-tui [flags]\n\n", version)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nConfig: %s (opensips-tui -sample-config prints an example)\n", config.DefaultPath())
	}
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}
	if *sample {
		fmt.Print(sampleConfig)
		return
	}

	// the language of startup errors: -lang or the locale; language: from
	// the config applies once it is read
	if *lang != "" {
		if err := i18n.Set(*lang); err != nil {
			fatal(err)
		}
	} else {
		_ = i18n.Set(i18n.FromEnv())
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(err)
	}
	if len(cfg.Hosts) == 0 {
		fmt.Fprintf(os.Stderr, i18n.T("no OpenSIPS hosts configured.\n\nCreate %s, for example:\n\n%s"), cfg.Path(), sampleConfig)
		os.Exit(2)
	}
	if err := theme.Register(cfg.Themes); err != nil {
		fatal(err)
	}
	if *themeName != "" {
		cfg.Theme = *themeName
	}

	host, _ := cfg.Current()
	if *hostName != "" {
		c, ok := cfg.Find(*hostName)
		if !ok {
			fatal(fmt.Errorf(i18n.T("host %q not found in %s"), *hostName, cfg.Path()))
		}
		host = c
	}
	// -lang, then language: from the config, then the locale
	switch {
	case *lang != "":
		cfg.Language = *lang
	case cfg.Language == "":
		cfg.Language = i18n.FromEnv()
	}
	if err := i18n.Set(cfg.Language); err != nil {
		fatal(err)
	}

	ui.Version = version
	app := ui.New(cfg, host, *readOnly, nil)
	if err := app.SetTheme(cfg.Theme); err != nil {
		fatal(err)
	}
	err = app.Start(*command)
	app.Close()
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "opensips-tui:", err)
	os.Exit(1)
}
