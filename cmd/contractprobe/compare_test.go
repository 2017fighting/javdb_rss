package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// 本文件只测**纯函数**：比较、集合运算、宽容解析。
//
// 探针的其余部分是「发请求 + 打印」，对它做单测的价值低（要拿网络或假上游，
// 而它测的对象本来就是真实上游）。但上面这些纯函数恰恰是最容易悄悄写错、
// 而错了之后**结论会看起来完全正常**的那部分 ——
// 比如把「集合相同」与「逐位同序」弄混，整张对照表就会反过来读。
// 因此它们必须有钉住行为的测试。

// TestSameSeq 防的是「顺序被悄悄忽略」。
//
// 整个对照实验的结论都建立在「逐位同序」这个判断上：一旦它退化成「集合相同」，
// 「参数只改了排序」与「参数筛掉了东西」就会被混为一谈，而这两件事对 feed 的意义
// 完全不同（前者只是顺序，后者是条目变化）。
func TestSameSeq(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"完全相同", []string{"a", "b", "c"}, []string{"a", "b", "c"}, true},
		{"都为空", nil, nil, true},
		{"顺序不同", []string{"a", "b"}, []string{"b", "a"}, false},
		{"长度不同", []string{"a"}, []string{"a", "b"}, false},
		{"只差一个", []string{"a", "b", "c"}, []string{"a", "x", "c"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameSeq(c.a, c.b); got != c.want {
				t.Errorf("sameSeq(%v, %v) = %v, 想要 %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestDiffStat 是这套对照实验的地基：它必须把三件**不同**的事分开 ——
// 逐位相同、同集合但重排、集合也不同。
//
// 混成一个布尔的话，「sort_by 生效了」与「filter_by_tags 真的筛掉了东西」
// 这类结论就无法区分，而那正是本票要回答的问题。
func TestDiffStat(t *testing.T) {
	base := []string{"a", "b", "c", "d"}

	t.Run("逐位相同", func(t *testing.T) {
		first, diffs, sameSet := diffStat(base, []string{"a", "b", "c", "d"})
		if first != -1 || diffs != 0 || !sameSet {
			t.Errorf("想要 (-1,0,true)，得到 (%d,%d,%v)", first, diffs, sameSet)
		}
	})

	t.Run("同集合但重排", func(t *testing.T) {
		first, diffs, sameSet := diffStat(base, []string{"b", "a", "d", "c"})
		if first != 0 {
			t.Errorf("首个错位应当是 0，得到 %d", first)
		}
		if diffs != 4 {
			t.Errorf("四个位置都不同，得到 %d", diffs)
		}
		if !sameSet {
			t.Error("同一批元素重排之后，sameSet 必须为 true —— 这是「只改了排序」的判据")
		}
	})

	t.Run("集合也不同", func(t *testing.T) {
		_, _, sameSet := diffStat(base, []string{"a", "b", "c", "e"})
		if sameSet {
			t.Error("换掉一个元素之后 sameSet 必须为 false")
		}
	})

	t.Run("右侧更短", func(t *testing.T) {
		first, diffs, sameSet := diffStat(base, []string{"a", "b"})
		if first != 2 {
			t.Errorf("首个错位应当是「超出现有长度」的位置 2，得到 %d", first)
		}
		if diffs != 2 {
			t.Errorf("缺两个元素应当记 2 处不同，得到 %d", diffs)
		}
		if sameSet {
			t.Error("少给两个元素不可能是同一集合")
		}
	})

	t.Run("左侧为空", func(t *testing.T) {
		// 这条来自一个真实的坑：基线为空时，任何结果都会「与基线不同」——
		// 若不加区分，一个空基线会被读成「参数生效了」。
		first, _, sameSet := diffStat(nil, []string{"a"})
		if first != 0 {
			t.Errorf("想要首个错位 0，得到 %d", first)
		}
		if sameSet {
			t.Error("空集合与非空集合不是同一集合")
		}
	})
}

// TestIntersectSeqKeepsOrder 钉住「交集保留左侧顺序」。
//
// 保留 a 的顺序是有意的：组合的期望结果应当与基线同序，
// 交集若被重排，就分不清「组合筛错了」与「交集函数写错了」。
func TestIntersectSeqKeepsOrder(t *testing.T) {
	got := intersectSeq([]string{"a", "b", "c", "d"}, []string{"d", "c", "b"})
	want := []string{"b", "c", "d"}
	if !sameSeq(got, want) {
		t.Errorf("得到 %v，想要 %v（应当按左侧顺序）", got, want)
	}
	if len(intersectSeq(nil, []string{"a"})) != 0 {
		t.Error("空序列的交集必须是空")
	}
}

// TestSubsetsIsDeterministic 钉住子集枚举的顺序。
//
// 顺序必须确定，因为探针的输出会被贴进笔记：排列不确定的话，
// 两次复勘的输出就无法逐行对照 —— 而那正是「可复跑」的意义。
func TestSubsetsIsDeterministic(t *testing.T) {
	got := subsets([]string{"c", "a", "b"})
	want := [][]string{
		{"a"}, {"b"}, {"c"},
		{"a", "b"}, {"a", "c"}, {"b", "c"},
		{"a", "b", "c"},
	}
	if len(got) != len(want) {
		t.Fatalf("子集个数 %d，想要 %d（非空子集应为 2^n-1）", len(got), len(want))
	}
	for i := range want {
		if !sameSeq(got[i], want[i]) {
			t.Errorf("第 %d 个是 %v，想要 %v", i, got[i], want[i])
		}
	}

	// 同一个输入跑两次必须完全一样（不是靠 map 迭代顺序凑出来的）。
	if !sameSeq2(subsets([]string{"a", "b", "c"}), subsets([]string{"c", "b", "a"})) {
		t.Error("子集枚举对输入顺序敏感 —— 输出会随调用方式漂移")
	}
}

// sameSeq2 比较两个序列的序列。
func sameSeq2(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameSeq(a[i], b[i]) {
			return false
		}
	}
	return true
}

// TestOrderedPairsCoversBothDirections 防的是「顺序无关」这条结论测了个空。
//
// 「c,m 与 m,c 同序」只有在**两种写法都真的被请求过**时才成立。
// 如果这个枚举只产出单向对，测试会全绿而结论是假的 —— 本项目吃过这种假通过。
func TestOrderedPairsCoversBothDirections(t *testing.T) {
	got := orderedPairs([]string{"b", "a"})
	want := [][]string{{"a", "b"}, {"b", "a"}}
	if len(got) != len(want) {
		t.Fatalf("得到 %d 对，想要 %d", len(got), len(want))
	}
	for i := range want {
		if !sameSeq(got[i], want[i]) {
			t.Errorf("第 %d 对是 %v，想要 %v", i, got[i], want[i])
		}
	}
}

// TestSetHelpers 防的是「交集/子集写反」这类看不见的错误。
//
// 组合语义的结论是「逗号列表 = 各单属性的交集」。交集函数写错（比如顺序被重排、
// 或把重复元素当成多个）会让结论看起来仍然成立 —— 因为对照的另一半也用了同一个
// 错函数。因此这里按集合语义逐条钉住，包括「重复元素不放大计数」这一条。
func TestSetHelpers(t *testing.T) {
	if !isSubset([]string{"a"}, []string{"a", "b"}) {
		t.Error("a 应当是 {a,b} 的子集")
	}
	if isSubset([]string{"a", "z"}, []string{"a", "b"}) {
		t.Error("含 z 时不应当是子集")
	}
	if !isSubset(nil, []string{"a"}) {
		t.Error("空集是任何集合的子集")
	}
	if got := commonCount([]string{"a", "b"}, []string{"b", "c"}); got != 1 {
		t.Errorf("共同元素 1 个，得到 %d", got)
	}
	// 重复元素不应把计数放大：这是集合运算，不是多重集。
	if got := commonCount([]string{"a", "a"}, []string{"a", "a"}); got != 1 {
		t.Errorf("应当按集合计，得到 %d", got)
	}
}

// TestWalkFind 测的是「形状未知时也能把关心的字段挑出来」。
//
// 写死字段名的话，上游一改名探针就静默打印空 —— 而「什么都没找到」
// 恰恰是最需要被看见的信号。
func TestWalkFind(t *testing.T) {
	var tree any
	raw := `{"a":{"filter_tags":[{"id":"p","name":"可播放"}]},"b":[{"tags":[{"id":10}]}]}`
	if err := json.Unmarshal([]byte(raw), &tree); err != nil {
		t.Fatal(err)
	}
	var out []string
	walkFind(tree, "", map[string]bool{"filter_tags": true, "tags": true}, &out)

	if len(out) != 2 {
		t.Fatalf("应当找到 2 处，得到 %d：%v", len(out), out)
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "a.filter_tags") {
		t.Errorf("缺少 a.filter_tags：%s", joined)
	}
	if !strings.Contains(joined, "b[0].tags") {
		t.Errorf("缺少 b[0].tags（数组下标也要出现在路径里）：%s", joined)
	}
}

// TestExtractFilterTags 用的是上游真实的形状（实测确认）：
// 顶层 filter_tags 是 [{id, name}, ...]。
func TestExtractFilterTags(t *testing.T) {
	raw := json.RawMessage(`{"actor":{"id":"EvkJ"},"filter_tags":[{"id":"p","name":"可播放"},{"id":"v","name":"含预览视频"}]}`)
	got := extractFilterTags(raw)
	if len(got) != 2 || got[0].ID != "p" || got[1].Name != "含预览视频" {
		t.Fatalf("解析结果不对: %+v", got)
	}

	// 形状变了时返回空而不是崩 —— 上层据此提示「改用位置参数」。
	if got := extractFilterTags(json.RawMessage(`{"actor":{}}`)); got != nil {
		t.Errorf("没有 filter_tags 时应当返回空，得到 %+v", got)
	}
	if got := extractFilterTags(json.RawMessage(`不是 JSON`)); got != nil {
		t.Errorf("坏 JSON 应当返回空而不是崩，得到 %+v", got)
	}
}

// TestExtractIDsIsTolerant 钉住「宁可多收，不要返回空」。
//
// 空集会被读成「上游没有数据」——那是个假结论。多收几个 id 的代价只是
// 后面多打几次请求（而且会被去重）。
func TestExtractIDsIsTolerant(t *testing.T) {
	raw := json.RawMessage(`{"actors":[{"id":"a1"},{"id":"a2"}],"nested":{"id":"n1","child":{"id":"n2"}}}`)
	got := extractIDs(raw)
	want := map[string]bool{"a1": true, "a2": true, "n1": true, "n2": true}
	if len(got) != len(want) {
		t.Fatalf("得到 %v，想要 %d 个", got, len(want))
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("多收了 %q", id)
		}
	}
}

// TestAnyToString 处理的是 json 解出来的数字都是 float64 这件事。
//
// 标签 id 在上游是数字（"28" 也会给成 28），若直接用 %v 会得到 "28.000000"，
// 拼进 filter_by_tags 就是个查不到东西的值 —— 又一种静默失败。
func TestAnyToString(t *testing.T) {
	if got := anyToString(float64(28)); got != "28" {
		t.Errorf("float64 应当渲染成整数，得到 %q", got)
	}
	if got := anyToString("EvkJ"); got != "EvkJ" {
		t.Errorf("字符串应当原样返回，得到 %q", got)
	}
	if got := anyToString(nil); got != "" {
		t.Errorf("nil 应当是空串，得到 %q", got)
	}
}

// TestSanitizeName 保证端点路径能当文件名用 —— 证据落盘不能因为路径里的
// 斜杠与问号而失败（那会让「证据可复核」这条落空）。
func TestSanitizeName(t *testing.T) {
	got := sanitizeName("/api/v1/movies/tags?filter_by=0:a:EvkJ")
	for _, bad := range []string{"/", "?", "="} {
		if strings.Contains(got, bad) {
			t.Errorf("结果里不该出现 %q：%q", bad, got)
		}
	}
	if got == "" {
		t.Error("不能是空字符串，否则会写到一个没有名字的文件")
	}
}
