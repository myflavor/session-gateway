package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendCreatesFile(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Append("sess-1", Message{Role: "user", Content: rawJSON(`"第 ` + itoa(i) + ` 条"`)}); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("同一会话应当只有一个文件，得到 %v", files)
	}
	lines, _ := os.ReadFile(files[0])
	if n := strings.Count(string(lines), "\n"); n != 3 {
		t.Fatalf("期望 3 行，得到 %d", n)
	}
}

// 同一个会话并发追加，行数与请求数必须一致——追加写不能互相覆盖。
func TestConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	const n = 50
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			s.Append("sess-2", Message{Role: "user", Content: rawJSON(`"x"`)})
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	b, _ := os.ReadFile(files[0])
	if c := strings.Count(string(b), "\n"); c != n {
		t.Fatalf("期望 %d 行，得到 %d", n, c)
	}
}

// List 要能读出文件名、首条用户消息与行数，且按最近更新排序。
func TestList(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	s.Append("aaa", Message{Role: "user", Content: rawJSON(`"第一个会话的问题"`)})
	s.Append("aaa", Message{Role: "assistant", Content: rawJSON(`"回答"`)})
	s.Append("bbb", Message{Role: "user", Content: rawJSON(`"第二个会话"`)})

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("期望 2 个会话，得到 %d", len(list))
	}
	var found bool
	for _, x := range list {
		if x.First == "第一个会话的问题" {
			found = true
			if x.Lines != 2 {
				t.Fatalf("期望 2 行，得到 %d", x.Lines)
			}
		}
	}
	if !found {
		t.Fatalf("没读到首条用户消息: %+v", list)
	}
}

// 会话标识含路径分隔符时，不能写到目录外。
func TestFileNameSafe(t *testing.T) {
	got := FileName("../../etc/passwd")
	if strings.ContainsAny(got, "/\\") {
		t.Fatalf("文件名不能含路径分隔符: %q", got)
	}
}

func rawJSON(s string) []byte { return []byte(s) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
