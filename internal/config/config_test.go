package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(write(t, "key-list: [\"k\"]\nproxy-list: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.DataDir != "/session" {
		t.Fatalf("默认值不对: %+v", c)
	}
}

func TestProxyLookup(t *testing.T) {
	c, err := Load(write(t, "proxy-list:\n  - path: a\n    origin: https://x.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Proxy("a") == nil || c.Proxy("b") != nil {
		t.Fatal("按 path 查找上游失败")
	}
}

func TestInvalidProxyRejected(t *testing.T) {
	for _, body := range []string{
		"proxy-list:\n  - path: \"a/b\"\n    origin: https://x.test\n",
		"proxy-list:\n  - path: a\n    origin: ftp://x\n",
		"proxy-list:\n  - origin: https://x.test\n",
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Fatalf("应当拒绝: %s", body)
		}
	}
}

func TestKeyAllowed(t *testing.T) {
	c, _ := Load(write(t, "key-list: [\"good\"]\n"))
	if !c.KeyAllowed("good") {
		t.Fatal("合法 key 被拒")
	}
	if c.KeyAllowed("bad") || c.KeyAllowed("") {
		t.Fatal("非法 key 通过了")
	}
}
