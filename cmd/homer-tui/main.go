// homer-tui is a terminal UI over the Homer 7 API (sipcapture homer-app).
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/napadailo/voip-tui/i18n"
	"github.com/napadailo/voip-tui/internal/homer/config"
	"github.com/napadailo/voip-tui/internal/homer/ui"
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

const sampleConfig = `currentHost: prod          # the host to start on, default: the first one
theme: default             # :theme lists them, custom ones below
# language: ru             # interface language (en, ru), default: from LANG; :lang switches
downloadDir: ~/Downloads   # where PCAP and text exports are saved
defaultSince: 1h           # search window when a query gives none
defaultLimit: 200
refreshSeconds: 5          # auto-refresh period (a in search results)
# flow:                    # how a call opens; f, a, u, v change it until homer-tui quits
#   hide: [LOG]            # kinds not to show: SIP, RTCP, LOG, "HEP 54"
#   byAlias: true          # one lane per alias instead of per address
#   unfold: false          # each host once, even when a call comes back
#   qosGraphs: true        # the QoS tab as graphs instead of a table
# searchFields:            # the fields of the search form by profile; ctrl-a, ctrl-d change them until homer-tui quits
#   1_call: [from_user, to_user, callid, user_agent]
hosts:
  - name: prod
    url: https://homer.example.com
    username: admin
    passwordCmd: security find-generic-password -s homer-prod -w
    labels: {dc: dc1, env: prod}   # or a plain tag: labels: prod, or a list: [prod, dc1]
  - name: lab
    url: http://127.0.0.1:9080
    username: admin
    password: sipcapture
  - name: ci
    url: https://homer.example.com
    tokenCmd: security find-generic-password -s homer-ci-token -w   # or token: <Auth-Token from :tokens>
    insecure: true          # do not verify TLS
    # apiPrefix: /homer     # when homer-app runs with main_settings.apiprefix
    # timezone: Europe/Berlin   # the zone to show times in
themes:
  noc:
    base: nord             # start from a built-in theme
    accent: "#ffaf00"
`

func main() {
	var (
		cfgPath   = flag.String("config", config.DefaultPath(), "config file")
		hostName  = flag.String("host", "", "host to start on (default: currentHost)")
		url       = flag.String("url", "", "ad-hoc Homer URL, configured hosts stay as they are ($HOMER_URL)")
		user      = flag.String("user", "", "username for -url ($HOMER_USER)")
		password  = flag.String("password", "", "password for -url; others see it in ps, $HOMER_PASSWORD does not show")
		token     = flag.String("token", "", "Auth-Token for -url instead of username/password; others see it in ps, $HOMER_TOKEN does not show")
		insecure  = flag.Bool("insecure", false, "skip TLS verification for -url")
		command   = flag.String("c", "", "initial command, e.g. -c 'calls from_user=100 since=2h'")
		dlDir     = flag.String("download-dir", "", "where PCAP/text exports are saved")
		themeName = flag.String("theme", "", "color theme")
		lang      = flag.String("lang", "", "interface language (en, ru), overrides the config and LANG")
		showVer   = flag.Bool("version", false, "print version")
		sample    = flag.Bool("sample-config", false, "print a sample config file")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "homer-tui %s - terminal UI over the Homer 7 API (homer-app /api/v3)\n\nUsage: homer-tui [flags]\n\n", version)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nConfig: %s (homer-tui -sample-config prints an example)\n", config.DefaultPath())
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
	// the environment after the flags, that -h and a wrong flag do not print it
	for _, f := range []struct {
		v   *string
		env string
	}{{url, "HOMER_URL"}, {user, "HOMER_USER"}, {password, "HOMER_PASSWORD"}, {token, "HOMER_TOKEN"}} {
		if *f.v == "" {
			*f.v = os.Getenv(f.env)
		}
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
	if *dlDir != "" {
		cfg.DownloadDir = config.ExpandHome(*dlDir)
	}
	if *url != "" {
		// ad-hoc host from flags or the environment, configured hosts stay as they are
		h := config.Host{Name: "cli", URL: *url, Username: *user, Password: *password, Token: *token, Insecure: *insecure}
		if err := cfg.AddHost(h); err != nil {
			fatal(err)
		}
		*hostName = "cli"
	}
	if len(cfg.Hosts) == 0 {
		fmt.Fprintf(os.Stderr, i18n.T("no Homer hosts configured.\n\nCreate %s, for example:\n\n%s\nor run: HOMER_PASSWORD=sipcapture homer-tui -url http://homer:9080 -user admin\n"),
			cfg.Path(), sampleConfig)
		os.Exit(2)
	}
	if *hostName != "" {
		cfg.CurrentHost = *hostName
	}
	host, ok := cfg.Current()
	if !ok || *hostName != "" && host.Name != *hostName {
		fatal(fmt.Errorf(i18n.T("host %q not found in %s"), *hostName, cfg.Path()))
	}
	if err := theme.Register(cfg.Themes); err != nil {
		fatal(err)
	}
	if *themeName != "" {
		cfg.Theme = *themeName
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
	app := ui.New(cfg, host)
	if err := app.SetTheme(cfg.Theme); err != nil {
		fatal(err)
	}
	if err := app.Start(*command); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "homer-tui:", err)
	os.Exit(1)
}
