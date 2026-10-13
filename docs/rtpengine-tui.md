# rtpengine-tui

A terminal interface over rtpengine's listen-cli, the interface `rtpengine-ctl` uses. It reaches rtpengine hosts over SSH and runs the same commands there: sessions and call details, terminate, limits and timeouts at runtime, interfaces and ports, statistics. It reads `rtpengine.conf` and `rtpengine-recording.conf` and shows them as a table, with every option checked against the rtpengine release on the host. Vim keys, themes, English or Russian interface.

Russian version: [rtpengine-tui.ru.md](rtpengine-tui.ru.md).

![rtpengine-tui against the emulator](demo.gif)

## Build

Go 1.22+.

```sh
make build            # ./bin/rtpengine-tui and ./bin/rtpengine-tui-mock
make test
make install          # into $GOPATH/bin
```

Or download a binary from [Releases](https://github.com/napadailo/voip-tui/releases).

## Connecting

The config is `~/.config/rtpengine-tui/config.yaml` (or `$RTPENGINE_TUI_CONFIG`). `rtpengine-tui -sample-config` prints an example:

```yaml
currentHost: rtp-prod-1
theme: default               # :themes lists them
language: ru                 # en or ru; without it LANG decides, :lang switches
refreshSeconds: 5
ssh:
  user: admin                # defaults for all hosts, ~/.ssh/config applies too
hosts:
  - name: rtp-prod-1
    address: rtp1.example.com        # ssh destination or ~/.ssh/config alias
    labels: {dc: dc1, env: prod}     # or a plain tag: labels: prod, or a list: [prod, dc1]
  - name: rtp-prod-2
    address: admin@10.0.0.12
    sudo: true                       # read configs and run rtpengine-ctl through sudo -n
    via: ctl
  - name: rtp-lab
    address: rtp-lab
    cli: 127.0.0.1:9900              # listen-cli; default: listen-cli from rtpengine.conf
    version: mr11.5                  # when rtpengine --version is not available
    configFile: /etc/rtpengine/rtpengine-lab.conf
    logCommand: grep -hF -- {callid} /var/log/rtpengine/rtpengine.log
```

Without a config: `rtpengine-tui -address rtp1.example.com`. `-c` sets the first command: `rtpengine-tui -host rtp-prod-1 -c config`.

SSH goes through the system `ssh`, so aliases, ProxyJump, the agent and known_hosts from `~/.ssh/config` work. There is one connection per host for the whole session (ControlMaster), so frequent refreshes do not open new ones.

How commands reach rtpengine (`via`):

| via | How | Needs on the host |
|---|---|---|
| `tunnel` (default) | `ssh -W <listen-cli>`: a TCP channel inside SSH to listen-cli | TCP forwarding allowed in sshd |
| `ctl` | `rtpengine-ctl [-ip -port] <command>` on the host | rtpengine-ctl (perl, Config::Tiny) |
| `direct` | TCP to listen-cli straight from this machine | listen-cli reachable over the network |

The listen-cli address is `cli:` from the config, else `listen-cli` from the host's rtpengine.conf, else `127.0.0.1:9900`.

## Keys

| Key | Action |
|---|---|
| `j` `k` `g` `G` | down, up, top, bottom |
| `ctrl-d` `ctrl-u`, `ctrl-f` `ctrl-b` | half page, page |
| `H` `M` `L`, `h` `l` | top, middle, bottom of the screen; scroll columns |
| `enter`, `esc` or `q` | open, back |
| `:` | command prompt (completion with `tab` and arrows, with history) |
| `/` | filter rows (regex, a leading `!` inverts); in text views search, then `n` `N` |
| `space` | in sessions mark a row (terminate works on marked rows), in the host list pick the host |
| `o` `O` | sort column, reverse order |
| `ctrl-r` | refresh |
| `?` | help |
| `:q`, `ctrl-c` | quit |

Hosts: `enter` opens the host's sessions, `space` makes the host current (marked `*`; commands like `:config` and `:limits` work on it), `c` configs, `m` limits, `i` interfaces, `p` proxies, `d` host card, `s` interactive ssh.

Sessions: `enter` call details, `ctrl-k` terminates the marked calls or the current one (asks first), `v` switches all, own, foreign, `D` per-call debug logging (mr9.5+), `L` the call's log, `a` auto-refresh.

Proxies (`p` or `:proxies`): the host's calls grouped by signalling proxy, the address NG commands came from (the `proxy` field of `list sessions`). Calls are grouped by IP without the port: a proxy's source port is ephemeral, usually one per worker, and the number of distinct ports is in the SOCKETS column. For each proxy you see its calls and their share, how many are own and foreign, when the last call came and how old the oldest one is. From mr8.5 the NG columns add what each proxy asked since the start, from `list jsonstats`: all NG commands, offer, answer and delete, errors and the average offer processing time. `enter` opens the sessions of that proxy only.

Call details: the output of `call <id> info` (`list sessions <id>` before mr10.5), parsed by call party. On top, a summary of whether RTP flows both ways, the creation and last signalling time, the owner and redis. Below, the media path "caller ⇄ rtpengine ⇄ callee", with rtpengine's addresses named after the interfaces of `list interfaces` (mr7.5+; on a shared address the interface is found by its port range), and for each party a table of streams: from which peer to which rtpengine port, packets, bytes, errors, average rate, how long ago the last packet came and whether the kernel or userspace handled it (mr12.5+). The counters are inbound: what rtpengine received from that peer. Ages are measured by the node's clock, not this machine's, so clock skew does not affect them. A silent stream gets its age in yellow or red, and "nothing received" on RTP means one-way audio. `y` switches to the raw output, the same as `rtpengine-ctl` prints. `D` turns per-call debug logging on or off, `L` opens the call's log.

Config: `enter` or `d` opens the option card (value in the file and at runtime, status, description, releases that have it), `v` shows all lines, the daemon section only or the findings only, `y` and `Y` show the raw rtpengine.conf and rtpengine-recording.conf.

Limits: `e` or `enter` puts `:set <name> ` in the prompt.

## The call's log

`L` in the call details or in the session list opens rtpengine's log lines of that call, newest at the bottom.

- `f` follows the log: every refresh reads the lines after the last one shown and keeps the view at the bottom, unless you scrolled up (`G` returns to the end). `f` again or leaving the view stops it.
- `ctrl-r` reads new lines once.
- The view keeps the last 5000 lines; the title then says how many there are in all. `s` saves the whole log of the call, not just the lines shown, to `downloadDir` as `log_<call-id>-<time>.log`.

The default is `journalctl -t rtpengine --since <call start> -o short-iso-precise | grep -F -- <call-id>`: under systemd journald has rtpengine's log both with syslog and with `log-stderr`. Following reads journald from the second of the last line shown, not from the call's start. Where rtpengine logs elsewhere, set `logCommand` for the host: `{callid}` becomes the shell-quoted Call-ID, `{since}` a journalctl `--since` value. A user without access to the journal needs `sudo: true` or membership in `systemd-journal` or `adm`.

`D` and then `L` are the usual pair: debug logging on for one call, then its debug lines here.

## Commands

| Command | What it does |
|---|---|
| `:hosts [name]`, `:nodes` | the host list, or switch to a host; `:<host name>` switches too |
| `:sessions [all\|own\|foreign\|<call-id>]` | `list sessions` |
| `:config` | rtpengine.conf and rtpengine-recording.conf as a table, checked against the release and compared with `params current` |
| `:limits` | limits and timeouts (`list maxsessions`, `list timeout` and the rest), with the values from rtpengine.conf next to them |
| `:set <name> <value>` | `set maxsessions 2000` and the like, asks first; not written to rtpengine.conf |
| `:interfaces` | interfaces and port usage (mr7.5+) |
| `:stats` (`:totals`, `:counters`, `:jsonstats`, `:transcoders`) | the statistics panel from `list jsonstats` (mr8.5+): sessions, traffic per second, totals since the start, quality, NG commands, interfaces, transcoding (chains, rates, totals from mr11.5); `y` shows the JSON, `s` saves it. Before mr8.5 the text of `list counters`, `list totals` and `list transcoders` (mr7.5) |
| `:params [current\|start\|diff]`, `:revert` | runtime configuration, revert to the startup values |
| `:terminate all\|own\|foreign\|<call-id>` | end calls, asks first |
| `:active`, `:standby` | make all calls owned or foreign (mr8.5+), asks first |
| `:proxies` | calls by signalling proxy and their NG commands since the start (mr8.5+); `enter` opens the calls of one proxy |
| `:keyspaces` | subscribed redis keyspaces (`kslist`) |
| `:cli <command>` | any CLI command as is, for example `:cli list maxsessions` |
| `:describe`, `:info` (`:loglevel`) | version, process, config paths, kernel module, log levels per subsystem (one level before mr9.5; `e` opens the level table: `+`/`-` one step, `enter` types a number, `a` all subsystems), the CLI commands the host has |
| `:theme [name]` | switch the theme, without a name list them |
| `:lang [en\|ru]` | switch the interface language, without an argument show the current one |

## Interface language

The interface is in English and Russian. The language comes from `-lang`, then `language:` in the config, then `LC_ALL`, `LC_MESSAGES` or `LANG`. Without a supported language there, the interface is in English. `:lang ru` and `:lang en` switch it at runtime and redraw the open views at once; the choice is not written to the config.

Everything rtpengine-tui itself writes is translated: hints, help, titles and columns, statuses, the explanations in call details, the config check and the template breakdown. Names that come from rtpengine stay as they are (CLI commands, options, NG flags, `own`/`foreign`, `offer`/`answer`, RTP, SDP): people look them up in logs and documentation. The descriptions of config options are translated too. Each translation keeps the English text it was made from: when `make gen-rtpengine` brings a different one, the English is shown, and `make gen-rtpengine` and the test `TestOptionTranslations` list what to update. The descriptions of NG flags in the template breakdown are in English for now.

Strings in the code are English and wrapped in `T()`/`Tf()`; `N()` marks text translated when drawn (titles, hints). The catalogs of rtpengine-tui are in `internal/rtpengine/locale_ru.go`, the framework's in `tui/locale_*.go`, option descriptions in `internal/rtpengine/options_ru.go`. The tests of `internal/rtpengine` and `tui` parse the sources and fail for each language when a marked string has no translation, when a catalog has an extra key, or when a translation has different `%` verbs. A second check fails when text goes into an error or the status line without `T()`. Alignment is computed in terminal columns, wide characters take two.

A new language: a catalog in `tui` and in `internal/rtpengine` (registered with `i18n.Register`), an option description file in `internal/rtpengine` and a line in `optionTranslations`. The tests show what is missing.

## rtpengine releases

rtpengine-tui finds the version from `version:` in the config, then `rtpengine --version`, then the package version (`dpkg`, `rpm`). The set of CLI commands comes from the `help` reply (there since mr9.5). Older releases use the table below; when the version is unknown, it is estimated from the commands available and shown as `≥mrX.Y`.

CLI commands by release, from `daemon/cli.c` of the sipwise/rtpengine branches mr6.5 … mr13.3 and master:

| Release | Added |
|---|---|
| mr6.5 | `list` numsessions, sessions, totals, counters, max*, *timeout, loglevel, redis*, controltos; `set` for the same; `terminate`; `params`; `ksadd`, `ksrm`, `kslist` |
| mr7.5 | `list interfaces`, `list transcoders` |
| mr8.5 | `list jsonstats`, `active`, `standby` |
| mr9.5 | `list loglevels`, `list/set deletedelay`, `debug`, `help` |
| mr10.5 | `call <id> info\|terminate\|debug\|tag` |
| mr11.5 | `set redisdisable` |
| mr13.2 | `usage`, `media` (builds with transcoding) |

Reply formats changed as well, and the client reads both: the session list `callid: … \| deletionmark:` before mr11.5 and `ID: … \| del: … \| rec:` from mr12.5; `params` with option names (`max-sessions`) before mr8.5 and field names (`max_sessions`, `timeout_us` in microseconds) later; the unknown command message `Unknown or incomplete command` before mr13.1 and `Unknown command` from mr13.2.

A command the host's release lacks is not sent; rtpengine-tui says which release has it: `"list interfaces" needs rtpengine mr7.5+, host runs mr6.5.9.12`.

## Checking rtpengine.conf

The configs are read over SSH (`cat`, through `sudo -n` with `sudo: true`). The path and section come from the command line of the running process (`--config-file`, `--config-section`), else from `configFile`/`recordingConfigFile` in the config, else `/etc/rtpengine/rtpengine.conf` and `/etc/rtpengine/rtpengine-recording.conf`.

Line statuses:

| Status | Means |
|---|---|
| `ok` | the option exists in this release |
| `runtime differs` | the value in the file differs from `params current`: changed with `set`, or not reloaded after an edit |
| `overridden` | the key is set again further down the section, the later one wins |
| `not in version` | an option of another release: `added in mr12.5, host runs mr11.5` or `dropped after …` |
| `unknown` | no release has this option, usually a typo; rtpengine does not start with it |
| `interface`, `templates`, `section` | the interfaces section (`interfaces-config`), the signalling templates (`templates`), or sections the daemon does not read |

The option table (name, description, argument, releases that have it) is generated from `daemon/main.c`, `recording-daemon/main.c` and `lib/auxlib.c`: `make gen-rtpengine` downloads the sources and rebuilds `internal/rtpengine/options_gen.go`. An option counts as available when the nearest branch below or above the host's version has it, so for releases in between (mr10.0, say) a finding may be missed, but none is false.

### Signalling templates

The section named by the `templates` option (mr13.2+) holds templates: NG flag strings in the syntax of `rtpengine_offer()` in Kamailio or OpenSIPS. `enter` on a template line in `:config` splits it into tokens the way the daemon's parser does (`control_ng_flags_parser.c`) and shows for each:

- what it is: a `key=value`, a flag, a replace flag (`replace-origin`, `replace=[...]`), a codec operation (`codec-strip-G729`, `codec=[transcode=[PCMA]]`), a transport shorthand (`RTP`, `SRTP`, `AVPF`, `DTLS`, which add up to the final `transport-protocol`) or a direction (`internal`, `external`, `direction=`);
- the description from `docs/ng_control_protocol.md`, and for values like `ICE=force` or `rtcp-mux=[offer require]` the description of each value;
- errors: an unknown flag (rtpengine logs a warning and ignores it) with the closest name, a difference in case only (names are case-sensitive), a flag of another release.

It also shows how the template applies. A template named after an NG command (`offer`, `answer`, `delete`…) is applied by rtpengine to every such command, and `default` to all messages. The others apply only where the proxy passes `template=<name>`. In the `:config` table a template with errors gets the status `unknown` or `not in version`; on hosts before mr13.2 all templates are `not in version`.

The table of NG keys and flags (`internal/rtpengine/ngflags_gen.go`) is generated by `make gen-rtpengine` too: the names with all aliases come from `call_ng_main_flags`, `call_ng_flags_flags`, `call_ng_flags_replace` and `call_ng_codec_flags` (moved to `daemon/call_flags.c` in master), the descriptions from the `offer` section of the documentation, for the branches mr13.2, mr13.3 and master.

## Emulator

`rtpengine-tui-mock` emulates the listen-cli of a chosen release: commands and reply formats follow the version, calls come and go, and `set`, `terminate`, `debug`, `params` and keyspaces work. With `-confdir` it writes sample rtpengine.conf and rtpengine-recording.conf with deliberate problems (a typo, options of newer releases, a repeated key, values changed at runtime), a log file per host in the layout of journald, and a config for rtpengine-tui that points at all of it.

```sh
./bin/rtpengine-tui-mock -fleet -confdir bin/mock-etc &     # mr6.5, mr9.5, mr11.5, mr13.3 on ports 9901-9904
./bin/rtpengine-tui -config bin/mock-etc/rtpengine-tui.yaml

./bin/rtpengine-tui-mock -listen 127.0.0.1:9900 -version mr12.5.1.3 &
./bin/rtpengine-tui -address local -cli 127.0.0.1:9900
```

`make demo-rtpengine` starts the fleet and prints the second line. The tests run against the emulator (`make test`), the UI tests included, on a virtual screen. `make demo-gif` records the animation above.
