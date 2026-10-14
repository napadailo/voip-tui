// Package config loads opensips-tui hosts (OpenSIPS machines) from a YAML file.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/napadailo/voip-tui/i18n"
)

// Host describes one OpenSIPS host reached over ssh.
type Host struct {
	Name string `yaml:"name"`
	// SSH is where to connect: a ~/.ssh/config alias, host or user@host. Defaults to Name.
	SSH        string   `yaml:"ssh,omitempty"`
	Port       int      `yaml:"port,omitempty"`
	User       string   `yaml:"user,omitempty"`
	SSHOptions []string `yaml:"sshOptions,omitempty"` // -o values, e.g. ProxyJump=bastion
	// opensips-cli on the host: path, args before -x (e.g. -f /etc/opensips-cli.cfg), instance (-i).
	CLI      string   `yaml:"cli,omitempty"`
	CLIArgs  []string `yaml:"cliArgs,omitempty"`
	Instance string   `yaml:"instance,omitempty"`
	// Sudo runs opensips-cli, or the mi_fifo commands, through sudo -n: usually
	// needed to access mi_fifo.
	Sudo bool `yaml:"sudo,omitempty"`
	// Via is how MI commands reach OpenSIPS: cli (default: opensips-cli over
	// ssh), fifo (mi_fifo over ssh, no opensips-cli needed), http (mi_http
	// through an ssh tunnel) or direct (mi_http from this machine, no ssh).
	Via string `yaml:"via,omitempty"`
	// FIFO and ReplyDir are mi_fifo's fifo_name and reply_dir on the host (via: fifo).
	FIFO     string `yaml:"fifo,omitempty"`
	ReplyDir string `yaml:"replyDir,omitempty"`
	// MI is the mi_http URL, as seen from the host (via: http) or from here (via: direct).
	MI             string `yaml:"mi,omitempty"`
	ReadOnly       bool   `yaml:"readOnly,omitempty"`
	TimeoutSeconds int    `yaml:"timeoutSeconds,omitempty"`
	Labels         Labels `yaml:"labels,omitempty"`
}

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

// String is the labels sorted, tags bare and pairs as k=v.
func (l Labels) String() string {
	var p []string
	for k, v := range l {
		if v == "" {
			p = append(p, k)
			continue
		}
		p = append(p, k+"="+v)
	}
	sort.Strings(p)
	return strings.Join(p, ",")
}

// Ways for MI commands to reach OpenSIPS, the via: of a host.
const (
	ViaCLI    = "cli"
	ViaFIFO   = "fifo"
	ViaHTTP   = "http"
	ViaDirect = "direct"
)

// Defaults of the mi_fifo and mi_http modules (fifo_name, reply_dir; httpd
// port 8888, mi_http root "mi").
const (
	DefaultFIFO     = "/tmp/opensips_fifo"
	DefaultReplyDir = "/tmp/"
	DefaultMI       = "http://127.0.0.1:8888/mi"
)

// Target is the ssh destination.
func (c *Host) Target() string {
	if c.SSH != "" {
		return c.SSH
	}
	return c.Name
}

// SSHConfig tunes the shared ssh connections.
type SSHConfig struct {
	// Command replaces "ssh", e.g. bin/opensips-tui-mock to emulate hosts.
	Command               string `yaml:"command,omitempty"`
	ControlPersistSeconds int    `yaml:"controlPersistSeconds,omitempty"`
	ConnectTimeoutSeconds int    `yaml:"connectTimeoutSeconds,omitempty"`
}

// Config is the on-disk configuration.
type Config struct {
	CurrentHost    string `yaml:"currentHost,omitempty"`
	RefreshSeconds int    `yaml:"refreshSeconds,omitempty"`
	DefaultView    string `yaml:"defaultView,omitempty"`
	Theme          string `yaml:"theme,omitempty"`
	// Language of the interface: en, ru; empty follows LANG.
	Language    string                       `yaml:"language,omitempty"`
	ReadOnly    bool                         `yaml:"readOnly,omitempty"`
	DownloadDir string                       `yaml:"downloadDir,omitempty"`
	SSH         SSHConfig                    `yaml:"ssh,omitempty"`
	Themes      map[string]map[string]string `yaml:"themes,omitempty"`
	Hosts       []Host                       `yaml:"hosts"`

	path string
}

// DefaultPath returns $OPENSIPS_TUI_CONFIG, $XDG_CONFIG_HOME/opensips-tui/config.yaml or ~/.config/opensips-tui/config.yaml.
func DefaultPath() string {
	if p := os.Getenv("OPENSIPS_TUI_CONFIG"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "opensips-tui", "config.yaml")
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
	if err := Parse(data, cfg); err != nil {
		return nil, fmt.Errorf(i18n.T("parse %s: %w"), path, err)
	}
	cfg.path = path
	return cfg, nil
}

// Parse decodes YAML strictly (unknown fields are errors) and validates hosts.
func Parse(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	seen := map[string]bool{}
	for _, c := range cfg.Hosts {
		if c.Name == "" {
			return errors.New(i18n.T("host without name"))
		}
		if seen[c.Name] {
			return fmt.Errorf(i18n.T("duplicate host %q"), c.Name)
		}
		seen[c.Name] = true
		switch c.Via {
		case "", ViaCLI, ViaFIFO, ViaHTTP, ViaDirect:
		default:
			return fmt.Errorf(i18n.T("host %q: via: %q, want cli, fifo, http or direct"), c.Name, c.Via)
		}
	}
	cfg.applyDefaults()
	return nil
}

func (c *Config) applyDefaults() {
	if c.RefreshSeconds <= 0 {
		c.RefreshSeconds = 5
	}
	if c.DefaultView == "" {
		c.DefaultView = "dialogs"
	}
	if c.Theme == "" {
		c.Theme = "default"
	}
	if c.SSH.Command == "" {
		c.SSH.Command = "ssh"
	}
	if c.SSH.ControlPersistSeconds <= 0 {
		c.SSH.ControlPersistSeconds = 300
	}
	if c.SSH.ConnectTimeoutSeconds <= 0 {
		c.SSH.ConnectTimeoutSeconds = 5
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
		ctx := &c.Hosts[i]
		if ctx.CLI == "" {
			ctx.CLI = "opensips-cli"
		}
		if ctx.Via == "" {
			ctx.Via = ViaCLI
		}
		if ctx.FIFO == "" {
			ctx.FIFO = DefaultFIFO
		}
		if ctx.ReplyDir == "" {
			ctx.ReplyDir = DefaultReplyDir
		}
		if ctx.MI == "" {
			ctx.MI = DefaultMI
		}
		if c.Hosts[i].TimeoutSeconds <= 0 {
			c.Hosts[i].TimeoutSeconds = 15
		}
	}
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

// ExpandHome replaces a leading ~.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}
