package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Upstream struct {
	Path   string `json:"path"`
	Origin string `json:"origin"`
}

type Config struct {
	Listen    string     `json:"listen"`
	MCPKeys   []string   `json:"mcp_keys"`
	Upstreams []Upstream `json:"upstreams"`
	DataDir   string     `json:"data_dir"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
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
		c.DataDir = "/data/session"
	}
}

func (c *Config) validate() error {
	for i, u := range c.Upstreams {
		if strings.TrimSpace(u.Path) == "" {
			return fmt.Errorf("upstreams[%d] 缺少 path", i)
		}
		if !strings.HasPrefix(u.Origin, "http://") && !strings.HasPrefix(u.Origin, "https://") {
			return fmt.Errorf("upstreams[%d] 的 origin 必须是 http(s)://", i)
		}
		if strings.ContainsAny(u.Path, "/?#") {
			return fmt.Errorf("upstreams[%d] 的 path 不能含 / ? #", i)
		}
	}
	return nil
}

func (c *Config) Upstream(seg string) *Upstream {
	for i := range c.Upstreams {
		if c.Upstreams[i].Path == seg {
			return &c.Upstreams[i]
		}
	}
	return nil
}

func (c *Config) KeyAllowed(key string) bool {
	if key == "" {
		return false
	}
	for _, k := range c.MCPKeys {
		if k == key {
			return true
		}
	}
	return false
}
