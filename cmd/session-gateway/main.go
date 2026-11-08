// Command session-gateway 代理模型请求并把对话存档，同时提供只读的
// MCP 工具让 agent 在存档上跑 bash。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/myflavor/session-gateway/internal/bash"
	"github.com/myflavor/session-gateway/internal/config"
	"github.com/myflavor/session-gateway/internal/mcp"
	"github.com/myflavor/session-gateway/internal/proxy"
	"github.com/myflavor/session-gateway/internal/session"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}
	if err := run(); err != nil {
		slog.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	store, err := session.New(cfg.DataDir)
	if err != nil {
		return err
	}

	// BASH_USER 为空时用当前用户（本地开发，读写都行）。
	runner, err := bash.New(cfg.DataDir, os.Getenv("BASH_USER"))
	if err != nil {
		return err
	}
	if runner.Cred != nil {
		log.Info("bash runs as reader", "uid", runner.Cred.Uid, "gid", runner.Cred.Gid)
	} else {
		log.Info("bash runs as current user (read-write)", "dir", cfg.DataDir)
	}

	px := proxy.New(cfg, store)
	mcpSrv := mcp.New(cfg.KeyList, runner, store)
	mcpHandler := requireKey(cfg, mcpSrv.Handler())

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           route(px, mcpHandler),
		ReadHeaderTimeout: 20 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen, "proxies", len(cfg.ProxyList), "keys", len(cfg.KeyList))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func route(px *proxy.Proxy, mcpHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/mcp"):
			mcpHandler.ServeHTTP(w, r)
		case r.URL.Path == "/healthz":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "ok")
		default:
			px.ServeHTTP(w, r, http.NotFoundHandler())
		}
	})
}

// requireKey 在 HTTP 层校验，未鉴权的请求进不到 MCP。
func requireKey(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get("x-mem-key"))
		if key == "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
				key = strings.TrimSpace(h[7:])
			}
		}
		if !cfg.KeyAllowed(key) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"invalid key"}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}
