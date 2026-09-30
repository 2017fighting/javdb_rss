package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Login 用账号密码换一个 token。
//
// # 契约（实测 2026-09-30 确认）
//
//	POST /api/v1/sessions
//	form: username=<用户名>  password=<密码>
//	→ {"success":1,"data":{"token":"eyJ..."}}
//
// 三个容易踩的点：
//
//  1. 字段是 **username** 而不是 email。先例项目的文档在两个地方写了不同的名字，
//     而实测要求 username —— 传 email 会得到
//     `ParameterInvalid: 參數不能爲空: username`。
//  2. 响应里的字段名历史上变过，因此 `token` 与 `access_token` 都读。
//  3. **没有验证码、没有设备绑定**（实测：任意 username/password 都会被走流程，
//     错误密码得到的是 IncorrentUsernameOrPassword 而不是挑战）。
//
// # ⚠️ 单会话：登录会挤掉已有的会话
//
// 用户实测报告：同一账号只能在一个地方登录，新登录会挤掉之前那个。
// 也就是说**本服务登录 = 用户手机上的 App 被挤下线**。
// 调用方必须把这个后果告诉用户，不能默默替他登。
func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		// 本地就能发现的问题不必白打一次上游。
		return "", fmt.Errorf("登录需要用户名与密码")
	}

	var data struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := c.PostFormJSON(ctx, "/api/v1/sessions",
		url.Values{"username": {username}, "password": {password}}, &data); err != nil {
		return "", fmt.Errorf("登录: %w", err)
	}

	token := strings.TrimSpace(data.Token)
	if token == "" {
		token = strings.TrimSpace(data.AccessToken)
	}
	if token == "" {
		// 返回空 token 会让上层拿去用，然后在下一个请求变成一个难懂的 401。
		// 在这里明确报出来。
		return "", fmt.Errorf("登录成功但响应里没有 token 字段")
	}
	return token, nil
}
