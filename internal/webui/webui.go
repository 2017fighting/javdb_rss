// Package webui 持有订阅链接生成器页面的全部前端资产，并把它们作为服务
// 自己的端点端出来。
//
// 资产用 embed 打进二进制：构建与运行都**不需要 Node**，页面也不依赖任何
// CDN 或第三方脚本/字体。Tailwind 的编译产物（assets/app.css）因此入库，
// 与 `.scratch/javdb-rss-ui/shots/` 入库同一套理由 —— 评审与构建都不该先
// 装一遍工具链。
//
// 这一层只负责「把文件端出去」：它不知道页面要什么数据，也不引入任何
// 服务端状态。页面自己去读服务的发现端点（`/collected` 等），
// 与「URL 即订阅、无状态」保持一致。
package webui

import (
	"embed"
	"mime"
	"net/http"
	"path"
	"strings"
)

// indexHTML 是页面本身。用 []byte 而不是 fs.FS：它每次请求原样端出去，
// 不需要任何文件系统语义（不列目录、不猜类型、不接受路径）。
//
//go:embed index.html
var indexHTML []byte

// assets 是页面自己的 CSS/JS。
//
//go:embed assets
var assets embed.FS

// AssetsPath 是页面资产的对外前缀。它同时是 httpapi 注册路由时的那个前缀 ——
// 两处写同一个常量，免得页面里的 `<link href>` 与路由各漂各的。
const AssetsPath = "/assets/"

// Page 服务 GET /{$}：订阅链接生成器本身。
//
// 它**只**注册在精确的根路径上（Go 1.22 的 `{$}`）。用 `GET /` 当通配是
// 一个具体的坑：那会把 `/rss/want` 这类打错的 feed 路径喂成 HTML 页面，
// 而 qBittorrent 只会说「这不是一个 feed」—— 用户看到的是「服务坏了」，
// 而不是「我的 URL 少了个 .xml」。打错的 feed 必须仍是干净的 404。
func Page(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "text/html; charset=utf-8")
	// HTML 不缓存：服务升级后页面（连同它引用的 JS）要立刻跟上，
	// 否则用户会拿着旧的 JS 打新的端点。
	w.Header().Set("cache-control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexHTML)
}

// Assets 服务 GET /assets/{名字}：页面自己的 CSS/JS。
//
// 刻意不套 http.FileServer：它会给 `/assets/` 出一份**目录清单**，
// 而这里只有两个已知文件。目录清单既多余，也会让「资产里有什么」变成
// 可枚举的东西；而这个页面唯一的消费者就是它自己。
func Assets(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, AssetsPath)
	// 只接受 assets/ 底下的单层文件名。页面自己的资产没有子目录，
	// 因此出现斜杠一定是有人在探路。
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	b, err := assets.ReadFile("assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("content-type", ct)
	}
	// 资产带版本内的短缓存即可：文件名不带指纹，所以不能长缓存 ——
	// 否则升级后会有一段时间拿到旧的 JS 配新的 HTML。
	w.Header().Set("cache-control", "public, max-age=300")
	_, _ = w.Write(b)
}
