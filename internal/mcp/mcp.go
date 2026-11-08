// Package mcp 暴露 session_bash 与 session_list 两个只读工具。
package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/myflavor/session-gateway/internal/bash"
	"github.com/myflavor/session-gateway/internal/session"
)

type Server struct {
	keys   map[string]bool
	runner *bash.Runner
	store  *session.Store
}

func New(keys []string, runner *bash.Runner, store *session.Store) *Server {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return &Server{keys: set, runner: runner, store: store}
}

func (s *Server) Handler() *server.StreamableHTTPServer {
	srv := server.NewMCPServer("session-gateway", "1.0.0",
		server.WithToolCapabilities(false),
		server.WithInstructions(
			"会话存档检索。用 session_bash 在存档上执行只读 bash 命令："+
				"grep 搜内容、ls 看会话、sed/jq 读细节。",
		),
	)
	srv.AddTool(bashTool(), s.handleBash)
	srv.AddTool(listTool(), s.handleList)
	return server.NewStreamableHTTPServer(srv,
		server.WithEndpointPath("/mcp"),
		server.WithStateLess(true),
	)
}

func bashTool() mcp.Tool {
	return mcp.NewTool("session_bash",
		mcp.WithDescription(
			"在会话存档上执行 bash 命令。存档目录就是工作目录，里面是一个个 .jsonl 文件，"+
				"每行一条消息：{\"ts\",\"role\",\"content\",\"session_id\"}。\n\n"+
				"常用：\n"+
				"  ls -lt | head             最近在聊什么\n"+
				"  grep -rl '关键词' .        哪些会话提到过\n"+
				"  grep -n '关键词' x.jsonl   定位到行号\n"+
				"  sed -n '120,160p' x.jsonl  读某一段\n"+
				"  jq -r '.content' x.jsonl   只取正文\n\n"+
				"写操作会被拒绝。输出上限 256KB，超时 20 秒。"),
		mcp.WithString("cmd",
			mcp.Required(),
			mcp.Description("要执行的 bash 命令，可以用管道"),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	)
}

func listTool() mcp.Tool {
	return mcp.NewTool("session_list",
		mcp.WithDescription(
			"列出所有会话存档：文件名、首条用户消息、行数、起止时间。"+
				"比 session_bash 的 `ls` 更结构化，用来快速了解「有哪些对话」。"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	)
}

func (s *Server) handleBash(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	script := req.GetString("cmd", "")
	if strings.TrimSpace(script) == "" {
		return mcp.NewToolResultError("cmd 不能为空"), nil
	}
	out, err := s.runner.Run(ctx, script)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if strings.TrimSpace(out) == "" {
		return mcp.NewToolResultText("(无输出)"), nil
	}
	return mcp.NewToolResultText(out), nil
}

func (s *Server) handleList(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	list, err := s.store.List()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(list) == 0 {
		return mcp.NewToolResultText("(存档为空)"), nil
	}
	var b strings.Builder
	for _, s := range list {
		fmt.Fprintf(&b, "%s\n  %s\n  %d 行 · %s · %s\n",
			s.File, s.First, s.Lines, humanBytes(s.Bytes), s.Updated.Local().Format("2006-01-02 15:04"))
	}
	return mcp.NewToolResultText(b.String()), nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
