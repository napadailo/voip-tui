// Package config loads rtpengine-tui hosts (rtpengine machines) from a YAML file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/napadailo/voip-tui/i18n"
)

// Transports to the rtpengine CLI (listen-cli).
const (
	// ViaTunnel opens an SSH direct-tcpip channel (ssh -W) to listen-cli.
	ViaTunnel = "tunnel"
	// ViaCtl runs rtpengine-ctl on the host over SSH.
	ViaCtl = "ctl"
	// ViaDirect connects to listen-cli over TCP from this machine.
	ViaDirect = "direct"
)

// Labels tag a host. In the config they are a map ({env: prod, dc: dc1}),
// a list of tags ([prod, dc1]) or one string ("prod", "prod, dc1"); a tag
// is stored as a key with an empty value.
type Labels map[string]string

// UnmarshalYAML accepts the map, list and string forms.
func (l *Labels) UnmarshalYAML(n *yaml.Node) error {
	out := Labels{}
	switch n.Kind {
	case yaml.MappingNode:
		var m map[string]string
		if err := n.Decode(&m); err != nil {
			return err
		}
		for k, v := range m {
			out[k] = v
		}
	case yaml.SequenceNode:
		var tags []string
		if err := n.Decode(&tags); err != nil {
			return err
		}
		for _, t := range tags {
			out.addTag(t)
		}
	case yaml.ScalarNode:
		for _, t := range strings.FieldsFunc(n.Value, func(r rune) bool { return r == ',' || r == ' ' }) {
			out.addTag(t)
		}
	default:
		return fmt.Errorf(i18n.T("line %d: labels: expected a map, a list or a string"), n.Line)
	}
	*l = out
	return nil
}

// addTag stores "k=v" as a key/value pair and anything else as a tag.
func (l Labels) addTag(t string) {
	t = strings.TrimSpace(t)
	if t == "" {
		return
	}
	k, v, _ := strings.Cut(t, "=")
	l[strings.TrimSpace(k)] = strings.TrimSpace(v)
}

// Host describes one rtpengine machine.
type Host struct {
	Name string `yaml:"name"`
	// Address is an SSH destination (host, user@host, ~/.ssh/config alias)
	// or "local" to run commands on this machine.
	Address      string `yaml:"address"`
	User         string `yaml:"user,omitempty"`
	Port         int    `yaml:"port,omitempty"`
	IdentityFile string `yaml:"identityFile,omitempty"`
	Sudo         bool   `yaml:"sudo,omitempty"`
	// Via selects how CLI commands reach rtpengine: tunnel (default), ctl, direct.
	Via string `yaml:"via,omitempty"`
	// CLI is the listen-cli address as seen from the host (tunnel, ctl) or
	// from here (direct). Empty: listen-cli from rtpengine.conf, then 127.0.0.1:9900.
	CLI string `yaml:"cli,omitempty"`
	// Version overrides detection, e.g. "mr12.5" or "11.5.1.18".
	Version             string `yaml:"version,omitempty"`
	ConfigFile          string `yaml:"configFile,omitempty"`
	RecordingConfigFile string `yaml:"recordingConfigFile,omitempty"`
	CtlBin              string `yaml:"ctlBin,omitempty"`
	// LogCommand prints rtpengine's log lines of one call: {callid} becomes
	// the shell-quoted Call-ID, {since} a journalctl --since value. Empty:
	// journalctl -t rtpengine, which has the log under systemd both with
	// syslog and with log-stderr.
	LogCommand string `yaml:"logCommand,omitempty"`
	Labels     Labels `yaml:"labels,omitempty"`
}

// SSH holds defaults applied to every host.
type SSH struct {
	User           string   `yaml:"user,omitempty"`
	IdentityFile   string   `yaml:"identityFile,omitempty"`
	Options        []string `yaml:"options,omitempty"`
	ConnectTimeout int      `yaml:"connectTimeout,omitempty"`
	// Multiplex keeps one SSH connection per host (OpenSSH ControlMaster).
	Multiplex *bool `yaml:"multiplex,omitempty"`
}

// Config is the on-disk configuration.
type Config struct {
	CurrentHost string `yaml:"currentHost,omitempty"`
	Theme       string `yaml:"theme,omitempty"`
	// Language of the interface: en, ru; empty follows LANG.
	Language       string `yaml:"language,omitempty"`
	RefreshSeconds int    `yaml:"refreshSeconds,omitempty"`
	DownloadDir    string `yaml:"downloadDir,omitempty"`
	SSH            SSH    `yaml:"ssh,omitempty"`
	Hosts          []Host `yaml:"hosts"`
	// Themes are custom skins: {name: {base: nord, border: "#ff8800", ...}}.
	Themes map[string]map[string]string `yaml:"themes,omitempty"`

	path string
}

// DefaultPath returns $XDG_CONFIG_HOME/rtpengine-tui/config.yaml or ~/.config/rtpengine-tui/config.yaml.
func DefaultPath() string {
	if p := os.Getenv("RTPENGINE_TUI_CONFIG"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "rtpengine-tui", "config.yaml")
}

// Load reads the config file. A missing file yields an empty config.
func Load(path string) (*Config, error) {
	cfg := &Config{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.applyDefaults()
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf(i18n.T("parse %s: %w"), path, err)
	}
	cfg.path = path
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.RefreshSeconds <= 0 {
		c.RefreshSeconds = 5
	}
	if c.Theme == "" {
		c.Theme = "default"
	}
	if c.SSH.ConnectTimeout <= 0 {
		c.SSH.ConnectTimeout = 5
	}
	if c.SSH.Multiplex == nil {
		t := true
		c.SSH.Multiplex = &t
	}
	if c.DownloadDir == "" {
		home, _ := os.UserHomeDir()
		dl := filepath.Join(home, "Downloads")
		if st, err := os.Stat(dl); err == nil && st.IsDir() {
			c.DownloadDir = dl
		} else {
			c.DownloadDir = "."
		}
	}
	c.DownloadDir = ExpandHome(c.DownloadDir)
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if h.Name == "" {
			h.Name = h.Address
		}
		if h.Via == "" {
			h.Via = ViaTunnel
			if h.Address == "local" {
				h.Via = ViaDirect
			}
		}
		if h.CtlBin == "" {
			h.CtlBin = "rtpengine-ctl"
		}
	}
}

// Validate checks host entries.
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for i, h := range c.Hosts {
		if h.Address == "" {
			return fmt.Errorf(i18n.T("hosts[%d] %q: address is required"), i, h.Name)
		}
		if seen[h.Name] {
			return fmt.Errorf(i18n.T("hosts[%d]: duplicate name %q"), i, h.Name)
		}
		seen[h.Name] = true
		switch h.Via {
		case ViaTunnel, ViaCtl, ViaDirect:
		default:
			return fmt.Errorf(i18n.T("hosts[%d] %q: via must be tunnel, ctl or direct, got %q"), i, h.Name, h.Via)
		}
		if h.Via == ViaDirect && h.CLI == "" && h.Address != "local" {
			return fmt.Errorf(i18n.T("hosts[%d] %q: via: direct needs cli: <ip:port>"), i, h.Name)
		}
	}
	return nil
}

// AddHost appends an ad-hoc host (from command line flags).
func (c *Config) AddHost(h Host) error {
	c.Hosts = append(c.Hosts, h)
	c.applyDefaults()
	return c.Validate()
}

// Path returns the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// Find returns the host with the given name.
func (c *Config) Find(name string) (*Host, bool) {
	for i := range c.Hosts {
		if c.Hosts[i].Name == name {
			return &c.Hosts[i], true
		}
	}
	return nil, false
}

// Current returns the active host, falling back to the first one.
func (c *Config) Current() (*Host, bool) {
	if h, ok := c.Find(c.CurrentHost); ok {
		return h, true
	}
	if len(c.Hosts) > 0 {
		return &c.Hosts[0], true
	}
	return nil, false
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}
