# voip-tui

voip-tui is a set of terminal interfaces for the components of a VoIP platform. Each tool works through the component's own management interface, the same CLI or API an operator uses by hand, and shows the answers as views you can navigate, filter and act on.

![rtpengine-tui against the emulator: hosts, config, limits, log levels, a call and its log, statistics](docs/demo.gif)

## Why

Running a VoIP platform means working with several command-line tools and web interfaces at once. Each has its own commands and output format, and both change between releases. A simple question, such as what happened to a call, takes several commands and copying identifiers from one output into the next.

voip-tui keeps those tools as the source of truth and adds what daily work lacks:

- command output is parsed and shown as linked views: host, sessions, call, log;
- each tool knows the release it talks to and shows only what that release supports;
- the original output is one key away, and any command can still be run by hand from the prompt.

Nothing is installed on the servers.

## Tools

| Tool | Status | Component | Works through | What it shows |
|---|---|---|---|---|
| `rtpengine-tui` | available | [rtpengine](https://github.com/sipwise/rtpengine) | listen-cli, the interface of `rtpengine-ctl` | hosts and session counts; sessions and parsed call details (media path, packet rates, one-way audio); the call's log with follow mode; per-call debug; terminate; statistics with JSON export; calls and NG commands by signalling proxy; interfaces and port usage; runtime limits and log levels; `rtpengine.conf` checked against the host's release; signalling templates explained |
| `opensips-tui` | available | [OpenSIPS](https://opensips.org) | MI through `opensips-cli`, `mi_fifo` or `mi_http` | hosts with running and installed versions; dialogs, contacts, dispatcher destinations and drouting gateways, each with a card; ending dialogs, removing contacts, switching destinations and gateways; rtpengine sets; clusterer and sharing tags; status/report; TCP connections; statistics with rates; processes and their log levels; any MI command; views and actions checked against the host's release and loaded modules |
| `homer-tui` | in development | [Homer 7](https://github.com/sipcapture/homer) | homer-app REST API | call, registration and log search; call flow ladder; QoS from RTCP; PCAP and text export, PCAP import; settings |
| `asterisk-tui` | planned | [Asterisk](https://www.asterisk.org) | | |

Tools in development are published here once they are ready.

### How rtpengine-tui connects

`via:` in the host config picks the path to listen-cli:

| `via` | Path |
|---|---|
| `tunnel` (default) | listen-cli through an SSH tunnel (`ssh -W`) |
| `ctl` | `rtpengine-ctl` on the host over SSH |
| `direct` | listen-cli over TCP from this machine |

`rtpengine.conf` is read and the call log is taken from journald over SSH. Raw CLI commands: `:cli`.

The full documentation: [docs/rtpengine-tui.md](docs/rtpengine-tui.md).

### How opensips-tui connects

`via:` in the host config picks how MI is reached:

| `via` | Path |
|---|---|
| `cli` (default) | `opensips-cli -x mi` on the host over SSH |
| `fifo` | `mi_fifo` on the host over SSH, no opensips-cli needed |
| `http` | `mi_http` through an SSH tunnel (`ssh -W`) |
| `direct` | `mi_http` over HTTP from this machine |

The views are the same whatever `via` is. Raw MI commands: `:mi`.

The full documentation: [docs/opensips-tui.md](docs/opensips-tui.md).

These are unofficial tools, not affiliated with Sipwise, OpenSIPS Solutions, QXIP / sipcapture or Sangoma. rtpengine, OpenSIPS, Homer and Asterisk are names of their respective owners.

## How the tools behave

- **They use the component's own interface.** Every view comes from a documented command or API call, and nothing else runs on the servers. When the parsed view is not enough, `y` shows the reply as the component returned it.
- **They follow the release.** Commands the release lacks are hidden or explained, and config options are checked against that release.
- **They ask before changing things.** Ending calls, changing settings and other state-changing actions ask for confirmation; quick reversible steps, such as raising a log level, apply at once. Runtime changes say that a restart drops them.
- **They come with emulators.** Each tool has an emulator of its component that answers like several real releases, for trying the tool and for the tests.
- **They work the same way.** Vim keys, a `:` prompt with completion and history, filtering, sorting, themes and languages come from one shared framework.

## Install

Download a binary for Linux or macOS from [Releases](https://github.com/napadailo/voip-tui/releases), or install with Go 1.22+:

```sh
go install github.com/napadailo/voip-tui/cmd/rtpengine-tui@latest
go install github.com/napadailo/voip-tui/cmd/opensips-tui@latest
```

Or build from a checkout:

```sh
make build      # bin/<tool> and bin/<tool>-mock
make install    # into $GOPATH/bin
make test
```

## Try it without servers

```sh
make demo-rtpengine
make demo-opensips
```

Each starts emulators of several releases of the component, writes a config for them and prints the command that connects the tool to them.

## Configuration

Each tool reads `~/.config/<tool>/config.yaml` (under `$XDG_CONFIG_HOME` if set). An environment variable named after the tool overrides the path, for example `RTPENGINE_TUI_CONFIG`. `<tool> -sample-config` prints a commented example, `<tool> -h` lists the flags.

SSH goes through the system `ssh`, so `~/.ssh/config` aliases, ProxyJump, agents and known_hosts work as usual. One master connection per host is kept for the session.

## Keys

| Key | Action |
|---|---|
| `j` `k` `g` `G` `ctrl-d` `ctrl-u` | move |
| `enter` | open |
| `esc` / `q` | back (`esc` first clears the filter) |
| `/` | filter rows (regex, `!` inverts); search in text views, then `n` / `N` |
| `:` | command prompt with completion and history |
| `space` | mark rows for bulk actions |
| `o` / `O` | sort column / order |
| `h` / `l` | scroll sideways in wide tables and long lines |
| `w` / `s` | wrap / save, in text views |
| `ctrl-r` | refresh |
| `?` | help: keys and commands of the tool |
| `:theme` | theme picker with live preview |
| `:lang` | interface language |
| `:q` | quit |

Each view lists its own keys in the header.

## Themes and languages

Built-in themes: default, dracula, nord, gruvbox, solarized-dark, solarized-light, monokai, catppuccin, tokyo-night. `:theme` switches them in any tool. A tool whose config has a `themes:` section also accepts custom themes:

```yaml
theme: noc
themes:
  noc: {base: nord, accent: "#ffaf00"}
```

The interface is available in English and Russian; `:lang` switches it, `LANG` sets the default.

## Roadmap

- `homer-tui`, then tools for more components.

## Project layout

```
cmd/<tool>/              entry points; cmd/<tool>-mock/ emulators
internal/<tool>/         client and parsers for the component
internal/<tool>/ui/      the tool's views
internal/<tool>/config/  the tool's config
internal/<tool>/mock/    the emulator
tui/                     the framework: app frame, tables, text views, cards, prompt, help
tui/theme/               themes
i18n/                    translations; i18n/i18ntest checks the catalogs against the code
docs/                    per-tool documentation
```

`tui`, `tui/theme` and `i18n` are importable, so a new tool needs only its client and views: add `cmd/<tool>` and `internal/<tool>/...` and build the views from `tui`. The API is pre-1.0 and may still change.

## Contributing

Issues and pull requests are welcome. `make test` runs the unit tests and the UI tests against the emulators. The i18n tests fail when a user-visible string has no translation, so new text needs entries in the `locale_*.go` catalogs. When a tool learns a new command or release, teach its emulator the same output, taken from the component's sources.

## License

MIT, see [LICENSE](LICENSE).
