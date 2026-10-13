// rtpengine-tui is a terminal UI over rtpengine's listen-cli, the interface of
// rtpengine-ctl, on rtpengine hosts reached over ssh.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/rtpengine/config"
	"github.com/napadailo/voip-tui/internal/rtpengine/ui"
	"github.com/napadailo/voip-tui/tui/theme"
)

var version = "dev"

const sampleConfig = `currentHost: rtp-prod-1
theme: default               # :themes lists them, custom ones below
# language: ru               # interface language (en, ru), default: from LANG; :lang switches
refreshSeconds: 5
downloadDir: ~/Downloads     # where "s" in text views saves files
ssh:
  user: admin                # defaults for all hosts, ~/.ssh/config applies too
  # identityFile: ~/.ssh/id_ed25519
  # options: ["-J", "bastion.example.com"]
hosts:
  - name: rtp-prod-1
    address: rtp1.example.com        # ssh destination or ~/.ssh/config alias
    labels: {dc: dc1, env: prod}     # or a plain tag: labels: prod, or a list: [prod, dc1]
  - name: rtp-prod-2
    address: admin@10.0.0.12
    sudo: true                       # read configs and run rtpengine-ctl via sudo -n
    via: ctl                         # use rtpengine-ctl on the host instead of an ssh tunnel
  - name: rtp-lab
    address: rtp-lab
    cli: 127.0.0.1:9900              # listen-cli; default: listen-cli from rtpengine.conf
    version: mr11.5                  # pin when rtpengine --version is not available
    configFile: /etc/rtpengine/rtpengine-lab.conf
    recordingConfigFile: /etc/rtpengine/rtpengine-recording.conf
    # "L" in the call details: the call's lines of rtpengine's log, default from journald
    logCommand: grep -hF -- {callid} /var/log/rtpengine/rtpengine.log
# themes:
#   mine: {base: nord, border: "#ff8800", ok: green}
`

func main() {
	var (
		cfgPath   = flag.String("config", config.DefaultPath(), "config file")
		host      = flag.String("host", "", "host to start on (default: currentHost)")
		address   = flag.String("address", "", "ad-hoc host: ssh destination, or \"local\"")
		cli       = flag.String("cli", "", "listen-cli address for -address (with -address local: connect directly)")
		command   = flag.String("c", "", "initial command, e.g. -c 'config' or -c 'sessions foreign'")
		themeName = flag.String("theme", "", "theme, overrides the config")
		lang      = flag.String("lang", "", "interface language (en, ru), overrides the config and LANG")
		showVer   = flag.Bool("version", false, "print version")
		sample    = flag.Bool("sample-config", false, "print a sample config file")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "rtpengine-tui %s - terminal UI over rtpengine's listen-cli (rtpengine-ctl) over ssh\n\nUsage: rtpengine-tui [flags]\n\n", version)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nConfig: %s (rtpengine-tui -sample-config prints an example)\n", config.DefaultPath())
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
	if *address != "" {
		// ad-hoc host from flags, configured hosts stay untouched
		h := config.Host{Name: "cli", Address: *address, CLI: *cli}
		if *address == "local" && *cli != "" {
			h.Via = config.ViaDirect
		}
		if err := cfg.AddHost(h); err != nil {
			fatal(err)
		}
		*host = "cli"
	}
	if len(cfg.Hosts) == 0 {
		fmt.Fprintf(os.Stderr, i18n.T("no rtpengine hosts configured.\n\nCreate %s, for example:\n\n%s\nor run: rtpengine-tui -address rtp1.example.com\n"), cfg.Path(), sampleConfig)
		os.Exit(2)
	}
	if *host != "" {
		cfg.CurrentHost = *host
	}
	cur, ok := cfg.Current()
	if !ok || *host != "" && cur.Name != *host {
		fatal(fmt.Errorf(i18n.T("host %q not found in %s"), *host, cfg.Path()))
	}
	if err := theme.Register(cfg.Themes); err != nil {
		fatal(err)
	}
	if *themeName != "" {
		cfg.Theme = *themeName
	}
	if err := theme.Apply(cfg.Theme); err != nil {
		fatal(err)
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
	app := ui.New(cfg, cur)
	if err := app.Start(*command); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "rtpengine-tui:", err)
	os.Exit(1)
}
