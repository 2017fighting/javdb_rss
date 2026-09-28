package appapi

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// 黄金向量。由 Python 用标准库 hashlib 独立算出，不是从本实现反向导出的 ——
// 否则测试只能证明「代码没变」，不能证明「协议对」。
//
//	md5(f"{ts}{prefix}") 配 "{ts}.{suffix}.{digest}"
var goldenVectors = []struct {
	ts   int64
	want string
}{
	{1700000000, "1700000000.lpw6vgqzsp.dacaffcd8b4e1b35c2752f065e906f3a"},
	{1770000000, "1770000000.lpw6vgqzsp.82ff452f148706f26e14e75bf83c8138"},
}

// TestSignGoldenVectors 锁死签名算法。
//
// 这条测试是「有人顺手改了常量或拼接顺序」的唯一防线。签名出错的表现是
// 上游返回 InvalidSignature —— 而那个错误不会在离线测试里出现，
// 只会让线上服务在某次发布后突然全部 502。
func TestSignGoldenVectors(t *testing.T) {
	s := NewSigner()
	for _, g := range goldenVectors {
		if got := s.Sign(g.ts); got != g.want {
			t.Errorf("Sign(%d) =\n  %q\nwant\n  %q", g.ts, got, g.want)
		}
	}
}

// TestSignShape 确认三段式结构，并钉住「摘要必须是 MD5 的 32 位小写 hex」。
// 长度或大小写变了，服务端同样会拒。
func TestSignShape(t *testing.T) {
	got := NewSigner().Sign(1700000000)
	parts := strings.Split(got, ".")
	if len(parts) != 3 {
		t.Fatalf("签名应为 3 段，得到 %d 段: %q", len(parts), got)
	}
	if parts[0] != "1700000000" {
		t.Errorf("第一段应为时间戳，得到 %q", parts[0])
	}
	if parts[1] != signSuffix {
		t.Errorf("第二段应为 suffix，得到 %q", parts[1])
	}
	if len(parts[2]) != 32 {
		t.Errorf("第三段应为 32 位 MD5 hex，得到 %d 位: %q", len(parts[2]), parts[2])
	}
	if parts[2] != strings.ToLower(parts[2]) {
		t.Errorf("摘要应为小写 hex: %q", parts[2])
	}
}

// TestSignIsPureFunctionOfTimestamp 钉住一个逆向时最容易猜错的方向：
// 签名**不**绑定 token、不绑定请求内容、不绑定设备信息。
//
// 实测确认：同一个时间戳算出的签名可以配任意 token 使用。
// 如果将来有人「为了安全」把请求内容掺进来，这条会拦住他。
func TestSignIsPureFunctionOfTimestamp(t *testing.T) {
	s := NewSigner()
	first := s.Sign(1700000000)
	for i := 0; i < 5; i++ {
		if got := s.Sign(1700000000); got != first {
			t.Fatalf("同一时间戳产生了不同签名: %q vs %q", got, first)
		}
	}
	if s.Sign(1700000001) == first {
		t.Error("不同时间戳产生了相同签名")
	}
}

// TestSignZeroUsesNow 确认 ts<=0 退化为当前时间，而不是产出 "0." 开头的垃圾签名。
func TestSignZeroUsesNow(t *testing.T) {
	before := time.Now().Unix()
	got := NewSigner().Sign(0)
	after := time.Now().Unix()

	tsPart := strings.SplitN(got, ".", 2)[0]
	ts, err := strconv.ParseInt(tsPart, 10, 64)
	if err != nil {
		t.Fatalf("签名第一段不是整数: %q", got)
	}
	if ts < before || ts > after {
		t.Errorf("ts=%d 不在 [%d, %d] 区间内", ts, before, after)
	}
}

// TestDefaultIdentityHasAllRequiredParams 钉住那 8 个必须带的公共参数。
//
// 少任何一个，服务端返回 ParameterInvalid（HTTP 200）。这条曾经真实发生过：
// 早期只带签名、不带公共参数，被拒后误判成「签名算法不对」。
func TestDefaultIdentityHasAllRequiredParams(t *testing.T) {
	required := []string{
		"app_channel", "app_version", "app_version_number", "platform",
		"system_version", "device_model", "device_name", "device_uuid",
	}
	v := DefaultIdentity().values()
	for _, k := range required {
		if v.Get(k) == "" {
			t.Errorf("公共参数 %q 为空 —— 服务端会返回 ParameterInvalid", k)
		}
	}
	if len(v) != len(required) {
		t.Errorf("公共参数数量 %d，期望 %d：多出来的字段需要确认是否也是必带", len(v), len(required))
	}
}
