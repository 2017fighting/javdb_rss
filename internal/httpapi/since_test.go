package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// captureLog 返回一个把日志写进内存的 logger（Debug 级，因为正常路径是 Debug）。
func captureLog(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// TestParseSinceAcceptsOnlyStrictDates 钉住 since 的输入形态。
//
// 这不是「格式洁癖」：未校验的 since 会按**字符串字序**比较，静默丢掉一整段。
// 实测（2026-10-05，真实 filterSince）：
//
//	since=2026-1-1            → 丢掉 1–9 月（6 部只剩 3 部）
//	since=hello               → 只剩 1 部（等同空 feed）
//	since=2026-01-01T00:00:00Z → 丢掉**当天**发行的作品（日期是它的前缀）
//
// 三种都是 200 + 错 feed，用户只看得到「怎么少了」。
func TestParseSinceAcceptsOnlyStrictDates(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"严格日期", "2026-01-01", "2026-01-01", false},
		{"两侧空白被 trim", "  2026-01-01\n", "2026-01-01", false},
		{"空串 = 不过滤", "", "", false},
		{"纯空白 = 不过滤", "   ", "", false},
		{"不补零的月日", "2026-1-1", "", true},
		{"只不补零的日", "2026-01-1", "", true},
		{"ISO 时间戳", "2026-01-01T00:00:00Z", "", true},
		{"日期时间（空格分隔）", "2026-01-01 00:00:00", "", true},
		{"乱写", "hello", "", true},
		{"不存在的日期", "2026-13-45", "", true},
		{"没有分隔符", "20260101", "", true},
		{"只有年", "2026", "", true},
		{"斜杠分隔", "2026/01/01", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSince(tt.raw, "", "")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSince(%q) 应当报错", tt.raw)
				}
				if !errors.Is(err, catalog.ErrBadRequest) {
					t.Errorf("错误应当能用 ErrBadRequest 判定（上层据此回 400 而不是 502）: %v", err)
				}
				if !strings.Contains(err.Error(), "YYYY-MM-DD") {
					t.Errorf("文案要写出期望的形态: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSince(%q) 不该报错: %v", tt.raw, err)
			}
			if string(got) != tt.want {
				t.Errorf("parseSince(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// TestParseSinceRejectsUpstreamRangeConflict 钉住「两个范围维度不能同时给」。
//
// year / month 是上游筛选（整年 / 整月），since 是本地的「这个日期起」——
// 同时发的结果必然是空 feed，而「选了年份反而是空的」会让人以为功能坏了。
// 女优路由本来就有这条（只认 year），全站路由此前没有（补上，且要连 month）。
func TestParseSinceRejectsUpstreamRangeConflict(t *testing.T) {
	tests := []struct {
		name      string
		since     string
		year      string
		month     string
		wantErr   bool
		wantWords []string
	}{
		{name: "year 与 since", since: "2026-01-01", year: "2021", wantErr: true,
			wantWords: []string{"year=2021", "since=2026-01-01", "不能同时给"}},
		{name: "month 与 since", since: "2026-01-01", month: "3", wantErr: true,
			wantWords: []string{"month=3", "不能同时给"}},
		{name: "year 与 month 都给了", since: "2026-01-01", year: "2021", month: "3", wantErr: true,
			wantWords: []string{"year=2021", "month=3"}},
		{name: "只有 year、没有 since —— 放行", year: "2021", wantErr: false},
		{name: "只有 since —— 放行", since: "2026-01-01", wantErr: false},
		{name: "空的 year 不算冲突", since: "2026-01-01", year: "  ", wantErr: false},
		{name: "路由不认 month（清单）时传空串", since: "2026-01-01", month: "", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSince(tt.since, tt.year, tt.month)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("不该报错: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("应当报错")
			}
			if !errors.Is(err, catalog.ErrBadRequest) {
				t.Errorf("错误应当能用 ErrBadRequest 判定: %v", err)
			}
			for _, w := range tt.wantWords {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("文案 %q 里应当有 %q", err.Error(), w)
				}
			}
		})
	}
}

// sinceWorks 是下面几条过滤测试共用的样本。
//
// 每一部都对应一种判据，编号即断言里的标识：
//
//	BOUNDARY  正好等于 since —— 闭区间，必须保留
//	OLDER     比 since 早一天 —— 丢弃
//	NODATE    发行日期为空 —— 保留（上游没给，不能替它决定）
//	SHAPELESS 形状不对（斜杠）—— 保留，且与「空」分开计数
//	UNPADDED  形状不对（不补零）—— 保留，同上
//	FUTURE    比 since 晚 —— 保留
func sinceWorks() []catalog.Work {
	return []catalog.Work{
		{Number: "OLDER", ReleaseDate: "2025-12-31",
			Magnets: []catalog.Magnet{{Infohash: "m-older"}}},
		{Number: "BOUNDARY", ReleaseDate: "2026-01-01",
			Magnets: []catalog.Magnet{{Infohash: "m-boundary"}}},
		{Number: "FUTURE", ReleaseDate: "2026-09-29",
			Magnets: []catalog.Magnet{{Infohash: "m-future"}}},
		{Number: "NODATE", Magnets: []catalog.Magnet{{Infohash: "m-nodate"}}},
		{Number: "SHAPELESS", ReleaseDate: "2026/9/29",
			Magnets: []catalog.Magnet{{Infohash: "m-shapeless"}}},
		{Number: "UNPADDED", ReleaseDate: "2026-1-5",
			Magnets: []catalog.Magnet{{Infohash: "m-unpadded"}}},
		// 没有磁链候选：会被 feed.Build 跳过，因此永远成不了 item。
		{Number: "NOMAGNET", ReleaseDate: "2026-06-01"},
	}
}

// TestSinceFilterKeepsBadDates 钉住「按错误规则丢弃数据比多给几条危险得多」。
func TestSinceFilterKeepsBadDates(t *testing.T) {
	log, _ := captureLog(t)
	got := mustSince(t, "2026-01-01").filter(log, sinceWorks(), false)

	kept := make(map[string]bool, len(got))
	for _, w := range got {
		kept[w.Number] = true
	}
	for _, want := range []string{"BOUNDARY", "FUTURE", "NODATE", "SHAPELESS", "UNPADDED", "NOMAGNET"} {
		if !kept[want] {
			t.Errorf("%s 应当保留（闭区间的边界日、坏日期一律保留）", want)
		}
	}
	if kept["OLDER"] {
		t.Error("OLDER（2025-12-31）比 since 早，应当被丢弃")
	}
}

// TestSinceFilterWarnsOnlyOnAnomalies 钉住定稿后的日志形态。
//
// 正常路径只留一条 Debug（qBittorrent 每 15 分钟轮询一次，Info/Warn 会变噪声）；
// 异常各有自己的 WARN。定稿后不再有「语义尚未定稿」那条 —— 它是这次定稿要删的东西。
func TestSinceFilterWarnsOnlyOnAnomalies(t *testing.T) {
	t.Run("正常路径：只有 Debug，没有 WARN", func(t *testing.T) {
		log, buf := captureLog(t)
		mustSince(t, "2026-01-01").filter(log, sinceWorks(), false)

		out := buf.String()
		if strings.Contains(out, "level=WARN") {
			t.Errorf("正常路径不该有 WARN：\n%s", out)
		}
		if !strings.Contains(out, "level=DEBUG") {
			t.Fatalf("正常路径要留一条 Debug：\n%s", out)
		}
		// 拆开的计数：作品数与「真的会出现在 feed 里」的条目数是两回事 ——
		// 实测 50 部里有 33 部没有磁链候选，它们永远成不了条目。
		for _, want := range []string{"取到=7", "有磁链候选=6", "保留=6", "保留且能成条目=5", "丢弃=1"} {
			if !strings.Contains(out, want) {
				t.Errorf("Debug 行里应当有 %q：\n%s", want, out)
			}
		}
	})

	t.Run("坏日期分开计数", func(t *testing.T) {
		log, buf := captureLog(t)
		mustSince(t, "2026-01-01").filter(log, sinceWorks(), false)

		out := buf.String()
		if !strings.Contains(out, "缺发行日期=1") {
			t.Errorf("Debug 行里应当有「缺发行日期=1」：\n%s", out)
		}
		if !strings.Contains(out, "发行日期形状不对=2") {
			t.Errorf("Debug 行里应当有「发行日期形状不对=2」（与「空」分开）：\n%s", out)
		}
	})

	t.Run("一条都没剩：WARN", func(t *testing.T) {
		log, buf := captureLog(t)
		// 样本里不能有「坏日期」：它们一律保留，因此永远筛不空。
		allOld := []catalog.Work{
			{Number: "A-1", ReleaseDate: "2025-01-01",
				Magnets: []catalog.Magnet{{Infohash: "a"}}},
			{Number: "B-1", ReleaseDate: "2025-06-01",
				Magnets: []catalog.Magnet{{Infohash: "b"}}},
		}
		// since 在未来：合法输入，但结果一定是空 feed —— 手滑多打一位年份的典型症状。
		mustSince(t, "2030-01-01").filter(log, allOld, false)

		out := buf.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "2030-01-01") {
			t.Errorf("筛空时应当有一条点名 since 的 WARN：\n%s", out)
		}
		if !strings.Contains(out, "一条都没剩") {
			t.Errorf("WARN 要说清是「筛空」：\n%s", out)
		}
	})

	t.Run("取数窗没走到 since：WARN", func(t *testing.T) {
		log, buf := captureLog(t)
		// 窗口取满（50 部）而最旧一部仍晚于 since → 不能证明更旧的作品里没有符合条件的。
		mustSince(t, "2020-01-01").filter(log, sinceWorks(), true)

		out := buf.String()
		if !strings.Contains(out, "取数窗") || !strings.Contains(out, "pages") {
			t.Errorf("窗口没走完时应当有一条提示加 pages 的 WARN：\n%s", out)
		}
	})

	t.Run("窗口取满但确实走过了 since：不 WARN", func(t *testing.T) {
		log, buf := captureLog(t)
		// 最旧一部（2025-12-31）早于 since（2026-01-01）→ 已经越过下界，下界是完整的。
		mustSince(t, "2026-01-01").filter(log, sinceWorks(), true)

		if out := buf.String(); strings.Contains(out, "取数窗") {
			t.Errorf("已经越过 since 就不该提窗口：\n%s", out)
		}
	})

	t.Run("空取数：不该喊「筛空」", func(t *testing.T) {
		log, buf := captureLog(t)
		mustSince(t, "2026-01-01").filter(log, nil, false)

		if out := buf.String(); strings.Contains(out, "一条都没剩") {
			t.Errorf("本轮根本没取到作品，不能说「被 since 筛掉了」：\n%s", out)
		}
	})

	t.Run("那条「尚未定稿」的 WARN 必须消失", func(t *testing.T) {
		log, buf := captureLog(t)
		mustSince(t, "2030-01-01").filter(log, sinceWorks(), true)

		if out := buf.String(); strings.Contains(out, "尚未定稿") {
			t.Errorf("定稿后不该再出现「尚未定稿」：\n%s", out)
		}
	})
}

// TestSinceFilterDisabledIsNoop 确认不带 since 时不过滤、也不打日志。
func TestSinceFilterDisabledIsNoop(t *testing.T) {
	log, buf := captureLog(t)
	works := sinceWorks()

	got := mustSince(t, "").filter(log, works, true)
	if len(got) != len(works) {
		t.Errorf("不过滤时应当原样返回 %d 部，得到 %d 部", len(works), len(got))
	}
	if out := buf.String(); out != "" {
		t.Errorf("没启用 since 就不该留下任何日志（否则每条 feed 都会多一行）:\n%s", out)
	}
}

// TestSinceFilterDoesNotMutateInput 确认过滤不就地改写调用方的切片。
func TestSinceFilterDoesNotMutateInput(t *testing.T) {
	log, _ := captureLog(t)
	works := sinceWorks()
	first := works[0].Number

	_ = mustSince(t, "2026-01-01").filter(log, works, false)

	if works[0].Number != first || len(works) != 7 {
		t.Errorf("入参被就地改写了：len=%d first=%s", len(works), works[0].Number)
	}
}

func mustSince(t *testing.T, raw string) sinceBound {
	t.Helper()
	b, err := parseSince(raw, "", "")
	if err != nil {
		t.Fatalf("parseSince(%q): %v", raw, err)
	}
	return b
}

// TestSinceComparesReleaseDateNotMagnetCreatedAt 钉住票 10 决策 1 的**否定面**。
//
// 「比发行日期而不是选中磁链的 created_at」这件事的价值全在否定面：那个字段
// 确实存在、也确实是个日期，因此「顺手改成比磁链时间」是个看起来非常合理的回归 ——
// 而它会让一部 2020 年的旧作品因为今天多了一条种子而重新出现在追新 feed 里
// （并集同理）。两个方向都要钉：
//
//	REISSUE     发行日期旧、磁链新 → **丢**（若比磁链时间就会被留下来）
//	COMPILATION 发行日期新、磁链旧 → **留**（合集再版，实测差到 6 年）
func TestSinceComparesReleaseDateNotMagnetCreatedAt(t *testing.T) {
	log, _ := captureLog(t)
	works := []catalog.Work{
		{Number: "REISSUE", ReleaseDate: "2020-09-22",
			Magnets: []catalog.Magnet{{Infohash: "m-reissue", CreatedAt: "2026-09-29"}}},
		{Number: "COMPILATION", ReleaseDate: "2026-09-29",
			Magnets: []catalog.Magnet{{Infohash: "m-compilation", CreatedAt: "2020-09-22"}}},
	}

	got := mustSince(t, "2026-01-01").filter(log, works, false)

	kept := make(map[string]bool, len(got))
	for _, w := range got {
		kept[w.Number] = true
	}
	if kept["REISSUE"] {
		t.Error("磁链新不能把发行日期旧的作品救回来：since 比的是 release_date，" +
			"不是 created_at，也不是两者的并集")
	}
	if !kept["COMPILATION"] {
		t.Error("发行日期新就该保留，哪怕它选中的磁链是六年前的（合集再版）")
	}
}

// TestSinceBadShapeIs400OnEveryRoute 钉住三条路由都拦坏形状的 since。
//
// 三条都必须拦：`since` 是三条路由共用的语义，而它坏起来的样子是
// 「200 + 少了/空了的 feed」，用户看不出是 URL 写错了。
func TestSinceBadShapeIs400OnEveryRoute(t *testing.T) {
	src := &recordingSource{works: []catalog.Work{
		{Number: "A-1", ReleaseDate: "2026-06-01",
			Magnets: []catalog.Magnet{{Infohash: "h"}}}}}
	h := newTestServer(t, "provider: stub\n", src)

	routes := []string{
		"/rss/actress/EvkJ.xml?since=%s",
		"/rss/list/k4EVE4.xml?since=%s",
		"/rss/tags/0.xml?since=%s",
	}
	bad := []string{"2026-1-1", "hello", "2026-01-01T00:00:00Z", "2026-13-45"}
	for _, route := range routes {
		for _, since := range bad {
			target := strings.Replace(route, "%s", since, 1)
			before := src.worksCalls
			rec := do(t, h, target)
			if rec.Code != 400 {
				t.Errorf("%s 状态码 = %d, want 400（坏形状不能静默产出错 feed）",
					target, rec.Code)
				continue
			}
			if !strings.Contains(rec.Body.String(), "YYYY-MM-DD") {
				t.Errorf("%s 的 400 文案要写出期望形态: %s", target, rec.Body.String())
			}
			// 校验必须在**取数之前**：写错的 URL 不该花掉一次上游请求。
			if src.worksCalls != before {
				t.Errorf("%s 被判 400 却仍然向数据源取了作品（校验跑在取数之后）", target)
			}
		}
		// 合法形态必须放行 —— 否则「一律 400」就成了「一律拒绝」。
		target := strings.Replace(route, "%s", "2026-01-01", 1)
		before := src.worksCalls
		if rec := do(t, h, target); rec.Code != 200 {
			t.Errorf("%s 状态码 = %d, want 200", target, rec.Code)
		} else if src.worksCalls != before+1 {
			t.Errorf("%s 合法请求应当恰好取一次作品，实际 %d 次", target, src.worksCalls-before)
		}
	}
}

// TestBrowseRejectsYearOrMonthWithSince 补上全站路由缺的那条互斥检查。
//
// 全站路由的 year / month 是掩码里的正当维度，since 是本地的下界 ——
// 同时发必然是空 feed。女优路由早就有这条 400，全站此前没有，
// 而订阅链接生成器的文案已经写着「服务那侧也会判 400」。
func TestBrowseRejectsYearOrMonthWithSince(t *testing.T) {
	src := &recordingSource{works: []catalog.Work{
		{Number: "A-1", ReleaseDate: "2026-06-01",
			Magnets: []catalog.Magnet{{Infohash: "h"}}}}}
	h := newTestServer(t, "provider: stub\n", src)

	for _, q := range []string{"year=2021&since=2026-01-01", "month=3&since=2026-01-01"} {
		rec := do(t, h, "/rss/tags/0.xml?"+q)
		if rec.Code != 400 {
			t.Errorf("全站 %s 状态码 = %d, want 400", q, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "不能同时给") {
			t.Errorf("全站 %s 的文案要说清原因: %s", q, rec.Body.String())
		}
	}
	// 单独给任一个都照常服务。
	for _, q := range []string{"year=2021", "month=3", "since=2026-01-01"} {
		if rec := do(t, h, "/rss/tags/0.xml?"+q); rec.Code != 200 {
			t.Errorf("全站只给 %s 时状态码 = %d, want 200", q, rec.Code)
		}
	}
}
