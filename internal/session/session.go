package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Message struct {
	TS        time.Time       `json:"ts"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content,omitempty"`
	Model     string          `json:"model,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Path      string          `json:"path,omitempty"`
}

type Store struct {
	dir string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(dir string) (*Store, error) {
	// 只检查不创建：目录由 entrypoint 建好并设成 writer 可写、reader 只读，
	// 进程自己 mkdir 会把权限设成运行用户，破坏这套边界
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("session dir unavailable: %w", err)
	}
	return &Store{dir: dir, locks: map[string]*sync.Mutex{}}, nil
}

func (s *Store) Dir() string { return s.dir }

func FileName(sessionID string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return -1
	}, sessionID)
	if len(clean) > 40 {
		clean = clean[:40]
	}
	sum := sha256.Sum256([]byte(sessionID))
	return clean + "-" + hex.EncodeToString(sum[:4]) + ".jsonl"
}

// Append 用 O_APPEND 而不是读-改-写：同一会话可能有多个请求并发写入。
func (s *Store) Append(sessionID string, m Message) error {
	name := FileName(sessionID)
	lock := s.lockFor(name)
	lock.Lock()
	defer lock.Unlock()

	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if m.TS.IsZero() {
		m.TS = time.Now().UTC()
	}
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Store) lockFor(name string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[name]
	if !ok {
		l = &sync.Mutex{}
		s.locks[name] = l
	}
	return l
}

type Session struct {
	File      string    `json:"file"`
	SessionID string    `json:"session_id"`
	First     string    `json:"first"`
	Lines     int       `json:"lines"`
	Bytes     int64     `json:"bytes"`
	Started   time.Time `json:"started"`
	Updated   time.Time `json:"updated"`
}

func (s *Store) List() ([]Session, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(s.dir, e.Name())
		sess := Session{
			File:    e.Name(),
			Lines:   countLines(path),
			Bytes:   info.Size(),
			Updated: info.ModTime(),
		}
		sess.SessionID, sess.First, sess.Started = head(path)
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	n := 0
	for sc.Scan() {
		n++
	}
	return n
}

func head(path string) (sessionID, first string, started time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", time.Time{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var m Message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if started.IsZero() {
			started = m.TS
		}
		if sessionID == "" {
			sessionID = m.SessionID
		}
		if first == "" && m.Role == "user" {
			first = clip(textOf(m.Content), 80)
		}
		if sessionID != "" && first != "" {
			break
		}
	}
	return sessionID, first, started
}

func textOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
