// Package config loads homer-tui hosts (Homer instances) from a YAML file.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/napadailo/voip-tui/i18n"
)

// Host describes a single Homer instance (homer-app).
type Host struct {
	Name        string `yaml:"name"`
	URL         string `yaml:"url"`
	APIPrefix   string `yaml:"apiPrefix,omitempty"`
	Username    string `yaml:"username,omitempty"`
	Password    string `yaml:"password,omitempty"`
	PasswordCmd string `yaml:"passwordCmd,omitempty"`
	Token       string `yaml:"token,omitempty"`
	TokenCmd    string `yaml:"tokenCmd,omitempty"`
	TokenHeader string `yaml:"tokenHeader,omitempty"`
	Insecure    bool   `yaml:"insecure,omitempty"`
	// Timezone is applied only to how timestamps are rendered.
	Timezone string `yaml:"timezone,omitempty"`
	Labels   Labels `yaml:"labels,omitempty"`
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

// Auth names how homer-tui signs in: password, passwordCmd, token or
// tokenCmd.
func (h *Host) Auth() string {
	switch {
	case h.Token != "" && h.Username == "":
		return "token"
	case h.TokenCmd != "" && h.Username == "":
		return "tokenCmd"
	case h.PasswordCmd != "" && h.Password == "":
		return "passwordCmd"
	}
	return "password"
}

// Config is the on-disk configuration.
type Config struct {
	CurrentHost string `yaml:"currentHost,omitempty"`
	Theme       string `yaml:"theme,omitempty"`
	// Language of the interface: en, ru; empty follows LANG.
	Language       string `yaml:"language,omitempty"`
	DownloadDir    string `yaml:"downloadDir,omitempty"`
	RefreshSeconds int    `yaml:"refreshSeconds,omitempty"`
	DefaultSince   string `yaml:"defaultSince,omitempty"`
	DefaultLimit   int    `yaml:"defaultLimit,omitempty"`
	Flow           Flow   `yaml:"flow,omitempty"`
	// SearchFields are the fields of the search form by profile, such as
	// 1_call: [from_user, to_user, callid]; ctrl-a and ctrl-d in the form
	// change them until homer-tui quits.
	SearchFields map[string][]string          `yaml:"searchFields,omitempty"`
	Hosts        []Host                       `yaml:"hosts"`
	Themes       map[string]map[string]string `yaml:"themes,omitempty"`

	path    string
	exposed bool // others may read the file, and it has secrets
}

// Flow is how a call opens; f, a, u and v in a call change it until
// homer-tui quits.
type Flow struct {
	// Hide lists kinds of messages not to show: SIP, RTCP, LOG, or HEP and
	// the payload type, such as "HEP 54".
	Hide    []string `yaml:"hide,omitempty"`
	ByAlias bool     `yaml:"byAlias,omitempty"`
	// Unfold draws a call that comes back on new lanes; unset means true.
	Unfold *bool `yaml:"unfold,omitempty"`
	// QoSGraphs opens the QoS tab as graphs instead of a table.
	QoSGraphs bool `yaml:"qosGraphs,omitempty"`
}

var flowKind = regexp.MustCompile(`^(SIP|RTCP|LOG|HEP \d+)$`)

// DefaultPath returns $HOMER_TUI_CONFIG, $XDG_CONFIG_HOME/homer-tui/config.yaml or ~/.config/homer-tui/config.yaml.
func DefaultPath() string {
	if p := os.Getenv("HOMER_TUI_CONFIG"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "homer-tui", "config.yaml")
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
	if st, err := os.Stat(path); err == nil && st.Mode().Perm()&0o077 != 0 {
		for _, h := range cfg.Hosts {
			cfg.exposed = cfg.exposed || h.Password != "" || h.Token != ""
		}
	}
	return cfg, nil
}

// Exposed tells a config file others may read with a password or a token
// in it.
func (c *Config) Exposed() bool { return c.exposed }

// Parse decodes YAML strictly (unknown fields are errors) and validates the hosts.
func Parse(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	cfg.applyDefaults()
	return cfg.Validate()
}

// Validate checks host entries.
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for i, h := range c.Hosts {
		if h.Name == "" {
			return fmt.Errorf(i18n.T("hosts[%d]: name is required"), i)
		}
		if seen[h.Name] {
			return fmt.Errorf(i18n.T("hosts[%d]: duplicate name %q"), i, h.Name)
		}
		seen[h.Name] = true
		if h.URL == "" {
			return fmt.Errorf(i18n.T("hosts[%d] %q: url is required"), i, h.Name)
		}
		if u, err := url.Parse(h.URL); err == nil && u.User != nil { // the url is on the screen
			return fmt.Errorf(i18n.T("hosts[%d] %q: url has a user or a password in it, put them in username and password"), i, h.Name)
		}
	}
	for i, k := range c.Flow.Hide {
		k = strings.ToUpper(strings.Join(strings.Fields(k), " "))
		if !flowKind.MatchString(k) {
			return fmt.Errorf(i18n.T("flow.hide: %q is not SIP, RTCP, LOG or HEP and a number"), c.Flow.Hide[i])
		}
		c.Flow.Hide[i] = k
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.RefreshSeconds <= 0 {
		c.RefreshSeconds = 5
	}
	if c.Theme == "" {
		c.Theme = "default"
	}
	if c.DefaultSince == "" {
		c.DefaultSince = "1h"
	}
	if c.DefaultLimit <= 0 {
		c.DefaultLimit = 200
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
		if c.Hosts[i].TokenHeader == "" {
			c.Hosts[i].TokenHeader = "Auth-Token"
		}
	}
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

// ResolvePassword returns the password, running passwordCmd if configured.
func (h *Host) ResolvePassword() (string, error) {
	if h.Password != "" || h.PasswordCmd == "" {
		return h.Password, nil
	}
	out, err := exec.Command("sh", "-c", h.PasswordCmd).Output()
	if err != nil {
		return "", fmt.Errorf("passwordCmd: %w", err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// ResolveToken returns the Auth-Token, running tokenCmd if configured.
func (h *Host) ResolveToken() (string, error) {
	if h.Token != "" || h.TokenCmd == "" {
		return h.Token, nil
	}
	out, err := exec.Command("sh", "-c", h.TokenCmd).Output()
	if err != nil {
		return "", fmt.Errorf("tokenCmd: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}
