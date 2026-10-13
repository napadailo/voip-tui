package rtpengine

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Stats is `list jsonstats` (mr8.5+): the metrics of `list totals` as JSON,
// in sections such as currentstatistics, totalstatistics, mos,
// voip_metrics, controlstatistics, interfaces and transcoders. Sections and
// keys a release does not print are missing; the getters then report so.
type Stats map[string]any

// ParseJSONStats parses the reply of `list jsonstats`.
func ParseJSONStats(raw string) (Stats, error) {
	var s Stats
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &s); err != nil {
		return nil, err
	}
	return s, nil
}

// JSONStats runs `list jsonstats` and returns it parsed and as printed.
func (c *Client) JSONStats(ctx context.Context) (Stats, string, error) {
	if err := c.need("list jsonstats"); err != nil {
		return nil, "", err
	}
	raw, err := c.Do(ctx, "list jsonstats")
	if err != nil {
		return nil, raw, err
	}
	s, err := ParseJSONStats(raw)
	return s, raw, err
}

// Obj is the section key, nil if missing.
func (s Stats) Obj(key string) Stats {
	if m, ok := s[key].(map[string]any); ok {
		return m
	}
	return nil
}

// List is the list key (proxies, interfaces, transcoders).
func (s Stats) List(key string) []Stats {
	l, _ := s[key].([]any)
	out := make([]Stats, 0, len(l))
	for _, e := range l {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// Num is the number key; numbers printed as strings count too.
func (s Stats) Num(key string) (float64, bool) {
	switch v := s[key].(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

// Int is Num rounded down, 0 if missing.
func (s Stats) Int(key string) uint64 {
	f, _ := s.Num(key)
	if f < 0 {
		return 0
	}
	return uint64(f)
}

// Str is the string key.
func (s Stats) Str(key string) string {
	v, _ := s[key].(string)
	return v
}

// NGStat is what one proxy asked rtpengine over the NG protocol since the
// start (controlstatistics), summed over its source ports.
type NGStat struct {
	Proxy   string // address without the port, as ProxyStat.Proxy
	Sockets int
	// Counts and Seconds (total processing time) by command, as rtpengine
	// names them in the JSON keys: offer, answer, delete, ping, query…
	Counts  map[string]uint64
	Seconds map[string]float64
	Errors  uint64
}

// Total is the number of NG commands of the proxy.
func (n *NGStat) Total() uint64 {
	var t uint64
	for _, c := range n.Counts {
		t += c
	}
	return t
}

// Avg is the average processing time of cmd, false without requests.
func (n *NGStat) Avg(cmd string) (time.Duration, bool) {
	c := n.Counts[cmd]
	if c == 0 {
		return 0, false
	}
	return time.Duration(n.Seconds[cmd] / float64(c) * float64(time.Second)), true
}

// NGByProxy sums controlstatistics per proxy address.
func (s Stats) NGByProxy() map[string]*NGStat {
	out := map[string]*NGStat{}
	for _, p := range s.Obj("controlstatistics").List("proxies") {
		host := ProxyHost(p.Str("proxy"))
		n := out[host]
		if n == nil {
			n = &NGStat{Proxy: host, Counts: map[string]uint64{}, Seconds: map[string]float64{}}
			out[host] = n
		}
		n.Sockets++
		for k := range p {
			switch {
			case k == "errorcount":
				n.Errors += p.Int(k)
			case strings.HasSuffix(k, "count"):
				n.Counts[strings.TrimSuffix(k, "count")] += p.Int(k)
			case strings.HasSuffix(k, "duration"):
				f, _ := p.Num(k)
				n.Seconds[strings.TrimSuffix(k, "duration")] += f
			}
		}
	}
	return out
}

// NGTotals are the commands of all proxies (total<cmd>count), most used
// first, without the ones never asked.
func (s Stats) NGTotals() []KV {
	cs := s.Obj("controlstatistics")
	type cmd struct {
		name string
		n    uint64
	}
	var cmds []cmd
	for k := range cs {
		if strings.HasPrefix(k, "total") && strings.HasSuffix(k, "count") {
			if n := cs.Int(k); n > 0 {
				cmds = append(cmds, cmd{strings.TrimSuffix(strings.TrimPrefix(k, "total"), "count"), n})
			}
		}
	}
	sort.Slice(cmds, func(i, j int) bool {
		if cmds[i].n != cmds[j].n {
			return cmds[i].n > cmds[j].n
		}
		return cmds[i].name < cmds[j].name
	})
	out := make([]KV, len(cmds))
	for i, c := range cmds {
		out[i] = KV{Key: c.name, Value: strconv.FormatUint(c.n, 10)}
	}
	return out
}
