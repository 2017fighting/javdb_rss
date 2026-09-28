package appapi

import (
	"context"
	"errors"
)

// Check 打一次最轻的请求，用来证明签名与服务端仍然兼容。
//
// 选 /api/v1/startup 是因为它：不需要 token、不依赖任何参数、
// 返回的是全局配置，且服务端在签名校验失败时**一定**会先拒它。
// 换句话说，它能通过的请求里，只有签名是真正被验证过的那一项。
func (c *Client) Check(ctx context.Context) error {
	return c.GetJSON(ctx, "/api/v1/startup", nil, nil)
}

// signatureActions 是「签名/请求构造与服务端不兼容」的 action 集合。
//
// 实测得到的两种形态（这是本服务唯一已知会失效的输入）：
//
//	签名值为空或缺失 → HTTP 200, success:0, action=ParameterInvalid
//	                   message 形如 "參數不能爲空: jdsignature"
//	签名值无效       → HTTP 400, success:0, action=InvalidSignature
//	                   message 形如 "無效的簽名"
//
// 两者都意味着**要改代码**而不是重试，因此归为一类。
// ParameterInvalid 偶尔也会因为公共参数缺了而触发（例如 platform），
// 那同样是「我们的请求构造和服务端不一致」，处置方式一样。
var signatureActions = map[string]bool{
	"InvalidSignature": true,
	"ParameterInvalid": true,
}

// ActionOf 取出 err 里上游报告的错误名。非信封错误返回空字符串。
func ActionOf(err error) string {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Action
	}
	var auth *AuthError
	if errors.As(err, &auth) {
		return auth.Action
	}
	return ""
}

// IsSignatureError 报告 err 是否由「签名与服务端不再兼容」引起。
//
// 这个区分很要紧，因为两种失败的处置方式完全不同：
//
//	签名失效  → 代码要改（Prefix 变了），重启和重试都没用
//	网络故障  → 等一会儿重试就好，不该把人叫起来
func IsSignatureError(err error) bool {
	return signatureActions[ActionOf(err)]
}
