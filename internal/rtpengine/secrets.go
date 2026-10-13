package rtpengine

import (
	"net"
	"strconv"
	"strings"
)

// RedisEndpoint is a parsed [PW@]HOST:PORT[/DB] value of the redis,
// redis-write and redis-subscribe options.
type RedisEndpoint struct {
	Host string
	Port string
	DB   int // -1 when absent
	auth string
}

// ParseRedisEndpoint parses a redis option value.
func ParseRedisEndpoint(v string) (RedisEndpoint, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return RedisEndpoint{}, false
	}
	e := RedisEndpoint{DB: -1}
	if i := strings.LastIndexByte(v, '@'); i >= 0 {
		e.auth, v = v[:i], v[i+1:]
	}
	if i := strings.LastIndexByte(v, '/'); i >= 0 {
		db, err := strconv.Atoi(v[i+1:])
		if err != nil {
			return RedisEndpoint{}, false
		}
		e.DB, v = db, v[:i]
	}
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return RedisEndpoint{}, false
	}
	e.Host, e.Port = host, port
	return e, true
}

// Addr is host:port.
func (e RedisEndpoint) Addr() string { return net.JoinHostPort(e.Host, e.Port) }

// String hides the password.
func (e RedisEndpoint) String() string {
	s := e.Addr()
	if e.DB >= 0 {
		s += "/" + strconv.Itoa(e.DB)
	}
	if e.auth != "" {
		s = "***@" + s
	}
	return s
}

// secretKeys are config options whose values must not be shown.
var secretKeys = []string{"pass", "secret", "auth", "key-id", "access-key"}

// MaskSecret hides passwords in config values for display: the PW@ part of
// redis endpoints and whole values of password-like options.
func MaskSecret(key, value string) string {
	switch key {
	case "redis", "redis-write", "redis-subscribe":
		if e, ok := ParseRedisEndpoint(value); ok {
			return e.String()
		}
	}
	for _, s := range secretKeys {
		if strings.Contains(key, s) && value != "" {
			return "***"
		}
	}
	return value
}
