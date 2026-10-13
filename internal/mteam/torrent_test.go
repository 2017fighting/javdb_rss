package mteam

import (
	"strings"
	"testing"
)

// 下面四份夹具都是脚本构造的，**期望的 infohash 用 python 的 hashlib 独立算出来**
// （对 info 字典的原始字节切片取 SHA-1），不是本实现的输出。
//
// 它们覆盖三件必须分开的事：
//
//  1. 正常的种子 → 一个具体的 infohash；
//  2. info 的键**没有**按字典序排列 → 见下；
//  3. 用不了的种子（v2-only、announce 没凭据、非 bencode）→ 必须报错。
const (
	// 夹具 A：规范的种子（info 的键按字典序）。
	torrentCanonical = "d8:announce47:https://tra1.m-team.cc/announce?credential=YWJj13:creation datei1791873283e4:infod6:lengthi1024e4:name5:a.mp412:piece lengthi16384e6:pieces20:012345678901234567897:privatei1eee"

	// 夹具 B：info 的键**没有**按字典序（`name` 在 `length` 前面）。
	//
	// ⚠️ 它存在的**唯一**理由是区分两种实现：
	//
	//	切原始切片       → a36ef264aacc672dfd3e249608dcafadfe4566a5   ← 正确
	//	重新编码 info 字典 → f875b7bedb38e072dde96440fb79e5fc55699d68   ← 错的
	//
	// 后者是「把解析出来的 info 再编码一遍」顺手排了序的结果。它语法上完全合法，
	// 但 tracker 不认 —— 客户端会把它当成**另一个**种子，而这件事没有任何外部信号。
	// bencode 规范要求字典键有序，现实里的种子不保证（而这个夹具就是那种）。
	torrentUnsorted = "d8:announce47:https://tra1.m-team.cc/announce?credential=YWJj4:infod4:name5:a.mp46:lengthi1024e12:piece lengthi16384e6:pieces20:012345678901234567897:privatei1eee"

	// 夹具 C：v2-only（有 `meta version` = 2，但没有 `length` / `files`）。
	// 本服务只认 v1，而 v2 的 infohash 是另一套算法 —— 静默给一个错的比报错糟得多。
	torrentV2Only = "d8:announce47:https://tra1.m-team.cc/announce?credential=YWJj4:infod9:file treed5:a.mp4d0:d6:lengthi1024eeee12:meta versioni2e4:name5:a.mp412:piece lengthi16384eee"

	// 夹具 D：announce 里没有凭据（像公网 tracker）。
	// 私种发出去会连不上 tracker —— 在客户端是红种，在我们这边**无声**。
	torrentNoCredential = "d8:announce36:https://tracker.example.com/announce4:infod6:lengthi1024e4:name5:a.mp412:piece lengthi16384e6:pieces20:012345678901234567897:privatei1eee"
)

func TestInfohash(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    string
		wantErr bool
		errHas  string
	}{
		{
			name: "规范的种子",
			data: torrentCanonical,
			want: "f875b7bedb38e072dde96440fb79e5fc55699d68",
		},
		{
			// 这条是「必须切原始切片」的唯一证据。若实现改成重新编码，
			// 它会得到 f875b7be…（夹具 A 的值）而在这里失败。
			name: "info 键序非规范 —— 必须切原始切片，不能重新编码",
			data: torrentUnsorted,
			want: "a36ef264aacc672dfd3e249608dcafadfe4566a5",
		},
		{
			name:    "v2-only 必须拒绝，不能给一个错的",
			data:    torrentV2Only,
			wantErr: true,
			errHas:  "v2",
		},
		{
			name:    "announce 里没有凭据必须拒绝",
			data:    torrentNoCredential,
			wantErr: true,
			errHas:  "凭据",
		},
		{
			name:    "不是 bencode",
			data:    "not a torrent at all",
			wantErr: true,
		},
		{
			name:    "截断",
			data:    torrentCanonical[:40],
			wantErr: true,
		},
		{
			name:    "没有 info 字典",
			data:    "d8:announce3:abce",
			wantErr: true,
			errHas:  "info",
		},
		{
			name:    "空输入",
			data:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Infohash([]byte(tt.data))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望报错，却得到 %q", got)
				}
				if tt.errHas != "" && !strings.Contains(err.Error(), tt.errHas) {
					t.Errorf("报错里应当提到 %q，实际是 %q", tt.errHas, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外报错: %v", err)
			}
			if got != tt.want {
				t.Errorf("Infohash = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestInfohashDoesNotMutateInput 确认解析不改调用方给的字节。
//
// 调用方（装饰器）拿到的 .torrent 会被存下来再端出去，因此它必须原样保留 ——
// 一个「解析顺手规整了字节」的实现会让存下来的那份与客户端该拿到的那份不一致。
func TestInfohashDoesNotMutateInput(t *testing.T) {
	data := []byte(torrentUnsorted)
	before := string(data)
	if _, err := Infohash(data); err != nil {
		t.Fatalf("意外报错: %v", err)
	}
	if string(data) != before {
		t.Error("解析改动了输入的字节")
	}
}
