// Package mcp exposes session_bash and session_list.
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
			"Search past coding sessions. Use session_bash to run bash over the archive: "+
				"grep for content, ls to list sessions, sed/jq to read details.",
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
			"Run bash over the session archive. The archive dir is the working directory; "+
				"it holds one .jsonl per session, one message per line: "+
				"{\"ts\",\"role\",\"content\",\"model\",\"session_id\",\"path\"}.\n\n"+
				"Typical use:\n"+
				"  ls -lt | head             what was worked on recently\n"+
				"  grep -rl 'keyword' .      which sessions mention it\n"+
				"  grep -n 'keyword' x.jsonl locate the line\n"+
				"  sed -n '120,160p' x.jsonl read that region\n"+
				"  jq -r '.content' x.jsonl  content only\n\n"+
				"Output is capped at 256KB and 20s; exit code 1 (grep no match) is not an error."),
		mcp.WithString("cmd",
			mcp.Required(),
			mcp.Description("bash command; pipes are allowed"),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	)
}

func listTool() mcp.Tool {
	return mcp.NewTool("session_list",
		mcp.WithDescription(
			"List archived sessions: file name, first user message, line count, size, "+
				"and last update. More structured than `ls` via session_bash."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	)
}

func (s *Server) handleBash(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	script := req.GetString("cmd", "")
	if strings.TrimSpace(script) == "" {
		return mcp.NewToolResultError("cmd is empty"), nil
	}
	out, err := s.runner.Run(ctx, script)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if strings.TrimSpace(out) == "" {
		return mcp.NewToolResultText("(no output)"), nil
	}
	return mcp.NewToolResultText(out), nil
}

func (s *Server) handleList(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	list, err := s.store.List()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(list) == 0 {
		return mcp.NewToolResultText("(archive is empty)"), nil
	}
	var b strings.Builder
	for _, s := range list {
		fmt.Fprintf(&b, "%s\n  %s\n  %d lines · %s · %s\n",
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
