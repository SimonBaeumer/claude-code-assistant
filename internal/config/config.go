// Package config loads and validates the claude-relay YAML configuration.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var DefaultYAML []byte

type Config struct {
	General General           `yaml:"general"`
	Routes  []Route           `yaml:"routes"`
	Claude  Claude            `yaml:"claude"`
	Viewer  Viewer            `yaml:"viewer"`
	Limits  Limits            `yaml:"limits"`
	Queue   Queue             `yaml:"queue"`
	Notify  bool              `yaml:"notify"`
	Actions map[string]Action `yaml:"actions"`
}

type General struct {
	Session string `yaml:"session"`
	Dir     string `yaml:"dir"`
}

type Route struct {
	Path    string `yaml:"path"`
	Session string `yaml:"session"`
	Dir     string `yaml:"dir"`
}

type Claude struct {
	Command          string   `yaml:"command"`
	Args             []string `yaml:"args"`
	Resume           bool     `yaml:"resume"`
	StartupTimeoutMs int      `yaml:"startup_timeout_ms"`
}

type Viewer struct {
	Terminal      []string `yaml:"terminal"`
	OpenIfMissing bool     `yaml:"open_if_missing"`
	Focus         bool     `yaml:"focus"`
}

type Limits struct {
	MaxChars int `yaml:"max_chars"`
}

type Queue struct {
	MaxItems int `yaml:"max_items"`
}

type Action struct {
	Instruction string `yaml:"instruction"`
	Body        string `yaml:"body"`
	Ask         bool   `yaml:"ask"`
}

// Placeholders lists every template placeholder an action may use.
var Placeholders = []string{"file", "abs_file", "lines", "language", "project", "question", "selection", "code_block"}

var (
	sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	placeholderRe = regexp.MustCompile(`\{([a-z_]+)\}`)
)

// Path returns the config file location ($CLAUDE_RELAY_CONFIG overrides it).
func Path() string {
	if p := os.Getenv("CLAUDE_RELAY_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "claude-relay", "config.yaml")
}

// Load reads the defaults, overlays the user file at path (if it exists) and validates.
func Load(path string) (*Config, error) {
	cfg := &Config{}
	if err := decode(DefaultYAML, cfg); err != nil {
		return nil, fmt.Errorf("built-in defaults: %w", err)
	}
	defaultBodies := map[string]string{}
	for name, a := range cfg.Actions {
		defaultBodies[name] = a.Body
	}

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("config %s: %w", path, err)
	default:
		if err := decode(data, cfg); err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
	}

	// An overridden action that leaves out "body" keeps the default code block.
	for name, a := range cfg.Actions {
		if a.Body == "" {
			if b, ok := defaultBodies[name]; ok {
				a.Body = b
			} else {
				a.Body = "\n{code_block}"
			}
			cfg.Actions[name] = a
		}
	}

	cfg.General.Dir = ExpandHome(cfg.General.Dir)
	for i := range cfg.Routes {
		cfg.Routes[i].Path = filepath.Clean(ExpandHome(cfg.Routes[i].Path))
		if cfg.Routes[i].Dir == "" {
			cfg.Routes[i].Dir = cfg.Routes[i].Path
		}
		cfg.Routes[i].Dir = filepath.Clean(ExpandHome(cfg.Routes[i].Dir))
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func decode(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Validate returns all problems found, joined into one readable error.
func (c *Config) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if !sessionNameRe.MatchString(c.General.Session) {
		add("general.session: %q must match [A-Za-z0-9_-]+", c.General.Session)
	}
	if c.General.Dir == "" || !filepath.IsAbs(c.General.Dir) {
		add("general.dir: must be an absolute path (or start with ~/), got %q", c.General.Dir)
	}
	for i, r := range c.Routes {
		if r.Path == "" || !filepath.IsAbs(r.Path) {
			add("routes[%d].path: must be an absolute path (or start with ~/), got %q", i, r.Path)
		}
		if !sessionNameRe.MatchString(r.Session) {
			add("routes[%d].session: %q must match [A-Za-z0-9_-]+", i, r.Session)
		}
	}
	if c.Claude.Command == "" {
		add("claude.command: must not be empty")
	}
	if c.Claude.StartupTimeoutMs <= 0 {
		add("claude.startup_timeout_ms: must be > 0")
	}
	if len(c.Viewer.Terminal) == 0 {
		add("viewer.terminal: must name a terminal command, e.g. [konsole, --separate, -e]")
	}
	if c.Limits.MaxChars <= 0 {
		add("limits.max_chars: must be > 0")
	}
	if c.Queue.MaxItems < 0 {
		add("queue.max_items: must be >= 0 (0 disables queueing)")
	}
	if len(c.Actions) == 0 {
		add("actions: at least one action is required")
	}
	known := map[string]bool{}
	for _, p := range Placeholders {
		known[p] = true
	}
	names := make([]string, 0, len(c.Actions))
	for n := range c.Actions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		a := c.Actions[name]
		if strings.TrimSpace(a.Instruction) == "" {
			add("actions.%s.instruction: must not be empty", name)
		}
		for field, text := range map[string]string{"instruction": a.Instruction, "body": a.Body} {
			for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
				if !known[m[1]] {
					add("actions.%s.%s: unknown placeholder {%s} (known: %s)", name, field, m[1], strings.Join(Placeholders, ", "))
				}
			}
		}
		if strings.Contains(a.Instruction, "{question}") && !a.Ask {
			add("actions.%s: uses {question} but ask is false", name)
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n  "))
	}
	return nil
}

// ExpandHome replaces a leading "~" with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
