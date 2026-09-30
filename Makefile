# 常用开发与构建命令。
#
# 目标：把「怎么构建、怎么测、怎么跑」固定下来，避免每次都靠记忆敲一长串参数。

BINARY      := javdb-rss
PKG         := ./cmd/javdb-rss
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X github.com/2017fighting/javdb_rss/internal/httpapi.Version=$(VERSION)
IMAGE       ?= javdb-rss:$(VERSION)

.DEFAULT_GOAL := help

.PHONY: help
help: ## 显示所有可用目标
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## 构建静态单二进制（版本取自 git describe）
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

.PHONY: run
run: ## 用 config.yaml 本地跑起来
	go run $(PKG) -config config.yaml

.PHONY: test
test: ## 跑全部测试（离线，不访问网络）
	go test -count=1 ./...

.PHONY: race
race: ## 带竞态检测跑测试
	go test -count=1 -race ./...

.PHONY: cover
cover: ## 生成覆盖率报告
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt: ## 格式化
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## 检查格式化（CI 用）
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "以下文件未格式化:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## 静态分析
	go vet ./...

.PHONY: check
check: fmt-check vet test ## 提交前的完整检查

.PHONY: docker-build
docker-build: ## 构建容器镜像
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

.PHONY: docker-run
docker-run: ## 本地跑容器（只绑 loopback，配置取自 deploy/）
	docker run --rm -p 127.0.0.1:8080:8080 \
		-v "$(PWD)/deploy/config.docker.yaml:/config/config.yaml:ro" \
		$(IMAGE)

.PHONY: clean
clean: ## 清理构建产物
	rm -f $(BINARY) coverage.out
