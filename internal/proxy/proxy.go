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
		http.Error(w, `{"error":"invalid upstream origin"}`, http.StatusBadGateway)
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
		},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			go p.recordRequest(up.Path, r, reqBody)
			// 边转发边累积，不能先 ReadAll：那会把整个流式响应缓冲住，
			// 客户端要等上游全部结束才能看到第一个字节。
			if resp.Body != nil {
				resp.Body = &capture{
					rc: resp.Body,
					// 回调发生在读取过程中，写盘不能挡在转发路径上
					onEOF: func(b []byte) {
						go p.recordResponse(up.Path, r, b, resp.Header.Get("Content-Type"))
					},
				}
			}
			return nil
		},
	}
	rp.ServeHTTP(w, r)
}

func (p *Proxy) recordRequest(path string, r *http.Request, reqBody []byte) {
	sid := sessionID(r)
	if sid == "" || len(reqBody) == 0 {
		return
	}
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Model string `json:"model"`
	}
	if json.Unmarshal(reqBody, &req) != nil {
		return
	}
	// 只记最后一条用户消息，前面的都是历史，已经记过
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if m := req.Messages[i]; m.Role == "user" {
			p.store.Append(sid, session.Message{
				Role: "user", Content: m.Content, Model: req.Model, SessionID: sid, Path: path,
			})
			return
		}
	}
}

func (p *Proxy) recordResponse(path string, r *http.Request, body []byte, contentType string) {
	sid := sessionID(r)
	if sid == "" {
		return
	}
	if text := assistantText(body, contentType); text != "" {
		p.store.Append(sid, session.Message{
			Role: "assistant", Content: mustJSON(text), SessionID: sid, Path: path,
		})
	}
}

// capture 把读到的字节顺带留一份，读完时回调一次。
//
// 上限 4MB：超长的工具输出只会让累积提前停止，不影响转发本身。
type capture struct {
	rc    io.ReadCloser
	buf   bytes.Buffer
	onEOF func([]byte)
	fired bool
}

const captureLimit = 4 << 20

func (c *capture) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 && c.buf.Len() < captureLimit {
		room := captureLimit - c.buf.Len()
		if n < room {
			room = n
		}
		c.buf.Write(p[:room])
	}
	if err != nil && !c.fired {
		c.fired = true
		c.onEOF(c.buf.Bytes())
	}
	return n, err
}

func (c *capture) Close() error { return c.rc.Close() }

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

// sessionID 只认客户端明确声明的会话头。X-Session-Id 之类的泛化头被很多
// 网关当作逐请求追踪 id，认它会把一次对话拆成几十个会话。
func sessionID(r *http.Request) string {
	for _, h := range []string{
		"X-Claude-Code-Session-Id",
		"X-ZCode-Session-Id",
		"X-OpenCode-Session",
	} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
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
