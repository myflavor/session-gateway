// Package proxy 把 /{path}/... 转发到对应上游，旁路把对话追加进存档。
package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/myflavor/session-gateway/internal/config"
	"github.com/myflavor/session-gateway/internal/session"
)

type Proxy struct {
	cfg   *config.Config
	store *session.Store
}

func New(cfg *config.Config, store *session.Store) *Proxy {
	return &Proxy{cfg: cfg, store: store}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.Handler) {
	seg, rest, ok := splitPath(r.RequestURI)
	if !ok {
		next.ServeHTTP(w, r)
		return
	}
	up := p.cfg.Proxy(seg)
	if up == nil {
		next.ServeHTTP(w, r)
		return
	}

	target, err := url.Parse(strings.TrimRight(up.Origin, "/"))
	if err != nil {
		http.Error(w, `{"error":"上游配置无效"}`, http.StatusBadGateway)
		return
	}

	var reqBody []byte
	if r.Body != nil {
		reqBody, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = rest
			pr.Out.URL.RawQuery = r.URL.RawQuery
			pr.Out.Host = target.Host
			stripHopByHop(pr.Out.Header)
		},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			p.record(up.Path, r, reqBody, resp)
			return nil
		},
	}
	rp.ServeHTTP(w, r)
}

func (p *Proxy) record(path string, r *http.Request, reqBody []byte, resp *http.Response) {
	sid := sessionID(r, reqBody)
	if sid == "" {
		return
	}

	if len(reqBody) > 0 {
		var req struct {
			Messages []json.RawMessage `json:"messages"`
			Model    string            `json:"model"`
		}
		if json.Unmarshal(reqBody, &req) == nil {
			// 只记最后一条用户消息，前面的都是历史，已经记过
			for i := len(req.Messages) - 1; i >= 0; i-- {
				var m struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				}
				if json.Unmarshal(req.Messages[i], &m) != nil || m.Role != "user" {
					continue
				}
				p.store.Append(sid, session.Message{
					Role: "user", Content: m.Content, Model: req.Model,
					SessionID: sid, Path: path,
				})
				break
			}
		}
	}

	if resp == nil || resp.Body == nil {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))

	text := assistantText(body, resp.Header.Get("Content-Type"))
	if text != "" {
		p.store.Append(sid, session.Message{
			Role: "assistant", Content: mustJSON(text), SessionID: sid, Path: path,
		})
	}
}

func assistantText(body []byte, contentType string) string {
	if strings.Contains(contentType, "text/event-stream") || bytes.HasPrefix(bytes.TrimSpace(body), []byte("data:")) {
		var b strings.Builder
		for _, line := range bytes.Split(body, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			payload := bytes.TrimSpace(line[len("data:"):])
			if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
				continue
			}
			var ev struct {
				Type  string `json:"type"`
				Delta struct {
					Text string `json:"text"`
				} `json:"delta"`
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if json.Unmarshal(payload, &ev) != nil {
				continue
			}
			b.WriteString(ev.Delta.Text)
			for _, c := range ev.Choices {
				b.WriteString(c.Delta.Content)
			}
		}
		return strings.TrimSpace(b.String())
	}

	var obj struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &obj) != nil {
		return ""
	}
	var parts []string
	for _, c := range obj.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	for _, c := range obj.Choices {
		if c.Message.Content != "" {
			parts = append(parts, c.Message.Content)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func mustJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func sessionID(r *http.Request, reqBody []byte) string {
	for _, h := range []string{"X-Session-Id", "X-Claude-Code-Session-Id", "X-OpenCode-Session", "X-ZCode-Session-Id"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	var req struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
		System json.RawMessage `json:"system"`
	}
	if json.Unmarshal(reqBody, &req) == nil {
		if req.Metadata.UserID != "" {
			return req.Metadata.UserID
		}
	}
	// 没有声明会话时按日期分文件，避免全部混进一个巨型文件
	return "undated-" + time.Now().UTC().Format("2006-01-02")
}

func splitPath(uri string) (seg, rest string, ok bool) {
	if !strings.HasPrefix(uri, "/") {
		return "", "", false
	}
	uri = uri[1:]
	i := strings.IndexByte(uri, '/')
	if i < 0 {
		return uri, "/", uri != ""
	}
	return uri[:i], uri[i:], uri[:i] != ""
}

func stripHopByHop(h http.Header) {
	for _, k := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		h.Del(k)
	}
}
