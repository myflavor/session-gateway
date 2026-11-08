package proxy

import (
	"bytes"
	"io"
	"testing"
)

// capture 必须把读到的内容原样交给调用方，同时留一份副本。
func TestCapturePassesThrough(t *testing.T) {
	body := []byte("data: hello\n\ndata: world\n\n")
	var got []byte
	c := &capture{rc: io.NopCloser(bytes.NewReader(body)), onEOF: func(b []byte) { got = append([]byte(nil), b...) }}

	out, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("转发内容被改动了: %q", out)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("回调拿到的内容不对: %q", got)
	}
}

// 超过上限时停止累积，但转发不能受影响。
func TestCaptureStopsAtLimit(t *testing.T) {
	body := bytes.Repeat([]byte("x"), captureLimit+1024)
	var got []byte
	c := &capture{rc: io.NopCloser(bytes.NewReader(body)), onEOF: func(b []byte) { got = b }}
	out, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(body) {
		t.Fatalf("转发被截断了: %d != %d", len(out), len(body))
	}
	if len(got) > captureLimit+64 {
		t.Fatalf("累积没有在上限处停止: %d", len(got))
	}
}

// 回调只触发一次，即使读取过程中多次遇到错误。
func TestCaptureCallbackOnce(t *testing.T) {
	calls := 0
	c := &capture{rc: io.NopCloser(bytes.NewReader([]byte("x"))), onEOF: func([]byte) { calls++ }}
	io.ReadAll(c)
	io.ReadAll(c)
	if calls != 1 {
		t.Fatalf("回调触发了 %d 次", calls)
	}
}
