# opensips-tui

Терминальный интерфейс к интерфейсу управления OpenSIPS (MI). Ходит на хосты OpenSIPS по SSH и выполняет те же MI-команды, что оператор выполнил бы через `opensips-cli -x mi`: диалоги, регистрации, статистика, процессы и их уровни логов, dispatcher, шлюзы drouting, наборы rtpengine, clusterer, любая MI-команда вручную. MI доходит через opensips-cli, `mi_fifo` или `mi_http`. Каждый экран и каждое действие проверяются по версии OpenSIPS на хосте. Клавиши vim, темы, интерфейс на английском или русском.

English version: [opensips-tui.md](opensips-tui.md).

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

## Сборка

Нужен Go 1.22+.

```sh
make build            # ./bin/opensips-tui и ./bin/opensips-tui-mock
make test
make install          # в $GOPATH/bin
```

## Подключение

Конфиг лежит в `~/.config/opensips-tui/config.yaml` (или `$OPENSIPS_TUI_CONFIG`). Пример печатает `opensips-tui -sample-config`:

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

ssh берётся системный, поэтому работают `~/.ssh/config`, ProxyJump и агент. На хост держится одно соединение (ControlMaster, сокеты в `/tmp/opensips-tui-<uid>`: путь в домашнем каталоге на macOS слишком длинный для unix-сокета), и вызовы MI не платят за новое рукопожатие. `BatchMode=yes`: без рабочего ключа ssh не спрашивает пароль, ошибка сразу видна в строке хоста.

Флаги: `-host edge-n2` - стартовый хост, `-c dialogs` - стартовая команда, `-theme nord`, `-lang ru`, `-readonly`.

### Как MI доходит до OpenSIPS: `via`

| `via` | Путь | Что нужно на хосте |
|---|---|---|
| `cli` (по умолчанию) | `opensips-cli -x mi` по ssh | opensips-cli и его доступ к MI |
| `fifo` | по ssh в `mi_fifo`: создаётся fifo для ответа в `replyDir`, в `fifo` пишется `:<ответ>:<JSON-RPC>` | модуль `mi_fifo`; opensips-cli не нужен |
| `http` | JSON-RPC в `mi_http` через ssh-туннель (`ssh -W`) на адрес из `mi`, как он виден с хоста | модули `httpd` и `mi_http` |
| `direct` | JSON-RPC в `mi_http` по адресу из `mi` прямо отсюда, без ssh | `mi_http`, доступный с этой машины |

Умолчания совпадают с умолчаниями модулей: `fifo: /tmp/opensips_fifo`, `replyDir: /tmp/`, `mi: http://127.0.0.1:8888/mi`. `sudo: true` с `via: fifo` выполняет команды fifo через `sudo -n`. Экраны и команды одни и те же при любом `via`, разница только в доставке; колонка VIA в списке хостов и строка MI в шапке показывают, чем идёт каждый. `fifo`, `http` и `direct` работают с OpenSIPS 3.0+ (MI в JSON-RPC); для 2.x остаётся `cli` с переходом на `opensipsctl fifo`.

opensips-cli всегда вызывается с `-o output_type=pretty-print`: другой `output_type` из его конфига opensips-tui не разобрал бы. Если opensips-cli на хосте ходит в MI по HTTP, а не через fifo, это задаётся в `cliArgs`: `[-o, communication_type=http, -o, url=http://127.0.0.1:8888/mi]`.

## Клавиши

| Клавиша | Действие |
|---|---|
| `j` `k` `g` `G` | вниз, вверх, в начало, в конец |
| `ctrl-d` `ctrl-u`, `ctrl-f` `ctrl-b` | полстраницы, страница |
| `H` `M` `L`, `h` `l` | верх, середина, низ экрана; прокрутка колонок |
| `enter`, `esc` или `q` | провалиться внутрь, вернуться назад |
| `:` | командная строка (дополнение по `tab` и стрелкам, с историей) |
| `/` | фильтр строк (regex, `!` в начале инвертирует); в тексте - поиск, затем `n` `N` |
| `space` | отметить строку: действия применяются ко всем отмеченным |
| `o` `O` | колонка сортировки, обратный порядок |
| `d`, `enter` | карточка строки: диалог, контакт, назначение dispatcher, шлюз, хост; поля, которых карточка не знает, внизу в «Прочее» |
| `y` | JSON, как его вернул MI: в карточке и `:info` карточка сменяется на JSON, повторный `y` возвращает её; в таблице JSON строки открывается поверх, повторный `y` закрывает его |
| `s`, `i` | ssh на хост, интерактивный opensips-cli на хосте (интерфейс приостанавливается) |
| `ctrl-r` | обновить |
| `?` | справка |
| `:q`, `ctrl-c` | выход |

В списке хостов видно адрес хоста (ssh-цель, а при `via: direct` - URL `mi_http`), `via`, запущенную версию, статус опроса, число активных диалогов и метки. Текущий хост отмечен `*`: это `currentHost` из конфига, а без него первый в списке. С одним хостом opensips-tui сразу открывает его `defaultView` (диалоги), с несколькими - начинает со списка. В списке `enter` открывает диалоги хоста, как сессии в rtpengine-tui, `space` делает хост текущим, `d` открывает его карточку (то же, что `:info`), `r` опрашивает заново, `s` и `i` открывают ssh и opensips-cli. `:<имя хоста>` переключает на хост с любого экрана.

Карточка хоста (`:info`) состоит из трёх частей, как `describe` в rtpengine-tui. Строка под именем говорит, как хост подключается: адрес, `via` и чем идёт MI. OpenSIPS: запущенная версия, установленный пакет, если он отличается, главный процесс (PID, аптайм, командная строка), пакеты opensips, файл конфига (`-f` процесса) и когда был опрос. Логирование: `log_level` процессов, сгруппированный по уровням, и `xlog_level` - уровень `xlog()` в скрипте. MI-команды: все MI-команды, которые использует opensips-tui: есть ли на хосте, с какого релиза, а если нет - почему.

`e` в карточке открывает уровни логов: `log_level` всех процессов и `xlog_level`. `+` и `-` сразу меняют выбранный на шаг (уровня 0 в OpenSIPS нет: после -1 err идёт 1 warn), `enter` подставляет в командную строку `mi log_level level=` или `mi xlog_level level=`. Уровень одного процесса меняется в `:processes`. Уровни от -3 (alert) до 4 (debug); изменение действует до перезапуска OpenSIPS.

Карточки строк: у диалога - состояние, начало и истечение, вызывающая сторона и каждая вызываемая (URI, tag, Contact, CSeq, route set, сокет и поля, которых карточка не знает), затем SDP каждой стороны: принятый OpenSIPS от неё и отправленный ей (`caller_sdp`, `caller_sent_sdp`, `callee_sdp`, `callee_sent_sdp`). У контакта - регистрация (срок, Call-ID, CSeq, User-Agent, атрибуты; State, Methods и Flags словами: записал ли usrloc контакт в базу, методы из его Allow, флаги usrloc) и откуда она пришла (received, сокет, Path, `+sip.instance`, reg-id). У назначения dispatcher и шлюза drouting - состояние, вес, приоритет и раздел, а атрибуты по одной паре `ключ=значение` на строку.

Всё, что меняет OpenSIPS, сначала показывает окно с точной командой для этого хоста. С `-readonly` или `readOnly: true` (для всех хостов или для одного) таких клавиш нет, а `:mi` выполняет только читающие команды: `version`, `uptime`, `which`, `ps`, `get_statistics` и ещё несколько, а также команды без параметров, которые оканчиваются на `_list`, `_show`, `_dump` или `_status`.

## Команды

| Команда | MI | Действия |
|---|---|---|
| `:hosts [имя]`, `:host`, `:nodes`, `:ctx` | - | список хостов или переключение; экран сохраняется, если он есть на новом хосте; `:<имя хоста>` тоже переключает |
| `:info`, `:describe`, `:loglevel` | version, uptime, which, log_level, xlog_level | карточка хоста; `e` - log_level и xlog_level: `+` `-` на шаг, `enter` - уровень |
| `:stats` | get_statistics all | по разделам, как в rtpengine-tui: SIP-сообщения, транзакции, ответы без состояния, диалоги, регистрации, сеть, разделяемая память, личная память по процессам (pkmem), нагрузка и по разделу на каждую другую группу со счётчиками модуля под их именами; у счётчиков скорость между обновлениями и итог, растущие отбросы и ошибки жёлтым; `y` - JSON и обратно, `s` - сохранить, `a` - вся статистика таблицей и обратно |
| `:allstats` | get_statistics all | вся статистика таблицей со скоростью; `x` сбросить, `a` - по разделам |
| `:dialogs`, `:dlg` | dlg_list | `D` завершить |
| `:contacts`, `:ul` | ul_dump | `D` удалить контакт, `X` удалить AOR |
| `:processes`, `:ps` | ps, log_level | `+` `-` уровень лога процесса |
| `:dispatcher`, `:ds` | ds_list | `T` вкл/выкл, `R` ds_reload |
| `:gateways`, `:gw`, `:dr` | dr_reload_status, dr_gw_status | `T` вкл/выкл, `R` dr_reload |
| `:rtpengine`, `:rtp` | rtpengine_show | `T` вкл/выкл |
| `:clusterer`, `:shtags` | clusterer_list, clusterer_list_shtags | - |
| `:status`, `:sr` | sr_list_status (3.2+) | - |
| `:tcp` | list_tcp_conns | - |
| `:commands` | which | `enter` выполняет команду без параметров |
| `:mi <команда> [k=v …]` | любая | меняющие состояние - с подтверждением |
| `:theme [имя]` | - | без имени - список с предпросмотром, `esc` возвращает прежнюю |
| `:lang [en\|ru]` | - | язык интерфейса; без аргумента показывает текущий |
| `:refresh <сек>` | - | период автообновления |

## Язык интерфейса

Интерфейс на английском и русском. Язык берётся из `-lang`, затем из `language:` в конфиге, затем из `LC_ALL`, `LC_MESSAGES` или `LANG`; если там нет поддерживаемого, интерфейс на английском. `:lang ru` и `:lang en` переключают язык на ходу и сразу перерисовывают открытые экраны; в конфиг выбор не записывается. То, что нашёл опрос хоста (предупреждения, ошибки), пишется один раз, на языке того момента; `r` в списке хостов опрашивает заново.

Переведено всё, что пишет сам opensips-tui. Имена из OpenSIPS остаются как есть: MI-команды, модули, параметры, состояния диалогов и назначений, имена SIP-заголовков. Каталог лежит в `internal/opensips/locale_ru.go`; тесты `internal/opensips` падают, если у помеченной строки нет перевода, в каталоге лишний ключ, у перевода другие `%`-глаголы или текст уходит в ошибку или строку статуса без `T()`.

## Версии OpenSIPS

При подключении opensips-tui опрашивает хост:

1. одним вызовом shell - `opensips -V` (установленная версия) и пути к `opensips-cli` и `opensipsctl`;
2. через MI - `version` (версия запущенного процесса), `which` (какие MI-команды есть) и `uptime`.

Если запущенная версия не совпадает с установленной, в шапке и в списке хостов будет жёлтое «установлен X»: пакет обновили, а OpenSIPS не перезапустили.

Экраны и действия вызывают не MI-команды напрямую, а операции из `internal/opensips/capabilities.go` (`dialog.end`, `ul.rm_contact`…). У операции есть варианты по диапазонам версий: какая MI-команда и с какими параметрами её выполняет. Например, `dlg_end_dlg` с 3.0 принимает `dialog_id`, а в 2.x - пару `h_entry h_id`. Кроме версии, операция проверяется по списку из `which`: если модуль не загружен, его команд там нет.

- 3.0 и новее - всё через MI в JSON.
- 2.x - MI там текстовый, opensips-cli с ним не работает. Если на хосте есть `opensipsctl`, доступен `:mi <команда>` через `opensipsctl fifo`.

Недоступный экран не открывается, причина пишется в строке статуса: `status/report: нужен OpenSIPS 3.2+, на хосте 3.1.10` или `на хосте нет MI-команды ds_list: модуль не загружен`. В справке (`?`) внизу видно, какие экраны есть на текущем хосте.

Новое отличие версий добавляется вариантом `Variant{Command, Params, Since, Until}` в таблицу операций.

## Темы

Встроенные: `default` (он же `k9s`), `dracula`, `nord`, `gruvbox`, `solarized-dark`, `solarized-light`, `monokai`, `catppuccin`, `tokyo-night`. Свои задаются в `themes` конфига: `base` - встроенная тема за основу, остальное - цвета по именам или `#rrggbb`: `bg`, `fg`, `border`, `title`, `header`, `key`, `keyDesc`, `infoKey`, `infoValue`, `logo`, `selectBg`, `selectFg`, `marked`, `crumbBg`, `crumbFg`, `crumbLast`, `ok`, `warn`, `err`, `dim`, `accent`, `jsonKey`, `jsonString`.

## Эмулятор

`make demo-opensips` запускает `opensips-tui-mock -confdir bin/opensips-mock-etc`. Он пишет конфиг `bin/opensips-mock-etc/opensips-tui.yaml` и печатает команду для запуска opensips-tui с ним. В конфиге десять хостов: 3.4, 3.4 с невыполненным рестартом, 3.6, 3.1 без dispatcher и rtpengine, 3.6 через `mi_fifo` и через `mi_http`, 3.4 с `via: direct`, хост без `mi_http`, 2.4 и недоступный.

```sh
make demo-opensips                                   # держать запущенным
./bin/opensips-tui -config bin/opensips-mock-etc/opensips-tui.yaml
```

Хосты, кроме `via: direct`, ходят через сам `opensips-tui-mock`, он подменяет ssh (`ssh.command` в конфиге): принимает те же аргументы и отвечает как хост с OpenSIPS. Так проверяется настоящий ssh-транспорт: сборка команды, кавычки, коды выхода. Что эмулировать, задаёт имя цели: `mock-3.1.10` - версия, `pkg3.4.9` - установленный пакет новее запущенного, `no<модуль>` - модуль не загружен (`nohttp`, `nofifo` - `mi_http`, `mi_fifo`), `down` - хост недоступен. Для хоста с `via: direct` эмулятор сам слушает `mi_http` (по умолчанию 127.0.0.1:18888, флаг `-listen`), поэтому его нужно держать запущенным. Состояние (диалоги, контакты, шлюзы) сохраняется между вызовами в `$OPENSIPS_TUI_MOCK_STATE`. Номера, имена и адреса в эмуляторе вымышленные: номера 555-01xx, `example.com`, адреса из RFC 5737.

```yaml
ssh:
  command: ./bin/opensips-tui-mock
hosts:
  - name: lab
    ssh: mock-3.1.10-nodispatcher
```

Тесты идут против эмулятора (`make test`), включая тесты интерфейса на виртуальном экране.
