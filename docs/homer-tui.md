# homer-tui

A terminal interface over Homer 7 through the homer-app REST API (`/api/v3`), the same API the Homer web interface runs on: search of SIP calls, registrations and logs, a call as a ladder diagram, QoS from RTCP, PCAP and text export, PCAP import, Homer settings. Several Homer instances side by side, vim keys, themes, English or Russian interface.

Russian version: [homer-tui.ru.md](homer-tui.ru.md).

```
Host:  lab http://127.0.0.1:9080  <enter> open call   <p> pcap
User:  admin (admin)              <space> mark        <t> text export
Nodes: localnode                  <v> view: sessions  <s> search form
Auth:  password                   <d> describe        <e> edit query
Rev:   v0.3.0 · default · en      <y> JSON            <r> refresh
╔══════════════════════════════ SIP call(1_call)[17] last 4h ══════════════════════════════╗
║ START                   STATUS         FROM TO  RURI DURATION MSGS SOURCE          DEST… ║
║ 2026-10-21 05:44:16.615 200 OK         110  214 214  41s      14   10.10.0.13:5060 SBC   ║
║ 2026-10-21 05:44:36.496 200 OK         101  210 210  10s      14   10.10.0.12:5060 SBC   ║
║ 2026-10-21 05:46:36.253 100 (no final) 108  212 212           3    10.10.0.14:5060 SBC   ║
╚══════════════════════════════════════════════════════════════════════════════════════════╝
```

## What it shows beyond the web interface

### A call that comes back, unfolded

A ladder with one column per host, as the web interface of Homer draws it, sends a call that comes back through the same hosts back and forth over the same columns. A PBX that forwards a call out through the SBC it came from is a common case: the second INVITE goes left, its answers go right, and both legs mix on the same lanes. homer-tui unfolds such a call: a host the call comes back to is drawn again further right, so the call reads left to right in the order it went, and every request goes right. The same goes for a B2BUA with an outside and an inside address, and a PBX that calls two parties and bridges them stands between its two calls. The title says `unfolded`; `u` draws each host once, `flow: {unfold: false}` in the config makes that the default. The rules are under [Keys](#keys).

Each host once, as in the web interface:

```
flow[28]
 10.10.0.14:5060        SBC                PBX           203.0.113.10:5060
      │───INVITE───────▶│                  │                  │
      │                 │───INVITE────────▶│                  │
      │                 │◀───INVITE────────│                  │
      │                 │──────────────INVITE────────────────▶│
      │                 │◀────────────180 Ringing─────────────│
      │                 │────180 Ringing──▶│                  │
      │                 │◀───180 Ringing───│                  │
      │◀──180 Ringing───│                  │                  │
```

Unfolded:

```
flow[28] unfolded
 10.10.0.14:5060        SBC                PBX                SBC          203.0.113.10:5060
      │───INVITE───────▶│                  │                  │                  │
      │                 │───INVITE────────▶│                  │                  │
      │                 │                  │───INVITE────────▶│                  │
      │                 │                  │                  │───INVITE────────▶│
      │                 │                  │                  │◀──180 Ringing────│
      │                 │                  │◀──180 Ringing────│                  │
      │                 │◀──180 Ringing────│                  │                  │
      │◀──180 Ringing───│                  │                  │                  │
```

### RTCP as the media path

The web interface shows RTCP as charts of each value by stream. On the QoS tab of a call `v` in homer-tui shows the media path instead: the hosts of the media in the order of the call, built from the offers and answers in SDP, and on each hop, for each way of the sound, the MOS and the worst loss its receiver reported, coloured good, fair or poor. A hop nobody reports on is drawn with no data, and a proxy the media does not pass, the SBC below, has the arrow go over it. Under the scheme a summary says in words what each side hears and where the trouble starts: for each way the first hop where the sound goes bad, as what breaks there carries on to the hops after it. Over time draws the MOS of each hop along the call. `flow: {qosGraphs: true}` in the config opens the tab this way.

```
QoS graphs
                    10.10.0.14              SBC                 PBX
                                       192.168.10.1        192.168.10.20
callee hears            │───────────── ● 3.6 8.6% ─────────────▶│
caller hears            │◀──────────── ● 4.0 5.5% ──────────────│

Summary
  caller hears  ● poor  MOS 4.0, loss 5.5% (7), jitter 2.2/3.5 ms  reported by 10.10.0.14
  callee hears  ● poor  MOS 3.6, loss 8.6% (11), jitter 1.8/3.0 ms  reported by PBX
  trouble       → loss up to 8.6% from 07:31:38, first on hop 1 (10.10.0.14 → PBX)
                ← loss up to 5.5% from 07:31:13, first on hop 1 (PBX → 10.10.0.14)

Over time  MOS of each reported hop along the call: green good, yellow fair, red poor
                      07:31:13                                  07:31:43
  1 10.10.0.14 → PBX  ──────────────────────────────────────────  loss 8.6%  jitter 1.8/3.0  RTT 7
  1 PBX → 10.10.0.14  ──────────────────────────────────────────  loss 5.5%  jitter 2.2/3.5  RTT 7
```

## Build

Go 1.22+.

```sh
make build            # ./bin/homer-tui and ./bin/homer-tui-mock
make test
make install          # into $GOPATH/bin
```

## Connecting

The config is `~/.config/homer-tui/config.yaml` (or `$HOMER_TUI_CONFIG`). `homer-tui -sample-config` prints an example:

```yaml
currentHost: prod          # the host to start on, default: the first one
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
```

Without a config: `HOMER_PASSWORD=... homer-tui -url http://homer:9080 -user admin`, or `HOMER_URL`, `HOMER_USER`, `HOMER_TOKEN`. `-password` and `-token` work too, but other users of the machine see them in `ps`; the environment and `passwordCmd`/`tokenCmd` they do not. Such a host is added as `cli` next to the configured ones.

Signing in: with a username and a password homer-tui gets a JWT (`POST /auth`) and logs in again when it expires. `passwordCmd` runs a command for the password, a keychain entry for example. Instead of a password a host can have a `token`: it goes in the `Auth-Token` header (`tokenHeader` changes the name) and works only when homer-app has `api_settings.enable_token_access`; `tokenCmd` gets it with a command, as `passwordCmd` does the password. homer-app gives tokens user rights, so settings changes need a username and a password of an administrator. A token the server refuses (HTTP 401) has expired, been deleted, used up its `limit_calls`, or token access is off; `:tokens` under an administrator shows EXPIRES, CALLS and LIMIT and creates a new one, its secret is shown once.

Credentials go to the configured host only: a redirect to another host, or from https down to http, is refused with an error, as Go would carry the `Auth-Token` header and the password of `POST /auth` on. A `url` with a user or a password in it is refused, the url being on the screen: they go in `username` and `password`. A config others may read with a `password` or a `token` in it is told in the header (`Auth:` … `chmod 600`). PCAP and text exports, which hold numbers and SIP, are saved for their owner only (mode 600) and never over a file already there: a second one gets `-1`.

Every host logs in in the background when homer-tui starts. A host that refuses the login or does not answer says why in the host list, and the others work. A view of a host that has not answered yet shows "connecting to …" and opens once it has.

Flags: `-host prod` the host to start on, `-c 'calls from_user=100 since=2h'` the first command, `-theme nord`, `-lang ru`, `-download-dir`, and `-url`, `-user`, `-password`, `-token`, `-insecure` for a host without a config.

## Hosts

The header shows the current host and its URL, the user and role, the database nodes, how homer-tui signs in, and the build. homer-app does not tell its version through the API, so unlike rtpengine-tui and opensips-tui there is no version line.

`:hosts` lists the hosts: address, how homer-tui signs in, the user and role, the status of the login, the database nodes and the labels. The current host is marked `*`: `currentHost` from the config, else the first one. With one host homer-tui opens its calls at once, with several it starts on the list. In the list `enter` opens the calls of the host, `space` makes it current, `d` opens its card, `r` logs in again. `:<host name>` switches to a host from any view and opens the same search, the same settings or the card there.

The host card (`:info`): the URL, API prefix, user and role, whether the user is an administrator, TLS, the time zone; the database nodes; the modules (`/modules/status`); the auth types (`/auth/type/list`); the search profiles from the mappings. `y` switches it to JSON and back.

## Keys

| Key | Action |
|---|---|
| `j` `k` `g` `G` | down, up, top, bottom |
| `ctrl-d` `ctrl-u`, `ctrl-f` `ctrl-b` | half page, page |
| `H` `M` `L`, `h` `l` | top, middle, bottom of the screen; scroll columns |
| `enter`, `esc` or `q` | open, back |
| `:` | command prompt (completion with `tab` and arrows, with history) |
| `/` | filter rows (regex, a leading `!` inverts); in text views search, then `n` `N` |
| `space` | mark a row: the call, PCAP and export work on marked rows |
| `o` `O` | sort column, reverse order |
| `ctrl-r` | refresh |
| `?` | help |
| `:q`, `ctrl-c` | quit |

Search results: `enter` opens the call of the marked or current rows, `v` switches between sessions and single messages, `d` the card of the row (a session with its status, duration and messages, or one message with its fields), `y` the row as JSON (`y` again closes it), `s` the search form, `e` the query in the prompt, `[` `]` move the time window, `a` auto-refresh, `p` PCAP, `t` text.

Call view: `1`-`4` or `tab` switch the tabs Flow (the ladder), Messages, QoS, Logs. `enter` shows the whole message, `z` hides the message pane, `h` `l` or the arrows scroll the flow by one host when the hosts do not fit (the title shows which are shown), `0` `$` to the first and the last, `p` `t` export PCAP and text, `y` the message as JSON (`y` again closes it).

A line under the tabs, set off from them by a rule, says what became of the call, by the leg of its first INVITE as the caller saw it: the final answer in its colour (or the last provisional one, or no answer), then badges, a label in its colour and the value in white on it:

| Badge | Meaning |
|---|---|
| `RNG` | ringing after: from the INVITE to the first 180 or 183 |
| `ASW` | answered after: from the INVITE to the 200 |
| `RBT` | how long it rang, for a call cancelled before an answer |
| `DUR` | the talk, from the answer to the BYE; with `…` while `F` follows a call going on |
| `BYE` | who hung up, caller or callee; `none` when an answered call has no BYE |
| `CXL` | who cancelled |
| `RSN` | the Reason of the BYE or the CANCEL, the Q.850 cause by name |
| `LEG` | the SIP legs of the call |

Then who called whom. Through a proxy it reads the answers the caller got; a Reason a proxy left out is taken from the other hops of the request.

```
 200 OK RNG 1.07s ASW 3.09s DUR 1m3s BYE caller LEG 2 114 → 217
 487 Cancelled RNG 840ms RBT 4.84s CXL caller LEG 1 102 → 201
 486 Busy LEG 1 102 → 216
```

The `TX` column beside the flow shows SIP transactions: a bracket from the first message of a transaction to its last (`┌` `├` `└`, `╶` for a transaction of one message), in turns of colours. A transaction is a session and a CSeq, so the hops of one request through a proxy are one transaction, and an ACK to a failed INVITE belongs to the INVITE; an ACK to a 2xx is a transaction of its own. Transactions open at the same time, a PRACK or a CANCEL while the INVITE waits for its answer, take columns side by side.

`v` shows a SIP message in plain words instead of as it is, and back: the parties (request URI, From, To, P-Asserted-Identity, Diversion and the like), the way from the first hop (Via and Record-Route with the aliases of Homer), the media of the SDP with codecs by name and a held stream marked, the session rules (Session-Expires, Require, Supported, Allow), and how it ended: the answer with what it usually means, Reason with the Q.850 cause by name, Warning. The choice stays for the next calls until homer-tui quits; `enter` opens the message whole in the same view, and `v` toggles there too. `m` moves the focus into the message pane to scroll it (`j` `k`, `g` `G`, `ctrl-d` `ctrl-u`, `ctrl-f` `ctrl-b`, `v` there too); `m`, `esc` or `q` go back to the list.

On the QoS tab `v` shows the RTCP reports as the media path instead of a table. On top is a scheme like the flow: the hosts of the media in the order of the call, the caller on the left, named by the host that announced their addresses in SDP; a relay with two addresses, such as rtpengine behind an SBC, is one column. The IPs of a column are under its name, both for a relay, so its inside and outside addresses tell apart. Every SIP host of the call is a column, whether it sends RTCP or not, and the hops come from the offers and answers in SDP, so a hop nobody reports on is drawn with no data and a proxy the media does not pass has the arrow go over it. The columns are the lanes of the flow by alias and unfolded: a call that comes back through the same hosts, forwarded by a PBX for one, has its media on columns of its own further right, and its sound to the callee goes right too. The row "callee hears" is the sound towards the callee, the row "caller hears" the sound back; each arrow is a hop and says what its receiver reported: MOS and the worst loss, coloured good, fair or poor, or no data when the receiver sends no reports, as rtpengine usually does not. All SSRCs of a way are one arrow, a re-INVITE does not split it. Summary says in words what each side hears, by the report of the hop next to it or the nearest one, and the trouble: for each way the first hop on the way of the sound where it goes bad, as what breaks there carries on to the hops after it, or that none is seen and which hops sent no reports. Over time draws the MOS of each reported hop along the call as a thin line coloured from green through yellow to red, · where reports are missing, with its numbers on the right. The message pane under it lists the hops with their addresses and explains the view.

On the Logs tab `w` shows the logs whole, each with its time and addresses and the message wrapped to the width of the screen, instead of a table that cuts the message; `w` again brings the table back.

`F` follows a call still going on: it is loaded again on every tick (`refreshSeconds`) with its window growing to now, the selection stays on its message or, on the last one, goes on to the new ones, and the title says `live`. Following stops by itself once a BYE is answered or the INVITE failed, and says so; `F` again stops it too.

homer-app puts SIP, RTCP and HEP logs of a call in one flow. `f` chooses which of them to show, with the count of each (SIP, RTCP and LOG are always listed, with 0 when the call has none): `space` or `enter` turns a line on and off, `esc` goes back to the call. A lane of the flow is one IP and port; `a` puts all the addresses of an alias in one lane, and the second line of the header then says how many there are (`f` shows how many lanes are left).

A call with correlated legs can come back: A calls B, B calls C, then C calls B and B calls A. homer-tui unfolds such a call: a leg is the messages of one session between two hosts, its first request goes left to right, and when that request goes to a host that is only left of the sender, the host is drawn again on the right. The call then reads A, B, C, B, A from left to right, and the INVITE from C to B goes right instead of back to the left; the title says `unfolded`. A device with two addresses under one alias, a B2BUA with an outside and an inside interface for one, unfolds on both: when the call comes back on one address and leaves on the other, the other is drawn again too. A PBX that calls two parties and bridges them, an Asterisk dialling one channel and then another, stands between its calls: the first goes to the right, the second, which the leftmost host starts after the first, to the left, and what follows it goes on to the left. A leg keeps the direction of its first request even when an answer was captured a little earlier by another agent. Answers and requests inside a leg use its lanes, RTCP and logs the rightmost lanes of their hosts. A call that does not come back looks the same either way. `u` draws each host once instead.

The filter, `a` and `u` apply to the Flow and Messages tabs and stay for the next calls until homer-tui quits; they are not written to the config. A start opens calls the way `flow:` in the config says (`hide`, `byAlias`, `unfold`, see the example above), without it with every message, by address, unfolded. `qosGraphs: true` there opens the QoS tab as graphs; `v` switches it to the table and back, and that too stays until homer-tui quits.

Settings: `d` and `enter` the card of the item, `y` its JSON (`y` again closes it), `e` edit in `$EDITOR` (in this order: `HOMER_TUI_EDITOR`, `VISUAL`, `EDITOR`, `vi`), `c` create from a template, `shift-d` delete after a confirmation. The editor must wait until the file is closed: one that comes back at once (`code` without `--wait`, for one) saves nothing, and homer-tui says so; a template saved as it is is created after a question. For aliases `shift-t` turns an alias on and off, for mappings `x` resets the mapping to the built-in one.

### Mappings

A mapping (`:mappings`, the table `mapping_schema` of homer-app) tells homer-app about one kind of message: its key `hepid_profile` (`1_call`) names the table the messages lie in (`hep_proto_1_call`), its fields say what a search may use, and its correlation says what a call found there is assembled with. The list says it short: what the messages are, the payload and the profile (`SIP · call`, `LOG · default`, `RTP · default`), how many search fields there are, FLOW: what the flow of a call found there is built with (`LOG`, `RTCP`, `RTP`, `SIP`, `LEG` for the other legs), whether homer-app searches the profile, and how many problems homer-tui found. `enter` or `d` opens the card of a mapping (`:search 1_call …` searches its profile):

- What it is: the payload (SIP, RTCP, RTP, LOG), the profile as the capture server sorts the messages (call, registration, default), the table, and whether homer-app searches it: only with `partid` 10.
- Search fields: the name to type in a search (`from_user=100`) and the field it stands for (`data_header.from_user`). `sid`, `raw`, `id` and `create_date` are searchable in every profile; any other field not listed is dropped from a search without a word.
- Flow: what the flow of a call found here is made of, block by block as the builder (`b`) has it: first the call's own messages, then a line for each rule as `s` in the builder writes them, the legs block first, with what it adds in the words of the list (`LOG` logs with this Call-ID, `RTCP` RTCP reports with this Call-ID, `RTP`, `SIP` the SIP with this Call-ID, `LEG` another leg of the call, and how it is found), the mapping it looks in, `append_sid` on the rule that passes the session ids it finds on, and `✗` with the reason where homer-app skips the rule, crashes or finds nothing as it keeps it now. On the left are the numbers of the rules homer-app keeps for the line, the ones the problems on top name; two numbers are one rule twice, such as logs looked for before and again after an `append_sid` rule, which runs once here. When the builder would write the rules otherwise, the card says how many. A rule no block stands for says which field it matches with which. Under the lines, how far before and after the search window the rules look, when they all look the same. The exact JSON is under `y`.

- Stored only: `retention`, `partition_step`, `version`, `hep_alias`. homer-app does not read them; the capture server may have settings of its own.
- Problems, on top: what homer-app skips, misreads or crashes on. A `partid` other than 10; a `_` in the profile, which breaks the call assembly as homer-app cuts the key at the first `_`; characters it does not accept in a key; two mappings with one key; fields it refuses (it takes `data_header.name` or `protocol_header.name` only); rules without `source_field`, `lookup_id` or two numbers in `lookup_range`, on which homer-app crashes; `lookup_id` 0, which it skips; an empty `input_function_js`, which homer-app runs and crashes on (leave the key out; the builder never writes empty keys); a `source_field` the profile does not have, with the name its table has it under (`correlation_id` is `protocol_header.correlation_id`, not `data_header`); a `lookup_field` written as `data_header.callid`, which is not SQL and finds nothing (the built-in `100_default` has it: write `data_header->>'callid'`); a rule repeated with no `append_sid` rule between, which would pass new session ids on.

`b` builds the call assembly of a mapping from blocks instead of JSON. The blocks are what rules are made for, named as the flow names them, and the list says for each the profile it looks in, MATCH, what of the call it takes, LOOKUP, the field it looks that up in there, and the window:

```
ON  BLOCK  PROFILE  MATCH             LOOKUP          WINDOW
✓   LOG    default  Call-ID           sid             5m before, 3m20s after
✓   RTCP   default  Call-ID           sid             5m before, 3m20s after
✓   LEG    call     correlation_id    Call-ID         5m before, 3m20s after
✓   LEG    call     Call-ID+"_b2b-1"  Call-ID         5m before, 3m20s after
```

`append_sid` after the lookup marks the block whose rule passes the session ids it finds on to the rules after it, so that they look for those sessions too. `LOG` and `RTCP` bring the logs and the RTCP of the call's Call-ID; `RTP` the RTP agent reports, by the Call-ID or by the IP of the messages; `SIP` the SIP of a profile with the same Call-ID, for a log or a report its call; `LEG` another leg: by `correlation_id` to `correlation_id` the other legs of a B2BUA with the same `correlation_id`, with all their messages, and every other block runs for them too; by `correlation_id` to `Call-ID` the leg whose Call-ID is this call's `correlation_id` (X-CID); by `Call-ID+"suffix"` the leg whose Call-ID is this one's and a suffix, such as Asterisk's `_b2b-1`. A custom block is a rule kept as it is and edited as JSON. The blocks are the same for every mapping; each takes the Call-ID from where the mapping's table keeps it, `data_header.callid` in SIP tables and the session id `sid` in the tables of logs, RTCP and RTP reports. `space` turns a block on and off, `enter` or `e` sets it up (the window around the search, the profile it looks in, the suffix), `a` adds one once it is set up (the list of blocks shows the whole help of the one selected under it) (`esc` in the setup goes back to the list of blocks and adds nothing), `shift-d` deletes after a question, `shift-j`/`shift-k` move (the legs block by `correlation_id` stays first: the session ids it finds go to the blocks after it); under the blocks homer-tui says how a call will be assembled and what homer-app would trip on, and `s` saves after a question that counts the rules before and after. `esc` asks before it leaves only blocks changed here; when the blocks of a mapping as it is write its rules otherwise (repeats folded, the legs first, a broken `lookup_field` mended), the line under them says so and `s` writes them so. Reading a mapping, the builder makes rules repeated after a legs rule one block and writes the legs rule first, so a mapping such as one with nine rules for logs, RTCP and legs twice saves as six that bring the same; `100_default`'s broken `data_header.callid` comes back as `data_header->>'callid'`.

homer-app reads mappings on every search, so a change applies at once. Its update writes only the fields that are not empty or zero, so `e` sends the whole mapping.

## Commands

| Command | What it does |
|---|---|
| `:calls [filters]` | search calls, profile `1_call` |
| `:reg`, `:sip`, `:logs` | profiles `1_registration`, `1_default`, `100_default` |
| `:search <hepid_profile> [filters]` | any profile of the mappings, for example `:search 5_default`; a profile name alone works too |
| `:aliases` `:users` `:tokens` `:mappings` `:hepsub` `:advanced` `:usersettings` `:agents` `:dashboards` `:nodes` | Homer settings |
| `:hosts [name]`, `:host`, `:ctx` | the host list, or switch to a host; `:<host name>` switches too |
| `:info`, `:describe` | the host card |
| `:import <file.pcap>`, `:import-now <file.pcap>` | upload a PCAP into Homer, with its own times or shifted to now |
| `:theme [name]` | switch the theme, without a name list them |
| `:lang [en\|ru]` | switch the interface language, without an argument show the current one |

## Search syntax

```
:calls from_user=100 to_user=200 since=2h limit=500
:calls callid=abc@host                  a field by its id, short name or title
:calls method=INVITE;BYE                ; - any of the values
:calls from_user=10%                    % - LIKE
:calls user_agent!=friendly-scanner     != - NOT
:calls from="2026-10-06 10:00" to=11:30 an absolute window; also -3h, RFC3339, unix
:reg node=node1,node2 or                database nodes, OR between the conditions
:calls since=1d where data_header.from_user = '100' AND data_header.method = 'INVITE'
```

The search form (`s`) has the profile, the window and a few fields of the profile: From user, To user, CallID and RURI user for calls; From user, CallID, Source IP and User Agent for registrations; for other profiles the first three fields their mapping does not hide. The window is a duration back from now or a range: `1h`, `15:00..15:30`, `-3h..-1h`, `2026-10-21 16:00..` (to now). `enter` in any field or `ctrl-s` searches, `ctrl-c` clears the values (the fields stay). `ctrl-a` adds one of the other fields of the profile, or an option: Limit, Nodes, OR logic, Where (smart); `ctrl-d` takes the field out with its value. The fields stay so for the profile until homer-tui quits; `searchFields` in the config sets them from the start (a field by its id, short name or title, as in a query), and a name the mapping lacks is reported. A field with a value is always in the form, so a query with `ua=` opens it with User Agent.

homer-app searches without an order: when a window holds more messages than `limit`, it returns the oldest of them, the first minutes of "the last hour". homer-tui therefore reads the window from its end back, a part at a time, as the table scrolls: the first part is the latest stretch of the window that `limit` holds whole, and when the cursor comes near the last rows, by `j`, `ctrl-d` or `G`, the stretch before it is read and its rows added, the cursor staying where it is. The rows fill the screen at once; the title says how far back they go while there is more (`back to 16:47:05, more below`). homer-tui finds the length of a stretch by a few searches, halving it between one the limit holds and one it does not, so that a part has at least half of `limit` where the window has as much. A part is whole sessions: those with messages in its stretch that no later part has, each with all its messages in the window, so its status, duration and message count are right even when the session reaches past the stretch. A session is so in the part of its latest activity, and in the table once. Those messages come by a second search for the session ids; with filters it keeps them, as a part that holds the whole session does. The rows are sorted by START (DATE for single messages), the latest first, `o` and `O` change it; a call that began earlier and ended later than those read comes in among them, where its START puts it. So it goes in every search, `:calls`, `:registrations`, `:logs` and any profile: a session is the session id, the Call-ID for SIP; a row with none, such as a log of no call, stands alone. `r`, auto-refresh, `[` `]`, the search form and a new query read the window again from its end.

The status of a session is the answer to its request: the first final answer for a call, the last for a registration, which renews itself. A 401 or 407 asks for credentials and the request comes again, so it is the status only when nothing else answered; answers to other requests (BYE, PRACK) do not count.

The fields come from the `fields_mapping` of the profile (`:mappings`, then `f`). homer-app drops fields that are not in the mapping without a word, so homer-tui warns about unknown ones.

## Interface language

The interface is in English and Russian. The language comes from `-lang`, then `language:` in the config, then `LC_ALL`, `LC_MESSAGES` or `LANG`; without a supported language there it is English. `:lang ru` and `:lang en` switch it at runtime and redraw the open views; the choice is not written to the config.

Everything homer-tui itself writes is translated. Names that come from Homer or SIP stay as they are: API paths, profiles such as `1_call`, mapping fields, SIP methods, headers and reason phrases, values from the database. The catalog is `internal/homer/locale_ru.go`; the tests of `internal/homer` fail when a marked string has no translation, a catalog has an extra key, a translation has different `%` verbs, or text reaches an error or the status line without `T()`.

## What the homer-app API offers

From the sources of [sipcapture/homer-app](https://github.com/sipcapture/homer-app) (`router/v1`, `controller/v1`, `data/service`). All paths start with `/api/v3`.

| Group | Endpoints | In homer-tui |
|---|---|---|
| Sign-in | `POST /auth`, `GET /auth/type/list`, OAuth2 `/oauth2/*` | login, `:info` |
| Search | `POST /search/call/data`, `/search/call/message`, `/search/call/decode/message` | `:calls` and the others |
| Call | `POST /call/transaction`, `/call/report/qos`, `/call/report/log` | call view |
| Export and import | `POST /export/call/messages/pcap`, `/export/call/messages/text`, `/import/data/pcap[/now]` | `p`, `t`, `:import` |
| Mappings | `/mapping/protocol` (CRUD), `/mapping/protocol/:hepid/:profile`, `/mapping/protocol/reset[/:guid]`, `/smart/search/tag/:hepid/:profile` | `:mappings` |
| Settings | `/alias`, `/users`, `/token/auth`, `/advanced`, `/hepsub/protocol`, `/user/settings`, `/agent/subscribe`, `/dashboard/*` | the commands of the same names |
| Service | `/users/profile`, `/database/node/list`, `/modules/status`, `/admin/profiles` | header, host list, `:info` |
| Proxies | `/prometheus/*`, `/search/remote/*` (Loki), `/proxy/grafana/*`, `/statistic/*` (InfluxDB) | no |
| Live stream | `GET /ws` (websocket, HEP relay) | no |

What the client takes into account:

- `param.location.node` must carry the database nodes (`value` of `/database/node/list`), or the search returns nothing.
- `timestamp.from/to` are in milliseconds.
- The search returns single messages, not calls; homer-tui groups them by Call-ID.
- Changing aliases, mappings, hepsub, global settings, tokens, and creating or deleting users needs an administrator.
- The secret of a new API token is shown once, in the answer to its creation.
- PCAP import works only when the server has `decoder_shark` enabled and tshark installed; otherwise every file gets "bad pcap data".
- There is no version of homer-app in the API.

## Emulator

`homer-tui-mock` emulates homer-app with generated traffic: calls through an SBC and a PBX with different outcomes, registrations, RTCP, HEP logs, a real PCAP on export, settings with create, update and delete. homer-tui can be tried without Homer, and the tests run against it.

```sh
make demo-homer                                      # keep it running
./bin/homer-tui -config bin/homer-mock-etc/homer-tui.yaml
```

`make demo-homer` runs `homer-tui-mock -listen 127.0.0.1:9080 -confdir bin/homer-mock-etc`. It writes a config with four hosts on the emulator: `lab` logs in with a password, `lab-token` with an API token (settings changes are refused, as by homer-app), `wrong-password` fails the login, `down` does not answer. The emulator prints the command that starts homer-tui with that config. Everything it generates is fictional: numbers, `example.com`, private addresses.

The tests run against the emulator (`make test`), the UI tests included, on a virtual screen.

Unofficial tool, not affiliated with QXIP or sipcapture, the authors of Homer.
