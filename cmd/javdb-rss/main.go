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
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/config"
	"github.com/2017fighting/javdb_rss/internal/dedupe"
	"github.com/2017fighting/javdb_rss/internal/health"
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

	src, err := buildSource(cfg, holder)
	if err != nil {
		return err
	}

	tracker := health.NewTracker()

	// 信号处理：SIGHUP 重载配置，INT/TERM 触发退出。
	// 必须在启动探针 goroutine 之前建好，因为它需要同一个 ctx 来退出。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	// 上游健康探针。只检查「签名常量与服务端是否还兼容」，
	// 因此不需要 token，也与 provider 是否已实现无关。
	go health.Run(ctx, upstreamChecker{holder: holder}, tracker,
		func() time.Duration { return holder.Current().AppAPI.ProbeInterval }, log)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           httpapi.New(holder, src, log).WithUpstream(tracker).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

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
// 返回的是「每次调用都重读配置」的包装，而不是一个固化了 host/token 的客户端 ——
// 这样 SIGHUP 改了 host / lang / device_uuid / token 能立即生效。
func buildSource(cfg *config.Config, holder *config.Holder) (catalog.Source, error) {
	switch cfg.Provider {
	case config.ProviderStub:
		slog.Default().Warn("正在使用固定数据源 stub —— 不会访问任何网络，仅用于跑通链路")
		return &stub.Source{}, nil
	case config.ProviderAppAPI:
		// 包一层 dedupe：合并并发的相同请求，只打一次上游。
		// 它不存任何东西（不是缓存），因此不返回陈旧数据、重启无影响。
		return dedupe.New(&appapiSource{holder: holder}), nil
	default:
		return nil, fmt.Errorf("未知的 provider: %q", cfg.Provider)
	}
}

// appapiSource 把真正的 App API 客户端适配成 catalog.Source。
//
// 它实现了 catalog.Source 但**不持有任何状态** —— 每次请求都按当前配置
// 构造一个客户端。相比「启动时建好一个长命客户端」，代价是每次请求多建几个
// 小对象（含一个 http.Client）；换来的是配置热重载对所有字段都生效，
// 而且不会出现「改了 token 却还在用旧的」这类难查的问题。
//
// 它也需要一个 config.Holder，因此与 main 里其他装配保持一致。
type appapiSource struct {
	holder *config.Holder
}

func (s *appapiSource) client() (*appapi.Client, error) {
	ac := s.holder.Current().AppAPI

	identity := appapi.DefaultIdentity()
	if ac.DeviceUUID != "" {
		identity.DeviceUUID = ac.DeviceUUID
	}

	token, err := config.LoadToken(ac.TokenFile)
	if err != nil {
		return nil, err
	}

	return &appapi.Client{
		Host:     ac.Host,
		Token:    token,
		Identity: identity,
		Signer:   appapi.NewSigner(),
		Lang:     ac.Lang,
		// 并行拉磁链。串行时一个 50 部的女优页要 6.75s，
		// 并发 8 降到 1.30s（上游本身只需 ~218ms，瓶颈在我们自己）。
		MagnetConcurrency: ac.MagnetConcurrency,
	}, nil
}

func (s *appapiSource) Code(ctx context.Context, code string) ([]catalog.Work, error) {
	c, err := s.client()
	if err != nil {
		return nil, err
	}
	return c.Code(ctx, code)
}

func (s *appapiSource) Actress(ctx context.Context, id string, params url.Values) ([]catalog.Work, error) {
	c, err := s.client()
	if err != nil {
		return nil, err
	}
	return c.Actress(ctx, id, params)
}

var _ catalog.Source = (*appapiSource)(nil)

// upstreamChecker 在每次检查时**重新读取配置**构造客户端。
//
// 不在启动时建好一个长命客户端，是因为 host / lang / device_uuid 都是可热重载的；
// 提前固化会让「改了配置却不生效」变成一个很难查的问题。
// 一次探针的开销是一次 HTTP 请求，多构几个临时对象不算代价。
type upstreamChecker struct{ holder *config.Holder }

func (c upstreamChecker) Check(ctx context.Context) health.Result {
	ac := c.holder.Current().AppAPI

	identity := appapi.DefaultIdentity()
	if ac.DeviceUUID != "" {
		identity.DeviceUUID = ac.DeviceUUID
	}

	cl := &appapi.Client{
		Host:     ac.Host,
		Identity: identity,
		Signer:   appapi.NewSigner(),
		Lang:     ac.Lang,
		// 并行拉磁链。串行时一个 50 部的女优页要 6.75s，
		// 并发 8 降到 1.30s（上游本身只需 ~218ms，瓶颈在我们自己）。
		MagnetConcurrency: ac.MagnetConcurrency,
		// 刻意不带 token：/api/v1/startup 是匿名端点，
		// 带上 token 只会让「token 过期」污染「签名是否有效」这个信号。
	}

	// 把 error 翻译成结构化结论。做在这一层而不是 appapi 里，
	// 是为了让 appapi 保持不知道 health 包的存在。
	if err := cl.Check(ctx); err != nil {
		return health.Result{OK: false, Action: appapi.ActionOf(err), Err: err.Error()}
	}
	return health.Result{OK: true}
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
