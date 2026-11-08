package bash

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
	"time"
)

type Limits struct {
	Timeout time.Duration
	MaxOut  int
}

var Default = Limits{Timeout: 20 * time.Second, MaxOut: 256 << 10}

type Runner struct {
	Dir string
	// Cred 非 nil 时以此用户执行；nil 表示沿用当前用户。
	Cred *syscall.Credential
}

// New 的 runAs 为空或等于当前用户时不降权。
func New(dir, runAs string) (*Runner, error) {
	r := &Runner{Dir: dir}
	if runAs == "" {
		return r, nil
	}
	u, err := user.Lookup(runAs)
	if err != nil {
		return nil, fmt.Errorf("user %q not found: %w", runAs, err)
	}
	if u.Uid == strconv.Itoa(os.Getuid()) {
		return r, nil
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("user %q has non-numeric uid/gid", runAs)
	}
	r.Cred = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	return r, nil
}

func (r *Runner) Run(ctx context.Context, script string) (string, error) {
	if script == "" {
		return "", errors.New("cmd is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, Default.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", script)
	cmd.Dir = r.Dir
	// 清空环境变量：容器里的密钥不该被 /proc/self/environ 读到
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "LANG=C.UTF-8"}
	if r.Cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: r.Cred}
	}

	var buf bytes.Buffer
	w := &capWriter{w: &buf, max: Default.MaxOut}
	cmd.Stdout = w
	cmd.Stderr = w

	err := cmd.Run()
	out := buf.String()
	if w.truncated {
		out += "\n... (output exceeded 256KB, truncated; narrow the query)"
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("command timed out after %s", Default.Timeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// 非零退出码是正常结果（grep 没匹配到就是 1）
			return out, nil
		}
		return out, err
	}
	return out, nil
}

type capWriter struct {
	w         io.Writer
	max       int
	n         int
	truncated bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	room := c.max - c.n
	if room <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		if _, err := c.w.Write(p[:room]); err != nil {
			return 0, err
		}
		c.n += room
		c.truncated = true
		return len(p), nil
	}
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}
