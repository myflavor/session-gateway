package bash

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 不降权时（本地开发）读写都该成功。
func TestRunAsCurrentUser(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), "echo hello && pwd")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("got %q", out)
	}
	if _, err := r.Run(context.Background(), "touch probe.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "probe.txt")); err != nil {
		t.Fatal("降权关闭时应当能写:", err)
	}
}

// 输出超过上限要截断，而不是把整份内容倒进上下文。
func TestOutputTruncated(t *testing.T) {
	r, _ := New(t.TempDir(), "")
	out, err := r.Run(context.Background(), "yes x | head -c 400000")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > Default.MaxOut+200 {
		t.Fatalf("输出没有被截断: %d 字节", len(out))
	}
	if !strings.Contains(out, "已截断") {
		t.Fatal("截断时应当注明")
	}
}

// 超时要终止命令，不能让 agent 一直挂着。
func TestTimeout(t *testing.T) {
	r, _ := New(t.TempDir(), "")
	old := Default.Timeout
	Default.Timeout = 200 * 1e6 // 200ms
	defer func() { Default.Timeout = old }()

	_, err := r.Run(context.Background(), "sleep 5")
	if err == nil || !strings.Contains(err.Error(), "未完成") {
		t.Fatalf("期望超时错误，得到 %v", err)
	}
}

// grep 没匹配到会返回退出码 1，这是正常结果，不该当成错误。
func TestNonZeroExitIsNotError(t *testing.T) {
	r, _ := New(t.TempDir(), "")
	out, err := r.Run(context.Background(), "grep nothing /dev/null")
	if err != nil {
		t.Fatalf("退出码 1 不该是错误: %v", err)
	}
	if out != "" {
		t.Fatalf("got %q", out)
	}
}

// 工作目录必须是存档根目录，agent 用相对路径就能操作。
func TestWorkingDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte(`{"role":"user"}`+"\n"), 0o600)
	r, _ := New(dir, "")
	out, err := r.Run(context.Background(), "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.jsonl") {
		t.Fatalf("got %q", out)
	}
}
