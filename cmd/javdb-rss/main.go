// Command javdb-rss 把 JavDB 官方 App 的订阅渲染成 qBittorrent 可订阅的 RSS。
//
// 用法：
//
//	javdb-rss -config config.yaml
//
// 收到 SIGHUP 会重载配置（失败则保留旧配置）；收到 SIGINT/SIGTERM 优雅退出。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/config"
	"github.com/2017fighting/javdb_rss/internal/httpapi"
	"github.com/2017fighting/javdb_rss/internal/stub"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath = flag.String("config", "config.yaml", "配置文件路径")
		logLevel   = flag.String("log-level", "info", "日志级别: debug|info|warn|error")
		showVer    = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(httpapi.Version)
		return nil
	}

	log := newLogger(*logLevel)
	slog.SetDefault(log)

	holder, err := config.NewHolder(*configPath)
	if err != nil {
		return err
	}
	cfg := holder.Current()

	src, err := buildSource(cfg)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           httpapi.New(holder, src, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 信号处理：SIGHUP 重载配置，INT/TERM 触发退出。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	go func() {
		for range hup {
			if err := holder.Reload(); err != nil {
				// 保留旧配置继续服务 —— 一个手滑写坏的 YAML 不该让实例失去配置。
				log.Error("重载配置失败，继续使用旧配置", "path", holder.Path(), "err", err)
				continue
			}
			log.Info("配置已重载", "path", holder.Path(), "listen", holder.Current().Listen)
		}
	}()

	errc := make(chan error, 1)
	go func() {
		log.Info("开始监听", "addr", cfg.Listen, "provider", cfg.Provider, "version", httpapi.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return fmt.Errorf("监听 %s: %w", cfg.Listen, err)
	case <-ctx.Done():
		log.Info("收到退出信号，正在关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭服务: %w", err)
	}
	return nil
}

// buildSource 根据配置装配数据源。
//
// provider: appapi 目前**直接报错**而不是静默退回假数据：
// 「配了真实源却拿到固定数据」是那种能瞒很久的故障，宁可启动失败。
func buildSource(cfg *config.Config) (catalog.Source, error) {
	switch cfg.Provider {
	case config.ProviderStub:
		slog.Default().Warn("正在使用固定数据源 stub —— 不会访问任何网络，仅用于跑通链路")
		return &stub.Source{}, nil
	case config.ProviderAppAPI:
		// 装配真实数据源需要两件还没定的东西：
		//   - 签名实现（ticket 02：直接依赖 javdb-cli 还是把算法拷进本仓库）
		//   - 番号 → 作品 的解析规则（ticket 06）
		return nil, fmt.Errorf("provider=appapi 尚未实现，见 ticket 02 与 ticket 06；当前请使用 provider=stub")
	default:
		return nil, fmt.Errorf("未知的 provider: %q", cfg.Provider)
	}
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	// 结构化输出到 stdout：容器与 systemd 都能直接收，且便于后续接日志系统。
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}
