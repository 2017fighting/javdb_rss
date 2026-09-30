# 多阶段构建：产出一个小体积、非 root、完全静态的单二进制镜像。
#
# 为什么是静态二进制：本服务只依赖标准库 + yaml 解析，没有任何 cgo 需求。
# 关掉 CGO 后交叉编译与跨发行版运行都不会有 libc 兼容问题。

ARG GO_VERSION=1.27

# ---------------------------------------------------------------------------
# 构建阶段
# ---------------------------------------------------------------------------
FROM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

# 先只拷依赖清单并下载 —— 这样改代码不会让依赖缓存失效。
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# VERSION 由构建时传入，会被写进 /version 端点。
# 不传时是 "dev" —— 宁可显示 dev，也不要显示一个编造的版本号。
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w -X github.com/2017fighting/javdb_rss/internal/httpapi.Version=${VERSION}" \
        -o /out/javdb-rss \
        ./cmd/javdb-rss

# ---------------------------------------------------------------------------
# 运行阶段
# ---------------------------------------------------------------------------
# 用 alpine 而不是 scratch：需要一个 shell 来在容器内 `kill -HUP` 触发配置重载，
# 也方便出问题时 exec 进去看。
FROM alpine:3.21

# ca-certificates 是**必需**的 —— 本服务要 HTTPS 访问上游，
# 没有根证书会得到 "x509: certificate signed by unknown authority"。
# tzdata 是为了 pubDate 与日志时间戳能显示本地时间。
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 -s /sbin/nologin app

COPY --from=build /out/javdb-rss /usr/local/bin/javdb-rss

# 配置与 token 从外面挂进来，不烤进镜像。
#
# /config  放 config.yaml
# /token   放从中 App 导出的 token（可选；需求 1/2/3 不需要）
#
# 这两个目录以 10001 属主创建，好让挂载的宿主目录不必改权限。
RUN mkdir -p /config /token && chown app:app /config /token

USER app
WORKDIR /config

# ⚠️ 容器里必须监听 0.0.0.0（默认的 127.0.0.1 在容器外访问不到）。
# 见 deploy/config.docker.yaml 的说明 —— 隔离由容器提供，
# 而**端口映射**才是决定它是否对局域网可见的那一步。
EXPOSE 8080

# liveness：只判断进程是否还在，**不掺上游状态**。
# 签名失效重启一千次也没用，把上游掺进来只会造成重启循环。
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

# 用 exec 形式，让 javdb-rss 成为 PID 1 直接收到 SIGTERM/SIGHUP。
# 不加 sh -c，否则信号会打在 shell 上而不是本进程。
ENTRYPOINT ["/usr/local/bin/javdb-rss"]
CMD ["-config", "/config/config.yaml"]
