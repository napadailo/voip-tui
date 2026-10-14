# opensips-tui

A terminal interface over the OpenSIPS management interface (MI). It reaches OpenSIPS hosts over SSH and runs the MI commands an operator would run with `opensips-cli -x mi`: dialogs, registrations, statistics, processes and their log levels, dispatcher, drouting gateways, rtpengine sets, clusterer, any MI command by hand. MI goes through opensips-cli, `mi_fifo` or `mi_http`. Every view and action is checked against the OpenSIPS version on the host. Vim keys, themes, English or Russian interface.

Russian version: [opensips-tui.ru.md](opensips-tui.ru.md).

```
Host:    edge-n1 mock-3.4.7     <D> end dialog    <i> opensips-cli
Version: 3.4.7                  <y> JSON          <:> command
MI:      opensips-cli           <d> describe      </> filter
Process: pid 3484 · up 14d14h   <space> mark      <?> help
Rev:     v0.2.0 · default · en  <s> ssh           <esc> back
╔═══════════════════════════════ Dialogs[15] edge-n1 ════════════════════════════════╗
║ ID              STATE      FROM          TO            DURATION  CALL-ID           ║
║ 16621523442200  confirmed  13125550164   13125550121   11m41s    833128383d2d2b8f… ║
║ 6485400622052   early      14155550167   17185550138   -         b0ec403d284721bd… ║
║ 1408749329267   confirmed  14155550199   16175550139   46m18s    83fc8c1e70a9c513… ║
╚════════════════════════════════════════════════════════════════════════════════════╝
```

## Build

Go 1.22+.

```sh
make build            # ./bin/opensips-tui and ./bin/opensips-tui-mock
make test
make install          # into $GOPATH/bin
```

## Connecting

The config is `~/.config/opensips-tui/config.yaml` (or `$OPENSIPS_TUI_CONFIG`). `opensips-tui -sample-config` prints an example:

```yaml
currentHost: edge-n1  # the host to start on, default: the first one
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
```

SSH is the system `ssh`, so `~/.ssh/config` aliases, ProxyJump and the agent work as usual. One master connection per host is kept (ControlMaster, sockets in `/tmp/opensips-tui-<uid>`: a path under the home directory is too long for a unix socket on macOS), so MI calls do not pay for a new handshake. `BatchMode=yes`: without a working key ssh does not ask for a password, and the error shows in the host's line.

Flags: `-host edge-n2` the host to start on, `-c dialogs` the first command, `-theme nord`, `-lang ru`, `-readonly`.

### How MI reaches OpenSIPS: `via`

| `via` | Path | Needed on the host |
|---|---|---|
| `cli` (default) | `opensips-cli -x mi` over ssh | opensips-cli with access to MI |
| `fifo` | over ssh into `mi_fifo`: a reply fifo is made in `replyDir`, `:<reply>:<JSON-RPC>` is written to `fifo` | the `mi_fifo` module; no opensips-cli |
| `http` | JSON-RPC to `mi_http` through an ssh tunnel (`ssh -W`) to the address in `mi`, as seen from the host | the `httpd` and `mi_http` modules |
| `direct` | JSON-RPC to `mi_http` at the address in `mi`, straight from this machine, no ssh | `mi_http` reachable from here |

The defaults are those of the modules: `fifo: /tmp/opensips_fifo`, `replyDir: /tmp/`, `mi: http://127.0.0.1:8888/mi`. With `via: fifo`, `sudo: true` runs the fifo commands through `sudo -n`. Views and commands are the same whatever `via` is, only the delivery differs; the VIA column of the host list and the MI line of the header show what each host uses. `fifo`, `http` and `direct` need OpenSIPS 3.0+ (MI in JSON-RPC); 2.x stays with `cli`, which falls back to `opensipsctl fifo`.

opensips-cli always runs with `-o output_type=pretty-print`: another `output_type` from its config would not parse. When opensips-cli on the host talks to MI over HTTP rather than the fifo, set that in `cliArgs`: `[-o, communication_type=http, -o, url=http://127.0.0.1:8888/mi]`.

## Keys

| Key | Action |
|---|---|
| `j` `k` `g` `G` | down, up, top, bottom |
| `ctrl-d` `ctrl-u`, `ctrl-f` `ctrl-b` | half page, page |
| `H` `M` `L`, `h` `l` | top, middle, bottom of the screen; scroll columns |
| `enter`, `esc` or `q` | open, back |
| `:` | command prompt (completion with `tab` and arrows, with history) |
| `/` | filter rows (regex, a leading `!` inverts); in text views search, then `n` `N` |
| `space` | mark a row: actions apply to all marked rows |
| `o` `O` | sort column, reverse order |
| `d`, `enter` | the row as a card: dialog, contact, dispatcher destination, gateway, host; fields the card does not know are listed under Other |
| `y` | JSON as MI returned it: in a card or `:info` the card switches to JSON and `y` again switches back; in a table the row's JSON opens over it and `y` again closes it |
| `s`, `i` | ssh to the host, interactive opensips-cli on the host (the interface is suspended) |
| `ctrl-r` | refresh |
| `?` | help |
| `:q`, `ctrl-c` | quit |

The host list shows each host's address (the ssh target, or the `mi_http` URL with `via: direct`), `via`, the running version, the status of the probe, active dialogs and labels. The current host is marked `*`: `currentHost` from the config, else the first one. With one host opensips-tui opens its `defaultView` (dialogs) at once, with several it starts on the list. In the list `enter` opens the host's dialogs, as the sessions in rtpengine-tui, `space` makes the host current, `d` opens its card (the same as `:info`), `r` probes it again, `s` and `i` open ssh and opensips-cli. `:<host name>` switches to a host from any view.

The host card (`:info`) has three parts, as `describe` in rtpengine-tui. The line under the name says how the host is reached: the address, `via` and what carries MI. OpenSIPS: the running version, the installed package when it differs, the main process (PID, uptime, command line), the opensips packages, the config file (`-f` of the process) and when the probe ran. Logging: `log_level` of the processes, grouped by level, and `xlog_level`, the level of `xlog()` in the script. MI commands: every MI command opensips-tui uses, whether the host has it and from which release, or why not.

`e` in the card opens the log levels: `log_level` of all processes and `xlog_level`. `+` and `-` change the selected one by a step at once (OpenSIPS has no level 0: after -1 err comes 1 warn), `enter` puts `mi log_level level=` or `mi xlog_level level=` in the prompt. The level of one process is changed in `:processes`. Levels go from -3 (alert) to 4 (debug); a change lasts until OpenSIPS restarts.

Row cards: a dialog shows its state, start and expiry, the caller and each callee (URI, tag, Contact, CSeq, route set, socket, and fields the card does not know), then the SDP of each side: the one OpenSIPS received from it and the one it sent to it (`caller_sdp`, `caller_sent_sdp`, `callee_sdp`, `callee_sent_sdp`). A contact shows its registration (expiry, Call-ID, CSeq, User-Agent, attributes; State, Methods and Flags in words: whether usrloc wrote it to the database, the methods from its Allow, the usrloc flags) and where it came from (received, socket, Path, `+sip.instance`, reg-id). A dispatcher destination and a drouting gateway show their state, weight, priority and partition, and their attributes one `key=value` pair per line.

Everything that changes OpenSIPS first shows a window with the exact command for this host. With `-readonly`, or `readOnly: true` globally or per host, such keys are gone, and `:mi` runs only commands that read: `version`, `uptime`, `which`, `ps`, `get_statistics` and a few more, and commands without parameters that end in `_list`, `_show`, `_dump` or `_status`.

## Commands

| Command | MI | Actions |
|---|---|---|
| `:hosts [name]`, `:host`, `:nodes`, `:ctx` | - | the host list, or switch to a host; the view stays when the new host has it; `:<host name>` switches too |
| `:info`, `:describe`, `:loglevel` | version, uptime, which, log_level, xlog_level | the host card; `e` log_level and xlog_level: `+` `-` a step, `enter` a level |
| `:stats` | get_statistics all | by category, as in rtpengine-tui: SIP messages, transactions, stateless replies, dialogs, registrations, network, shared memory, private memory per process (pkmem), load, and a section for each other group, with the statistics of that module under their own names; counters with the rate between refreshes and the total, growing drops and errors in yellow; `y` JSON and back, `s` save, `a` all statistics as a table and back |
| `:allstats` | get_statistics all | every statistic as a table with its rate; `x` reset, `a` by category |
| `:dialogs`, `:dlg` | dlg_list | `D` end |
| `:contacts`, `:ul` | ul_dump | `D` remove contact, `X` remove AOR |
| `:processes`, `:ps` | ps, log_level | `+` `-` log level of the process |
| `:dispatcher`, `:ds` | ds_list | `T` on/off, `R` ds_reload |
| `:gateways`, `:gw`, `:dr` | dr_reload_status, dr_gw_status | `T` on/off, `R` dr_reload |
| `:rtpengine`, `:rtp` | rtpengine_show | `T` on/off |
| `:clusterer`, `:shtags` | clusterer_list, clusterer_list_shtags | - |
| `:status`, `:sr` | sr_list_status (3.2+) | - |
| `:tcp` | list_tcp_conns | - |
| `:commands` | which | `enter` runs the command without parameters |
| `:mi <command> [k=v …]` | any | state-changing ones ask first |
| `:theme [name]` | - | without a name the list with a preview, `esc` returns the previous one |
| `:lang [en\|ru]` | - | the interface language, without an argument the current one |
| `:refresh <seconds>` | - | the auto-refresh period |

## Interface language

The interface is in English and Russian. The language comes from `-lang`, then `language:` in the config, then `LC_ALL`, `LC_MESSAGES` or `LANG`; without a supported language there it is English. `:lang ru` and `:lang en` switch it at runtime and redraw the open views at once; the choice is not written to the config. What a host probe found (warnings, errors) is written once, in the language of that moment; `r` in the host list probes again.

Everything opensips-tui itself writes is translated. Names that come from OpenSIPS stay as they are: MI commands, modules, parameters, dialog and destination states, SIP header names. The catalog is `internal/opensips/locale_ru.go`; the tests of `internal/opensips` fail when a marked string has no translation, a catalog has an extra key, a translation has different `%` verbs, or text reaches an error or the status line without `T()`.

## OpenSIPS versions

On connecting, opensips-tui probes the host:

1. in one shell call: `opensips -V` (the installed version) and the paths of `opensips-cli` and `opensipsctl`;
2. over MI: `version` (the running version), `which` (the MI commands there are) and `uptime`.

When the running version differs from the installed one, the header and the host list show a yellow "installed X": the package was upgraded and OpenSIPS was not restarted.

Views and actions do not call MI commands directly but operations from `internal/opensips/capabilities.go` (`dialog.end`, `ul.rm_contact`…). An operation has variants by version range: which MI command and parameters run it. For example, `dlg_end_dlg` takes `dialog_id` from 3.0 and the `h_entry h_id` pair in 2.x. Besides the version, an operation is checked against the `which` list: a module that is not loaded has no commands there.

- 3.0 and later: everything through MI in JSON.
- 2.x: MI is text there, and opensips-cli does not work with it. When the host has `opensipsctl`, `:mi <command>` goes through `opensipsctl fifo`.

A view that is not available does not open, and the reason is in the status line: `status/report needs OpenSIPS 3.2+, host runs 3.1.10` or `no MI command ds_list on the host: module not loaded`. The help (`?`) lists at the bottom which views the current host has.

A new difference between versions is a `Variant{Command, Params, Since, Until}` in the operations table.

## Themes

Built in: `default` (also `k9s`), `dracula`, `nord`, `gruvbox`, `solarized-dark`, `solarized-light`, `monokai`, `catppuccin`, `tokyo-night`. Your own go under `themes` in the config: `base` is the built-in theme to start from, the rest are colors by name or `#rrggbb`: `bg`, `fg`, `border`, `title`, `header`, `key`, `keyDesc`, `infoKey`, `infoValue`, `logo`, `selectBg`, `selectFg`, `marked`, `crumbBg`, `crumbFg`, `crumbLast`, `ok`, `warn`, `err`, `dim`, `accent`, `jsonKey`, `jsonString`.

## Emulator

`make demo-opensips` runs `opensips-tui-mock -confdir bin/opensips-mock-etc`. It writes `bin/opensips-mock-etc/opensips-tui.yaml` and prints the command that starts opensips-tui with it. The config has ten hosts: 3.4, 3.4 with a restart pending, 3.6, 3.1 without dispatcher and rtpengine, 3.6 through `mi_fifo` and through `mi_http`, 3.4 with `via: direct`, a host without `mi_http`, 2.4 and an unreachable one.

```sh
make demo-opensips                                   # keep it running
./bin/opensips-tui -config bin/opensips-mock-etc/opensips-tui.yaml
```

Except for `via: direct`, the hosts go through `opensips-tui-mock` itself, which stands in for ssh (`ssh.command` in that config): it takes the same arguments and answers as a host with OpenSIPS. This exercises the real ssh transport: building the command, quoting, exit codes. The target name says what to emulate: `mock-3.1.10` the version, `pkg3.4.9` an installed package newer than the running one, `no<module>` a module that is not loaded (`nohttp`, `nofifo`: `mi_http`, `mi_fifo`), `down` an unreachable host. For the host with `via: direct` the emulator serves `mi_http` itself (127.0.0.1:18888 by default, flag `-listen`), so keep it running. State (dialogs, contacts, gateways) is kept between calls in `$OPENSIPS_TUI_MOCK_STATE`. The numbers, names and addresses it makes up are fictional: 555-01xx numbers, `example.com`, RFC 5737 addresses.

```yaml
ssh:
  command: ./bin/opensips-tui-mock
hosts:
  - name: lab
    ssh: mock-3.1.10-nodispatcher
```

The tests run against the emulator (`make test`), the UI tests included, on a virtual screen.
