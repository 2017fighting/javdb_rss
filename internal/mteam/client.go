package mteam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 三种**重试无用**的失败。它们分开的理由都一样：处置方式与别的失败不同。
//
// 用哨兵错误而不是一个带 Action 字段的错误类型：调用方要判定的就是「是不是这一类」，
// 而 `errors.Is` 已经把这件事故障安全地表达完了。
var (
	// ErrCredential 表示 API key 无效、过期，或者压根没配。
	// 处置是**去控制台换一个**，不是重试。
	ErrCredential = errors.New("M-Team 凭据无效")
	// ErrBlocked 表示请求被 Cloudflare 按 User-Agent 拦掉了。
	//
	// 它的表现是 302 到 www.google.com —— 既不像鉴权失败，也不像网络故障，
	// 所以必须单独成一类：不分开的话排查会一路往「key 是不是过期了」错下去。
	ErrBlocked = errors.New("M-Team 请求被 Cloudflare 拦掉（302）")
	// ErrLinkExpired 表示下载链接过期。
	//
	// 实测链接生成后约 5 分钟失效。正常流程（生成完立刻取）不会碰到，
	// 因此碰到就说明别处出了问题 —— 它不是「重试一下就好」的那类。
	ErrLinkExpired = errors.New("M-Team 下载链接已过期")
)

const (
	// defaultBaseURL 是 M-Team 的 API 入口。站点域名把第一个 label 换成 api 就是它。
	defaultBaseURL = "https://api.m-team.cc"
	// originHeader 是站点对 CORS 的校验要求（先例项目都带它）。
	originHeader = "https://kp.m-team.cc/"
	// userAgent 是本服务对 M-Team 的 User-Agent。
	//
	// ⚠️ 它**必须非空、且不能含 `curl`**：实测 Cloudflare 会把这两类请求
	// 302 到 www.google.com。Go 的默认 UA 恰好能过，但「恰好能过」不是设计 ——
	// 显式写一个自己的，免得哪天换了 HTTP 栈就莫名其妙被拦。
	userAgent = "javdb-rss"
	// searchPageSize 是搜索的每页条数（先例项目都用 100）。
	searchPageSize = 100
	// maxBody 是读响应体的上限。种子本身可以到几百 KB，给足余量；
	// 它存在的意义是别让一个坏响应把内存吃光。
	maxBody = 16 << 20
)

// Client 是 M-Team 的 HTTP 客户端。
type Client struct {
	// BaseURL 是 API 入口。留空用 defaultBaseURL。
	BaseURL string
	// APIKey 是【控制台 → 實驗室 → 存取令牌】取到的存取令牌。
	APIKey string
	// HTTP 可注入（测试用）。为 nil 时用默认客户端。
	HTTP *http.Client
}

// SearchHit 是搜索结果里的一行 —— 只保留本服务要用的字段。
//
// 它刻意不是 catalog.Magnet：那边没有 torrent id，而**下载种子**要它。
// 投影成领域模型是调用方的事。
type SearchHit struct {
	// ID 是种子在馒头上的 id。它只用来下载 —— 身份（guid）用的是 infohash。
	ID string
	// Name 是种子名（通常是番号开头的一长串）。
	Name string
	// SizeBytes 是体积。线格式是**字符串**，解析失败时退化为 0 ——
	// 它只用于 feed 的 enclosure length（qBittorrent 不拿它做判断），
	// 不该让整条 feed 挂掉。
	SizeBytes int64
	// CNSub 表示这条带中文字幕。
	//
	// 判据是 `labelsNew` 里含「中字」。⚠️ **不要去读 detail 的 `hasChineseSubtitle`** ——
	// 实测它在 labelsNew 明明写着「中字」时仍然是 false（一个看起来权威、实际不填的字段）。
	CNSub bool
	// CreatedAt 是线上传时间，**原样保留**（线格式形如 "2021-02-22 15:19:51"）。
	CreatedAt string
	// Seeders 是做种数。解析失败退化为 0。
	Seeders int
}

// Search 按关键词搜一次（`mode` 固定 adult，一页 searchPageSize 条）。
func (c *Client) Search(ctx context.Context, keyword string) ([]SearchHit, error) {
	body, err := json.Marshal(map[string]any{
		"mode":       "adult",
		"keyword":    keyword,
		"pageNumber": 1,
		"pageSize":   searchPageSize,
		"visible":    1,
	})
	if err != nil {
		return nil, fmt.Errorf("构造搜索请求: %w", err)
	}

	// out 收的是**信封里的 data**，不是整个信封 —— 信封由 do 自己校验。
	var data struct {
		Data []struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Size        string   `json:"size"`
			LabelsNew   []string `json:"labelsNew"`
			CreatedDate string   `json:"createdDate"`
			Status      struct {
				Seeders string `json:"seeders"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := c.do(ctx, "/api/torrent/search", "application/json", bytes.NewReader(body), &data); err != nil {
		return nil, err
	}

	rows := data.Data
	hits := make([]SearchHit, 0, len(rows))
	for _, r := range rows {
		size, _ := strconv.ParseInt(r.Size, 10, 64)
		seeders, _ := strconv.Atoi(r.Status.Seeders)
		hits = append(hits, SearchHit{
			ID:        r.ID,
			Name:      r.Name,
			SizeBytes: size,
			CNSub:     hasChineseSubtitle(r.LabelsNew),
			CreatedAt: r.CreatedDate,
			Seeders:   seeders,
		})
	}
	return hits, nil
}

// hasChineseSubtitle 判定中文字幕。
//
// 只认标签里含「中字」这一条 —— 不解析文件名、不看那个不填的 hasChineseSubtitle。
func hasChineseSubtitle(labels []string) bool {
	for _, l := range labels {
		if strings.Contains(l, "中字") {
			return true
		}
	}
	return false
}

// Download 取回某个种子的字节：先换一个下载链接，再取它。
//
// 两步合在一起，是因为调用方要的就是「这份种子」——
// 中间那个链接是**五分钟就过期**的实现细节，暴露出去只会让调用方有机会存错东西。
func (c *Client) Download(ctx context.Context, id string) ([]byte, error) {
	form := url.Values{"id": {id}}
	var link string
	// ⚠️ 这一步要 **form**，不是 JSON —— 给 JSON 会得到「參數錯誤」。
	// 站点自己的 wiki 就承认文档里 json/form 的说明不可信。
	if err := c.do(ctx, "/api/torrent/genDlToken",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()), &link); err != nil {
		return nil, err
	}
	if link == "" {
		return nil, errors.New("genDlToken 没有给出下载链接")
	}
	return c.fetch(ctx, link)
}

// fetch 取下载链接指向的字节。
//
// 单独一个方法而不是复用 do，有两处**必须**不同：
//
//  1. 它返回的不是 JSON；
//  2. ⭐ 它**跟随重定向**，而 API 端点不跟。
//
// 第 2 条是实测踩出来的：genDlToken 给的链接会 302 到一个 CDN
// （`fr1.halomt.com?playload=…`），真正的种子在那一跳之后。
// 而 API 端点恰好相反 —— 那边任何 302 都是 Cloudflare 按 UA 拦人。
// 两边共用一个客户端的话，必然有一边是错的。
func (c *Client) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造取种子的请求: %w", err)
	}
	c.setHeaders(req, "")

	resp, err := c.cdnClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("取种子: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := classifyStatus(resp); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("读种子: %w", err)
	}
	// 种子是 bencode，以 'd' 开头；错误信封是 JSON，以 '{' 开头。
	// 用这个区分而不是看 Content-Type：实测过期时 Content-Type 仍然是
	// application/x-bittorrent，只有体变了。
	if len(raw) > 0 && raw[0] == '{' {
		if err := classifyEnvelope(raw); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("取种子失败：上游返回了 JSON 而不是种子：%s", snippet(raw))
	}
	if len(raw) == 0 {
		return nil, errors.New("取种子失败：上游返回了空响应")
	}
	return raw, nil
}

// do 发一次 POST，把成功响应的 data 解进 out。
func (c *Client) do(ctx context.Context, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+path, body)
	if err != nil {
		return fmt.Errorf("构造请求 %s: %w", path, err)
	}
	c.setHeaders(req, contentType)

	resp, err := c.apiClient().Do(req)
	if err != nil {
		return fmt.Errorf("请求 %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := classifyStatus(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("读 %s 的响应: %w", path, err)
	}
	if err := classifyEnvelope(raw); err != nil {
		return err
	}

	// 只解 data：信封本身已经校验过了，再解一遍是多余的。
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s 的响应不是合法 JSON: %w（前 120 字节：%s）", path, err, snippet(raw))
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("%s 的 data 形状不认识: %w", path, err)
	}
	return nil
}

// setHeaders 装上每个请求都要带的头。
//
// ⚠️ 三样都不能少：`x-api-key` 是鉴权，`User-Agent` 是躲 WAF（见 userAgent），
// `Origin` 是站点对 CORS 的校验。
func (c *Client) setHeaders(req *http.Request, contentType string) {
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", originHeader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
}

// classifyStatus 把 HTTP 状态码映射成错误。
func classifyStatus(resp *http.Response) error {
	// ⚠️ 重定向单独成一类：M-Team 的 API 从不合法地重定向，
	// 而实测的 302 是 Cloudflare 按 UA 拦人的表现（Location 指向 www.google.com）。
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("%w：HTTP %d，Location=%q", ErrBlocked, resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("M-Team 返回 HTTP %d", resp.StatusCode)
	}
	return nil
}

// classifyEnvelope 检查响应信封，并在失败时分类。
//
// ⚠️ **失败也是 HTTP 200。** 实测 key 无效时是 `200 + {"code":"1","message":"key無效"}`，
// 完全没带 key 时是 `200 + {"code":"401",…}`。只看状态码的实现会把它当成功，
// 然后在解析 data 时莫名其妙地炸 —— 这就是这里必须看 code 的理由。
func classifyEnvelope(raw []byte) error {
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("M-Team 的响应不是合法 JSON: %w（前 120 字节：%s）", err, snippet(raw))
	}
	if env.Code == "0" {
		return nil
	}

	// 分类靠 message 的原话 —— 这是唯一可用的信号，因此**认不出来的一律
	// 落到普通错误**：猜错分类比不分类更坏（告警会说「去换 key」而真相是别的）。
	switch {
	case env.Code == "401", strings.Contains(env.Message, "key無效"):
		return fmt.Errorf("%w：%s", ErrCredential, env.Message)
	case strings.Contains(env.Message, "超出有效期"), strings.Contains(env.Message, "不可用"):
		return fmt.Errorf("%w：%s", ErrLinkExpired, env.Message)
	default:
		return fmt.Errorf("M-Team 返回失败（code=%s）：%s", env.Code, env.Message)
	}
}

// base 返回去掉了末尾斜杠的入口地址。
func (c *Client) base() string {
	if b := strings.TrimRight(c.BaseURL, "/"); b != "" {
		return b
	}
	return defaultBaseURL
}

// apiClient 返回**API 端点**要用的客户端。
//
// ⚠️ **必须禁掉自动重定向**：Go 的默认行为是**跟随** 302，而那会把 Cloudflare 的
// 拦截变成「我们拿到了 google 的 200 页面」，于是 ErrBlocked 这条判据永远不会触发，
// 排查方向也就跟着错掉。注入的客户端（测试）也要遵守这一条，因此在这里覆写。
func (c *Client) apiClient() *http.Client {
	cp := c.baseClient()
	cp.CheckRedirect = noRedirect
	return cp
}

// cdnClient 返回**下载链接**要用的客户端：它跟随重定向（最多 10 跳，Go 的默认）。
//
// 理由见 fetch：那一跳是 CDN，不是拦截。
func (c *Client) cdnClient() *http.Client {
	cp := c.baseClient()
	cp.CheckRedirect = nil // nil = 用 net/http 的默认策略（最多 10 跳）
	return cp
}

// baseClient 是两者共用的基底。
func (c *Client) baseClient() *http.Client {
	if c.HTTP == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	cp := *c.HTTP
	return &cp
}

// noRedirect 让 3xx 原样返回，而不是被跟随。
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// snippet 截一小段响应体用于报错。
//
// 它存在的场景很具体：被 WAF 拦下来时响应是 HTML，而「不是合法 JSON」这句话
// 单独看不出发生了什么 —— 带上开头几十字节，人一眼就知道拿到的是网页。
func snippet(raw []byte) string {
	const n = 120
	s := strings.TrimSpace(string(raw))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
