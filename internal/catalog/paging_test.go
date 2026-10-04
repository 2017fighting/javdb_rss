package catalog

import (
	"net/url"
	"testing"
)

// TestPageCount 钉住 `pages` 的读法：缺省 1、写坏当 1、超上限夹到 MaxPages。
//
// 这个函数原本是 appapi 的私有实现，现在上提到 catalog —— 因为 httpapi 也要用
// 同一个答案（判断「取数窗是否取满」）。两处各写一份的后果是它们会漂：
// 上游夹到 20 而本地按 999 算，窗口判断就成了错的。
func TestPageCount(t *testing.T) {
	tests := []struct {
		name  string
		pages string
		want  int
	}{
		{"缺省", "", 1},
		{"正常", "3", 3},
		{"零与负数当 1", "0", 1},
		{"负数当 1", "-2", 1},
		{"写坏当 1", "abc", 1},
		{"空白当 1", "  ", 1},
		{"超上限夹到 MaxPages", "999", MaxPages},
		{"正好上限", "20", 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := url.Values{}
			if tt.pages != "" {
				params.Set("pages", tt.pages)
			}
			if got := PageCount(params); got != tt.want {
				t.Errorf("PageCount(pages=%q) = %d, want %d", tt.pages, got, tt.want)
			}
		})
	}
}

// TestWindowFull 钉住「取数窗是否取满」的判据。
//
// 它是 since 下界检查的输入：窗口取满 = 不能证明上游已经到底，因此可能还有
// 更旧的作品没取到。**它只回答「不能证明到底」，不回答「一定有更旧的」** ——
// 因此它是必要条件检测，宁可漏报也不误报（误报会在数据完整时喊狼来了）。
func TestWindowFull(t *testing.T) {
	tests := []struct {
		name  string
		count int
		pages string
		want  bool
	}{
		{"一页取满 50", 50, "", true},
		{"一页只有 49（上游到底了）", 49, "", false},
		{"两页取满 100", 100, "2", true},
		{"两页只回 99（到底了）", 99, "2", false},
		{"一条都没有", 0, "", false},
		{"条数超过窗口（分页重叠等原因）", 150, "2", true},
		{"请求 999 页时按夹过的 20 页算", 50, "999", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := url.Values{}
			if tt.pages != "" {
				params.Set("pages", tt.pages)
			}
			if got := WindowFull(tt.count, params); got != tt.want {
				t.Errorf("WindowFull(%d, pages=%q) = %v, want %v", tt.count, tt.pages, got, tt.want)
			}
		})
	}

	// 上限必须与 appapi 实际使用的那个常量同源，否则窗口判断会算错。
	if UpstreamPageLimit != 50 {
		t.Errorf("UpstreamPageLimit = %d, want 50（实测服务端上限）", UpstreamPageLimit)
	}
}
