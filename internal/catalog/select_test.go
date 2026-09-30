package catalog

import "testing"

// magnet 是测试用的磁链构造器：只关心选择规则读到的三个字段。
func magnet(hash string, cnsub bool, createdAt string) Magnet {
	return Magnet{Infohash: hash, CNSub: cnsub, CreatedAt: createdAt}
}

// TestSelect 钉住 ticket 04/09 定下的选择规则：
//
//	有中文字幕 → 取 created_at 最新的中文字幕
//	否则       → 取 created_at 最新的候选
//	created_at 相同 → infohash 字典序（升序）定序
//	**不读取切片顺序**
func TestSelect(t *testing.T) {
	tests := []struct {
		name   string
		in     []Magnet
		want   string
		wantOK bool
	}{
		{"无候选", nil, "", false},
		{"空切片", []Magnet{}, "", false},
		{"单条普通", []Magnet{magnet("a", false, "2026-01-01")}, "a", true},
		{"单条中文字幕", []Magnet{magnet("a", true, "2026-01-01")}, "a", true},
		{"普通候选取最新", []Magnet{magnet("old", false, "2026-01-01"), magnet("new", false, "2026-03-01")}, "new", true},
		{
			"有中文字幕就忽略更新的普通候选",
			[]Magnet{magnet("newplain", false, "2026-03-01"), magnet("oldsub", true, "2026-01-01")},
			"oldsub", true,
		},
		{
			"中文字幕之间取最新",
			[]Magnet{magnet("subold", true, "2026-01-01"), magnet("subnew", true, "2026-03-01")},
			"subnew", true,
		},
		{
			"同日的普通候选按 infohash 字典序",
			[]Magnet{magnet("bbb", false, "2026-01-01"), magnet("aaa", false, "2026-01-01")},
			"aaa", true,
		},
		{
			"同日的中文字幕按 infohash 字典序",
			[]Magnet{magnet("bbb", true, "2026-01-01"), magnet("aaa", true, "2026-01-01")},
			"aaa", true,
		},
		{
			"created_at 为空时按 infohash 字典序",
			[]Magnet{magnet("bbb", false, ""), magnet("aaa", false, "")},
			"aaa", true,
		},
		{
			"created_at 都无法解析时也按 infohash 字典序",
			[]Magnet{magnet("bbb", false, "unknown"), magnet("aaa", false, "garbage")},
			"aaa", true,
		},
		{
			"最新的一条在切片最后也要选中",
			[]Magnet{magnet("x", false, "2026-01-01"), magnet("y", false, "2026-02-01"), magnet("z", false, "2026-03-01")},
			"z", true,
		},
		{
			"最新的一条在切片最前也要选中",
			[]Magnet{magnet("z", false, "2026-03-01"), magnet("y", false, "2026-02-01"), magnet("x", false, "2026-01-01")},
			"z", true,
		},
		{
			"两种日期格式混用仍取最新",
			[]Magnet{magnet("sep", false, "09/27/2026"), magnet("oct", false, "2026-10-01")},
			"oct", true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Select(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("Select ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.Infohash != tt.want {
				t.Errorf("Select = %q, want %q", got.Infohash, tt.want)
			}
		})
	}
}

// TestSelectDoesNotReadSliceOrder 是本规则的核心性质：结果只由字段决定，
// 与候选在切片里的位置无关（上游顺序不可重放，ticket 04 实测）。
func TestSelectDoesNotReadSliceOrder(t *testing.T) {
	a := magnet("aaa", false, "2026-01-01")
	b := magnet("bbb", false, "2026-02-01")
	c := magnet("ccc", true, "2026-01-15")

	forward, _ := Select([]Magnet{a, b, c})
	backward, _ := Select([]Magnet{c, b, a})
	if forward.Infohash != backward.Infohash {
		t.Errorf("同一组候选换顺序结果不同：%q vs %q", forward.Infohash, backward.Infohash)
	}
	if forward.Infohash != "ccc" {
		t.Errorf("应当选中中文字幕那条，得到 %q", forward.Infohash)
	}
}

// TestCompareCreatedAt 钉住比较器：两种已知线格式都按“日”比较，
// 只有一边可解析时「可解析的算更新」，两边都不可解析时返回 0（交给 infohash 定序）。
func TestCompareCreatedAt(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2026-01-02", "2026-01-01", +1},
		{"2026-01-01", "2026-01-02", -1},
		{"2026-01-01", "2026-01-01", 0},
		{"09/27/2026", "2026-09-28", -1},
		{"2026-09-28", "09/27/2026", +1},
		{"09/27/2026", "09/27/2026", 0},
		// 只有一边可解析：可解析的算更新（空串/坏数据按最旧算）。
		{"", "2026-01-01", -1},
		{"2026-01-01", "", +1},
		{"garbage", "2026-01-01", -1},
		{"2026-01-01", "unknown", +1},
		// 两边都不可解析：视为相同（0），由 infohash 定序 —— 不拿原字符串比。
		{"", "", 0},
		{"garbage", "garbage", 0},
		{"garbage", "unknown", 0},
	}
	for _, tt := range tests {
		if got := CompareCreatedAt(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareCreatedAt(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestParseCreatedAt 覆盖 feed 里曾有一份的解析逻辑，现在集中到 catalog。
func TestParseCreatedAt(t *testing.T) {
	tests := []struct {
		in   string
		want string // RFC3339 日期；空表示解析失败
	}{
		{"2026-01-28", "2026-01-28T00:00:00Z"},
		{"09/27/2026", "2026-09-27T00:00:00Z"},
		{"", ""},
		{"garbage", ""},
	}
	for _, tt := range tests {
		got, ok := ParseCreatedAt(tt.in)
		if tt.want == "" {
			if ok {
				t.Errorf("ParseCreatedAt(%q) 应当失败，得到 %v", tt.in, got)
			}
			continue
		}
		if !ok {
			t.Errorf("ParseCreatedAt(%q) 解析失败", tt.in)
			continue
		}
		if got.UTC().Format("2006-01-02T15:04:05Z") != tt.want {
			t.Errorf("ParseCreatedAt(%q) = %v, want %s", tt.in, got, tt.want)
		}
	}
}
