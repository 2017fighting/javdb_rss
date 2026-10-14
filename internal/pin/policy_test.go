package pin

import (
	"encoding/json"
	"strings"
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

// TestKeepOrSwitchNeverCrossesSources 钉住「首发冻结」真正落地的那一条。
//
// ⚠️ 它必须在**策略**这一层测：决定最终发哪条的是这里，不是上层的装饰器。
// 验收（Docker + 真 M-Team）实测踩到过：M-Team 层对已钉住的作品「原样放行」
// 上游那个候选池，而这里按 cnsub/日期规则判定上游那条更好 —— 于是 pin 从馒头
// 被换回上游，guid 抖回去（客户端多下一份），而且**馒头从此再也不生效**。
func TestKeepOrSwitchNeverCrossesSources(t *testing.T) {
	tests := []struct {
		name    string
		pinned  catalog.Magnet
		desired catalog.Magnet
		want    string
		why     string
	}{
		{
			"pin 是馒头、候选是上游 —— 不切（哪怕上游是 cnsub 且更新）",
			catalog.Magnet{Source: catalog.SourceMTeam, Infohash: "mt", CreatedAt: "2020-01-01"},
			catalog.Magnet{Source: catalog.SourceUpstream, Infohash: "up", CNSub: true, CreatedAt: "2026-01-01"},
			"mt", "换来源 = 客户端多下一份；唯一的出口是运维显式做的「重置」",
		},
		{
			"pin 是上游、候选是馒头 —— 不切",
			catalog.Magnet{Source: catalog.SourceUpstream, Infohash: "up", CNSub: true, CreatedAt: "2020-01-01"},
			catalog.Magnet{Source: catalog.SourceMTeam, Infohash: "mt", CNSub: true, CreatedAt: "2026-01-01"},
			"up", "同上",
		},
		{
			"同一个来源里，cnsub 升级照旧切（既有行为不变）",
			catalog.Magnet{Source: catalog.SourceUpstream, Infohash: "up1", CNSub: false, CreatedAt: "2026-01-01"},
			catalog.Magnet{Source: catalog.SourceUpstream, Infohash: "up2", CNSub: true, CreatedAt: "2020-01-01"},
			"up2", "ticket 09 定稿的规则：出现 cnsub 才切",
		},
		{
			"馒头内部也照旧（它自己那一组里出现 cnsub）",
			catalog.Magnet{Source: catalog.SourceMTeam, Infohash: "mt1", CNSub: false, CreatedAt: "2026-01-01"},
			catalog.Magnet{Source: catalog.SourceMTeam, Infohash: "mt2", CNSub: true, CreatedAt: "2020-01-01"},
			"mt2", "同上",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := DefaultPolicy{}.KeepOrSwitch(recordOf(tt.pinned), true, tt.desired)
			if got.Infohash != tt.want {
				t.Errorf("选中 %q, want %q —— %s", got.Infohash, tt.want, tt.why)
			}
			// 来源也必须跟着走：丢了它，enclosure 会从种子文件退回磁力（死链）。
			if got.Source != tt.pinned.Source {
				t.Errorf("来源 = %v，应当仍是 pin 的 %v", got.Source, tt.pinned.Source)
			}
		})
	}
}

// TestRecordSourceIsReadableAndRoundTrips 钉住来源在 `pin.json` 里是**可读的**，
// 而手改错了会当场报错（不静默当上游）。
//
// 那份文件是给人手工改的（README 把「删掉某一条」当成正式的运维手段）——
// 一个数字位会让那件事变成猜谜，而猜错的后果是一条 pin 的来源被读成上游，
// 于是它的 enclosure 从种子文件退回磁链，而私种的裸磁链是死链。
func TestRecordSourceIsReadableAndRoundTrips(t *testing.T) {
	b, err := json.Marshal(recordOf(catalog.Magnet{Source: catalog.SourceMTeam, Infohash: "h"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"source":"mteam"`) {
		t.Errorf("来源应当是**可读的名字**，实际 %s", b)
	}

	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Source != catalog.SourceMTeam {
		t.Errorf("往返之后来源丢了：%v", r.Source)
	}

	// 旧文件（没有 source 键）读成上游 —— 向后兼容，不炸。
	r = Record{}
	if err := json.Unmarshal([]byte(`{"infohash":"h","pinned_at":"2026-01-01T00:00:00Z"}`), &r); err != nil {
		t.Fatalf("旧文件应当照读: %v", err)
	}
	if r.Source != catalog.SourceUpstream {
		t.Errorf("没有 source 键时应当读成上游，实际 %v", r.Source)
	}

	// 认不出的名字报错，不静默当上游。
	r = Record{}
	if err := json.Unmarshal([]byte(`{"infohash":"h","source":"mteem"}`), &r); err == nil {
		t.Error("认不出的来源名必须报错（静默当上游会让那部作品的种子地址变成死链）")
	}
}
