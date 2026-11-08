package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Proxy struct {
	Path   string `yaml:"path"`
	Origin string `yaml:"origin"`
}

type Config struct {
	Listen    string   `yaml:"listen"`
	DataDir   string   `yaml:"data-dir"`
	KeyList   []string `yaml:"key-list"`
	ProxyList []Proxy  `yaml:"proxy-list"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.DataDir == "" {
		c.DataDir = "/session"
	}
}

func (c *Config) validate() error {
	for i, p := range c.ProxyList {
		if strings.TrimSpace(p.Path) == "" {
			return fmt.Errorf("proxy-list[%d]: missing path", i)
		}
		if strings.ContainsAny(p.Path, "/?#") {
			return fmt.Errorf("proxy-list[%d]: path must not contain / ? #", i)
		}
		if !strings.HasPrefix(p.Origin, "http://") && !strings.HasPrefix(p.Origin, "https://") {
			return fmt.Errorf("proxy-list[%d]: origin must start with http(s)://", i)
		}
	}
	return nil
}

func (c *Config) Proxy(seg string) *Proxy {
	for i := range c.ProxyList {
		if c.ProxyList[i].Path == seg {
			return &c.ProxyList[i]
		}
	}
	return nil
}

func (c *Config) KeyAllowed(key string) bool {
	if key == "" {
		return false
	}
	for _, k := range c.KeyList {
		if k == key {
			return true
		}
	}
	return false
}
