package rtpengine

import (
	"net"
	"sort"
	"strings"
	"time"
)

// ProxyStat sums up the calls one signalling proxy has on a node.
type ProxyStat struct {
	Proxy               string // IP, without the port
	Calls, Own, Foreign int
	Deleting            int
	Ports               int // distinct source ports: proxy workers/sockets talking NG
	Oldest, Newest      time.Time
}

// ProxyHost strips the source port from a session's proxy (created_from).
// The port is ephemeral, usually one per proxy worker, so calls are grouped
// by address.
func ProxyHost(proxy string) string {
	if h, _, err := net.SplitHostPort(proxy); err == nil {
		return h
	}
	if i := strings.LastIndexByte(proxy, ':'); i > 0 && net.ParseIP(proxy[:i]) != nil {
		return proxy[:i] // IPv6 printed without brackets
	}
	return proxy
}

// GroupByProxy counts sessions per proxy address, most calls first.
func GroupByProxy(sess []Session) []ProxyStat {
	byHost := map[string]*ProxyStat{}
	ports := map[string]map[string]bool{}
	for _, s := range sess {
		h := ProxyHost(s.Proxy)
		p := byHost[h]
		if p == nil {
			p = &ProxyStat{Proxy: h}
			byHost[h] = p
			ports[h] = map[string]bool{}
		}
		p.Calls++
		if s.Foreign {
			p.Foreign++
		} else {
			p.Own++
		}
		if s.Deleted {
			p.Deleting++
		}
		ports[h][s.Proxy] = true
		if !s.Created.IsZero() {
			if p.Oldest.IsZero() || s.Created.Before(p.Oldest) {
				p.Oldest = s.Created
			}
			if s.Created.After(p.Newest) {
				p.Newest = s.Created
			}
		}
	}
	res := make([]ProxyStat, 0, len(byHost))
	for h, p := range byHost {
		p.Ports = len(ports[h])
		res = append(res, *p)
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Calls != res[j].Calls {
			return res[i].Calls > res[j].Calls
		}
		return res[i].Proxy < res[j].Proxy
	})
	return res
}
