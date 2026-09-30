package pin

import (
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// TestDefaultPolicyDesiredIsCatalogSelect 钉住「纯函数部分只有一处实现」：
// 策略的 Desired 就是 catalog.Select（ticket 04/09 的 created_at 规则）。
func TestDefaultPolicyDesiredIsCatalogSelect(t *testing.T) {
	in := []catalog.Magnet{magnetAt("old", false, "2026-01-01"), magnetAt("new", false, "2026-03-01")}
	got, ok := DefaultPolicy{}.Desired(in)
	want, _ := catalog.Select(in)
	if !ok || got.Infohash != want.Infohash {
		t.Fatalf("DefaultPolicy.Desired = %q/%v, want %q", got.Infohash, ok, want.Infohash)
	}
	if got.Infohash != "new" {
		t.Errorf("应当按 created_at 取最新，得到 %q", got.Infohash)
	}
}

// TestDefaultPolicyKeepOrSwitch 是 ticket 09 的核心决定矩阵。
//
// 规则：只有出现 cnsub 才可能切换。
//
//	pin 非 cnsub + 任一 cnsub        → 切到该 cnsub
//	pin 是 cnsub + 日期严格更新的 cnsub → 切
//	其余（同日/更旧/只有普通候选）    → 不切
func TestDefaultPolicyKeepOrSwitch(t *testing.T) {
	tests := []struct {
		name        string
		pinned      Record
		pinnedOK    bool
		desired     catalog.Magnet
		wantHash    string
		wantChanged bool
	}{
		{
			"无 pin 一律采用 desired",
			Record{}, false, magnetAt("d", false, "2026-01-01"),
			"d", true,
		},
		{
			"pin 非 cnsub，出现 cnsub 就切（即使 cnsub 更旧）",
			record("p", false, "2026-05-01"), true, magnetAt("d", true, "2026-01-01"),
			"d", true,
		},
		{
			"pin 非 cnsub，只有更新的普通候选 → 不切",
			record("p", false, "2026-01-01"), true, magnetAt("d", false, "2026-05-01"),
			"p", false,
		},
		{
			"pin 是 cnsub，出现日期更新的 cnsub → 切",
			record("p", true, "2026-01-01"), true, magnetAt("d", true, "2026-05-01"),
			"d", true,
		},
		{
			"pin 是 cnsub，出现同日的 cnsub → 不切（避免 tie-break 抖动）",
			record("p", true, "2026-05-01"), true, magnetAt("d", true, "2026-05-01"),
			"p", false,
		},
		{
			"pin 是 cnsub，出现更旧的 cnsub → 不切",
			record("p", true, "2026-05-01"), true, magnetAt("d", true, "2026-01-01"),
			"p", false,
		},
		{
			"pin 是 cnsub，只剩普通候选 → 不切",
			record("p", true, "2026-01-01"), true, magnetAt("d", false, "2026-05-01"),
			"p", false,
		},
		{
			"desired 与 pin 同一条 → 不算变更",
			record("same", true, "2026-01-01"), true, magnetAt("same", true, "2026-01-01"),
			"same", false,
		},
		{
			"pin 与 desired 都是 cnsub 且都无日期 → 视为同日，不切",
			record("p", true, ""), true, magnetAt("d", true, ""),
			"p", false,
		},
		{
			"pin 与 desired 都是 cnsub 且日期都无法解析 → 视为同日，不切",
			record("p", true, "garbage"), true, magnetAt("d", true, "unknown"),
			"p", false,
		},
		{
			"pin 是 cnsub 但快照无日期，desired 有日期 → 切（无日期按最旧算）",
			record("p", true, ""), true, magnetAt("d", true, "2026-05-01"),
			"d", true,
		},
		{
			"pin 非 cnsub 且都无日期 → 因出现 cnsub 仍切",
			record("p", false, ""), true, magnetAt("d", true, ""),
			"d", true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := DefaultPolicy{}.KeepOrSwitch(tt.pinned, tt.pinnedOK, tt.desired)
			if got.Infohash != tt.wantHash {
				t.Errorf("选中 %q, want %q", got.Infohash, tt.wantHash)
			}
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
		})
	}
}
