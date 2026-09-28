// Package appapi 是 JavDB 官方 App 私有 API 的传输层。
//
// 这里刻意**只放已经在 2026-09-28 实测确认过的传输事实**，不放任何业务规则：
//
//   - `jdsignature` 是 **HTTP 请求头**（不是查询参数）
//   - 每次请求必须带 8 个公共查询参数，缺一即 ParameterInvalid
//   - 响应信封为 {success, action, message, data}
//
// 两处未知被隔离成接口，让它们可以各自演化而不污染这一层：
//
//   - 签名算法 → Signer（归 ticket 02；Prefix 源自 App 内 access key，将来可能失效）
//   - 番号 → 作品 的解析规则 → 由调用方在更高层实现（归 ticket 06）
package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Identity 是每次请求都要带上的设备与版本信息。
//
// 这 8 个字段全部经实测确认：少任何一个，服务端返回
// `ParameterInvalid: Parameter cannot be empty: <第一个缺的字段>`。
type Identity struct {
	AppChannel       string
	AppVersion       string
	AppVersionNumber string
	Platform         string
	SystemVersion    string
	DeviceModel      string
	DeviceName       string
	DeviceUUID       string
}

// DefaultIdentity 返回一组能通过服务端校验的默认值。
//
// 这些值本身不是秘密，服务端也不校验它们与 token 的同源性 ——
// 但 device_uuid 是签名之外唯一可能被用来做设备指纹的字段，
// 因此配置里允许覆盖，好让一台实例长期保持同一个身份。
func DefaultIdentity() Identity {
	return Identity{
		AppChannel:       "official",
		AppVersion:       "1.9.28",
		AppVersionNumber: "10928",
		Platform:         "android",
		SystemVersion:    "13",
		DeviceModel:      "Pixel 6",
		DeviceName:       "Pixel",
		DeviceUUID:       "11111111-2222-3333-4444-555555555555",
	}
}

// values 把 Identity 摊成查询参数。
func (id Identity) values() url.Values {
	return url.Values{
		"app_channel":        {id.AppChannel},
		"app_version":        {id.AppVersion},
		"app_version_number": {id.AppVersionNumber},
		"platform":           {id.Platform},
		"system_version":     {id.SystemVersion},
		"device_model":       {id.DeviceModel},
		"device_name":        {id.DeviceName},
		"device_uuid":        {id.DeviceUUID},
	}
}

// Signer 产出 jdsignature 头的值。
//
// 这是一个刻意的接缝。签名算法由 ticket 02 拥有，且它有一个**已知会失效的输入**：
// 签名常量派生自 App 内的 access key，App 升级或服务端轮换都可能让整套签名作废
// （症状是 ParameterInvalid 重新出现）。把它抽成接口，意味着将来换算法
// 只需换一个实现，而不是在传输层里到处找常量。
//
// 实现者必须满足：对同一个 ts 返回同一个值（纯函数），且不阻塞。
type Signer interface {
	// Sign 返回给定 Unix 秒对应的 jdsignature 值。
	// ts <= 0 时实现方应当使用当前时间。
	Sign(ts int64) string
}

// APIError 是服务端在信封里报的业务错误。
type APIError struct {
	Action  string
	Message string
	// Status 是 HTTP 状态码。同一个 action 在不同状态下含义不同，
	// 因此保留它供诊断；0 表示响应本来就是 200（信封失败路径）。
	Status int
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("javdb api")
	if e.Status != 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	if e.Action != "" {
		fmt.Fprintf(&b, ": %s", e.Action)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	return b.String()
}

// AuthError 表示请求被接受但**凭据有问题** —— 签名对了，token 缺失/过期/被顶下线。
//
// 单独成一型是因为它需要完全不同的处置：普通 APIError 是「这次没拿到数据」，
// AuthError 是「去重新导出 token」。日志里必须能把两者分开，
// 否则用户会看到一条永远为空的 feed 却不知道原因。
type AuthError struct {
	Action  string
	Message string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("javdb api: 需要重新提供 token (%s): %s", e.Action, e.Message)
}

// authActions 是实测与上游一致认定的「凭据有问题」错误名。
var authActions = map[string]bool{
	"JWTVerificationError": true,
	"Unauthorized":         true,
	"LoginRequired":        true,
	"TokenInvalid":         true,
	"TokenExpired":         true,
}

// Client 是一个带签名的 App API 客户端。
type Client struct {
	// Host 形如 https://jdforrepam.com，结尾斜杠会被去掉。
	Host string
	// Token 是用户手工从 App 导出的 JWT。空字符串表示匿名请求。
	Token string
	// Identity 是设备与版本信息。
	Identity Identity
	// Signer 产出 jdsignature。
	Signer Signer
	// Lang 是 accept-language，影响服务端返回的文案语言与部分字段。
	Lang string
	// HTTP 允许注入测试用的 transport。为零值时使用带超时的默认客户端。
	HTTP *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// envelope 是服务端的响应信封。
//
// success 在实测中出现过 0/1 两种数值形态，上游还见过布尔形态，
// 因此用 any 接住再自行判断。
type envelope struct {
	Success any             `json:"success"`
	Action  string          `json:"action"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// successTruthy 判断信封里的 success 是否表示成功。
//
// 实测 /api/v1/startup 返回 {"success":1,...}、失败路径返回 {"success":0,...}。
// 同时接受布尔与字符串形态，因为这个字段的类型没有被固定下来。
func successTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	default:
		return true
	}
}

// GetJSON 发一次签名的 GET，并把信封里的 data 解进 dest。
//
// dest 为 nil 时只校验成功与否，不解析负载。
func (c *Client) GetJSON(ctx context.Context, path string, params url.Values, dest any) error {
	q := c.Identity.values()
	for k, vs := range params {
		for _, v := range vs {
			q.Set(k, v)
		}
	}

	u := strings.TrimRight(c.Host, "/") + path
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}

	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("构造请求: %w", err)
	}

	// jdsignature 是请求头 —— 这一条是 2026-09-28 实测确认的，
	// 早期把它当查询参数试探得到的 ParameterInvalid 是误判。
	req.Header.Set("jdsignature", c.Signer.Sign(ts))
	req.Header.Set("user-agent", "Dart/3.4 (dart:io)")
	req.Header.Set("connection", "keep-alive")
	if c.Lang != "" {
		req.Header.Set("accept-language", c.Lang)
	}
	if c.Token != "" {
		req.Header.Set("authorization", "Bearer "+c.Token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("请求 %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("读取 %s 响应: %w", path, err)
	}

	// 先尽力解析信封，**不管 HTTP 状态码**。
	//
	// 这一点是实测逼出来的：签名值无效时服务端返回 HTTP 400，
	// 但响应体仍然是标准信封。
	//
	//   签名缺失  → HTTP 200 + {"success":0,"action":"ParameterInvalid"}
	//   签名无效  → HTTP 400 + {"success":0,"action":"InvalidSignature"}
	//
	// 如果按状态码提前返回一个字符串错误，action 就丢了 —— 而 action 正是
	// 区分「签名坏了，要改代码」与「网络抖了，等会儿重试」的唯一依据。
	// 丢掉它会让告警在最该响的时候变成哑的。
	var env envelope
	// 判据是「success 字段存在」而不是「action 非空」——
	// 成功响应里 action 就是 null，用后者会把每条成功响应都误判成解析失败。
	envelopeOK := json.Unmarshal(body, &env) == nil && env.Success != nil

	if envelopeOK && !successTruthy(env.Success) {
		if authActions[env.Action] {
			return &AuthError{Action: env.Action, Message: env.Message}
		}
		return &APIError{Action: env.Action, Message: env.Message, Status: resp.StatusCode}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("请求 %s 返回 HTTP %d: %s", path, resp.StatusCode, truncate(string(body), 200))
	}
	if !envelopeOK {
		return fmt.Errorf("解析 %s 信封失败; body=%s", path, truncate(string(body), 200))
	}

	if dest == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, dest); err != nil {
		return fmt.Errorf("解析 %s 负载: %w", path, err)
	}
	return nil

}

// IsAuthError 报告 err 链上是否有凭据类错误。
func IsAuthError(err error) bool {
	var ae *AuthError
	return errors.As(err, &ae)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
