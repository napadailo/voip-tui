package mock

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfOpts describes the mock node in its rtpengine.conf.
type ConfOpts struct {
	CLI string
}

// SampleConf returns an rtpengine.conf for the mock. It deliberately contains
// what the config view should flag: a misspelt option, options from releases
// newer than old mocks, a duplicate key, values changed at runtime, and
// interface/template sections.
func SampleConf(o ConfOpts) string {
	return fmt.Sprintf(`[rtpengine]
table = 0
# no-fallback = false

interfaces-config = interface
templates = templates

listen-ng = localhost:2223
listen-cli = %s
listen-http = localhost:2225

timeout = 60
silent-timeout = 3600
final-timeout = 0
offer-timeout = 3600
delete-delay = 30
tos = 184
control-tos = 184

port-min = 30000
port-max = 40000

max-sessions = 1000
# typo on purpose: rtpengine rejects unknown keys at startup
max-sesions = 1500
max-sessions = -1

log-level = 6
log-facility = local1
log-level-ice = 5

num-threads = 16
redis = 127.0.0.1:6379/1
redis-allowed-errors = -1
redis-disable-time = 10

recording-dir = /var/spool/rtpengine
recording-method = proc

# newer releases only
mqtt-host = mqtt.example.com
io-uring = true
http-buf-size = 1024

[interface-pub]
address = 10.20.10.11
port-min = 30000
port-max = 40000

[interface-priv]
address = 10.10.0.2

[templates]
WebRTC = transport-protocol=UDP/TLS/RTP/SAVPF ICE=force trickle-ICE rtcp-mux=[offer require] no-rtcp-attribute SDES=off generate-mid
offer = replace-origin replace-session-connection ICE=remove RTP AVP codec-strip-G729 strict-sorce
default = TOS=184 label=rtpe

[rtpengine-testing]
table = -1
`, o.CLI)
}

// SampleRecordingConf returns an rtpengine-recording.conf for the mock.
func SampleRecordingConf() string {
	return `[rtpengine-recording]
table = 0
num-threads = 8
spool-dir = /var/spool/rtpengine
output-dir = /var/lib/rtpengine-recording
output-pattern = %Y%m%d/%c-%t
output-format = wav
output-mixed = true
output-single = false
mix-num-inputs = 4

# newer releases only
notify-uri = https://recordings.example.com/notify
flush-packets = true
s3-host = s3.example.com

mysql-host = localhost
mysql-port = 3306
mysql-user = rtpengine
mysql-pass = s3cr3t
mysql-db = rtpengine
`
}

// WriteConfs stores both sample files in dir and returns their paths.
func WriteConfs(dir string, o ConfOpts) (conf, rec string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	conf = filepath.Join(dir, "rtpengine.conf")
	rec = filepath.Join(dir, "rtpengine-recording.conf")
	if err := os.WriteFile(conf, []byte(SampleConf(o)), 0o644); err != nil {
		return "", "", err
	}
	return conf, rec, os.WriteFile(rec, []byte(SampleRecordingConf()), 0o644)
}
