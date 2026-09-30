// Package appapi 的签名实现。
//
// # 来源与致谢
//
// 本文件的算法与常量来自开源项目 javdb-cli（MIT License）：
//
//	https://github.com/FlanChanXwO/javdb-cli
//	internal/javdb/protocol/signature/sign.go
//
// 该项目把 jdsignature 从 JavDB.apk 1.9.28 中逆向出来。我们在 2026-09-28
// 用完全相同的算法对 **1.9.35** 的服务端做了实测，确认仍然有效。
//
// 之所以把它拷贝进来而不是依赖那个 SDK：算法只有三行、两个常量，
// 而引入整个 SDK 会拖进 37 个模块（HTTP/TLS 指纹/QUIC/cobra…），
// 并把返回类型退化成 map[string]any —— 恰恰在最需要类型安全的那层边界上。
// 代价是我们自己负责发现它失效（见下方 probe.go）。
package appapi

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	// signPrefix 是签名前缀常量。
	//
	// 它由 App 内的 access key（"30820"）与包内的 CONST_PREFIX/CONST_SUFFIX
	// 派生而成。**这是本服务唯一已知会失效的输入**。
	//
	// ⚠️ 失效的症状是 **HTTP 400 + action=InvalidSignature**（实测确认），
	// 不是 ParameterInvalid —— 后者是「签名缺失或公共参数缺了」。
	// （这里曾经写错成 ParameterInvalid，会把查日志的人引向错误的排查方向。）
	// 检测机制见 probe.go 与 health 包的后台探针。
	signPrefix = "71cf27bb3c0bcdf207b64abecddc970098c7421ee7203b9cdae54478478a199e7d5a6e1a57691123c1a931c057842fb73ba3b3c83bcd69c17ccf174081e3d8aa"

	// signSuffix 是签名中段常量。
	signSuffix = "lpw6vgqzsp"
)

// Signer 的默认实现。
//
// 它的全部输入只有时间戳 —— 不掺设备信息、不掺请求内容、不掺 token。
// 这一点值得强调，因为「签名是否绑定 token」是逆向时最容易猜错的方向，
// 实测确认答案是「不绑定」：同一个签名配上不同 token 会被正常受理。
type constSigner struct{}

// NewSigner 返回默认的签名器。
func NewSigner() Signer { return constSigner{} }

// Sign 实现 Signer。
//
// 形式为 "{ts}.{suffix}.{md5(ts + prefix)}"。
func (constSigner) Sign(ts int64) string {
	if ts <= 0 {
		ts = time.Now().Unix()
	}
	sum := md5.Sum([]byte(fmt.Sprintf("%d%s", ts, signPrefix)))
	return fmt.Sprintf("%d.%s.%s", ts, signSuffix, hex.EncodeToString(sum[:]))
}

// 编译期确认默认签名器满足接口。
var _ Signer = constSigner{}

// 注意：服务端校验的就是 MD5。这里不是安全性选择，是**协议兼容性**选择 ——
// 换摘要算法等于换协议，必须先确认服务端也换了。
