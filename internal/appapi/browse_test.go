package appapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// TestBrowseBuildsTheSiteWideMask 钉住全站掩码的**逐字符**形状。
//
// 这是本次新增里最容易静默错的一处：掩码错了上游不报错，
// 只会返回**别的作品**（写错槽位 = 筛了另一个维度）。
// 形状来自 App 的真实抓包 + 逐槽位实测，见 notes/tag-vocabulary.md 第 7 节：
//
//	{zone}:t:{main}:{tags}:{year}:{duration}:{month}
func TestBrowseBuildsTheSiteWideMask(t *testing.T) {
	cases := []struct {
		name string
		zone int
		sel  catalog.BrowseSelector
		want string
	}{
		// 空筛选这一条与抓包里 App 自己发的**逐字符相同**（`0:t:m::::`，
		// 7 段 6 个冒号）—— 这是「字段顺序与段数都对」最直接的证据：
		// 我们自己拼出来的基线跟 App 发的一模一样。
		{"空筛选（抓包里的基线）", 0, catalog.BrowseSelector{}, "0:t:::::"},
		{"主属性", 0, catalog.BrowseSelector{Main: "c"}, "0:t:c::::"},
		{"主属性多个", 0, catalog.BrowseSelector{Main: "c,m"}, "0:t:c,m::::"},
		// 以下五条都是**实测跑过的**掩码，不是照着实现抄的：
		// 每一条的期望值来自 notes/tag-vocabulary.md 第 7 节那张验证表。
		{"主属性+标签", 0, catalog.BrowseSelector{Main: "m", Tags: "68"}, "0:t:m:68:::"},
		{"主属性+标签多个", 0, catalog.BrowseSelector{Main: "m", Tags: "10,8"}, "0:t:m:10,8:::"},
		{"主属性+年份", 0, catalog.BrowseSelector{Main: "m", Year: "2020"}, "0:t:m::2020::"},
		{"主属性+年份+时长", 0, catalog.BrowseSelector{Main: "m", Year: "2020", Duration: "gt-120"}, "0:t:m::2020:gt-120:"},
		{"主属性+年份+月份", 0, catalog.BrowseSelector{Main: "m", Year: "2020", Month: "3"}, "0:t:m::2020::3"},
		{"不填主属性也合法（槽位留空）", 0, catalog.BrowseSelector{Tags: "68"}, "0:t::68:::"},
		{"用户给的那条完整掩码", 0, catalog.BrowseSelector{
			Main: "m", Tags: "68", Year: "2020", Duration: "gt-120", Month: "3",
		}, "0:t:m:68:2020:gt-120:3"},
		{"无码片库", 1, catalog.BrowseSelector{Main: "m"}, "1:t:m::::"},
		{"欧美片库", 2, catalog.BrowseSelector{}, "2:t:::::"},
		{"FC2 片库", 3, catalog.BrowseSelector{}, "3:t:::::"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := browseFilter(tc.zone, tc.sel)
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if got != tc.want {
				t.Errorf("掩码 = %q\n         want %q", got, tc.want)
			}
		})
	}
}

// TestBrowseRejectsOverFiveTags 是**上游硬限制**的落点。
//
// 实测把 6 个 id 交给上游时第 6 个被静默丢弃 —— 用户会以为筛了 6 个。
// 所以必须在本地拦住，而不是发出去。
func TestBrowseRejectsOverFiveTags(t *testing.T) {
	called := false
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"movies":[]}`))
	})

	_, err := c.Browse(context.Background(), 0, catalog.BrowseSelector{Tags: "1,2,3,4,5,6"}, url.Values{})
	if err == nil {
		t.Fatal("6 个标签应当被拦下")
	}
	if !strings.Contains(err.Error(), catalog.ErrBadRequest.Error()) {
		t.Errorf("应当可判定为 ErrBadRequest: %v", err)
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("文案里应当说清上限是 5: %v", err)
	}
	if called {
		t.Error("超限的请求不该发到上游")
	}

	// 恰好 5 个要放行。
	if _, err := browseFilter(0, catalog.BrowseSelector{Tags: "1,2,3,4,5"}); err != nil {
		t.Errorf("5 个标签应当放行: %v", err)
	}
}

// TestBrowseRejectsDurationWithoutYear 钉住一条实测的坑。
//
// `0:t:m:::90-120:` 返回的时长是 76–300 —— **根本没筛**。也就是说单独给时长
// 不会报错，只会静默失效。既然「静默失效」正是本项目最不能接受的一类失败，
// 那就在这里判成用户写错。
func TestBrowseRejectsDurationWithoutYear(t *testing.T) {
	_, err := browseFilter(0, catalog.BrowseSelector{Duration: "90-120"})
	if err == nil {
		t.Fatal("有时长没年份应当被拦下 —— 上游会静默忽略它")
	}
	if !strings.Contains(err.Error(), catalog.ErrBadRequest.Error()) {
		t.Errorf("应当可判定为 ErrBadRequest: %v", err)
	}
	if _, err := browseFilter(0, catalog.BrowseSelector{Year: "2020", Duration: "90-120"}); err != nil {
		t.Errorf("带上年份之后应当放行: %v", err)
	}
}

// TestBrowseRejectsBadZone 确认片库号写了别的值不是「静默换库」。
func TestBrowseRejectsBadZone(t *testing.T) {
	for _, zone := range []int{-1, 4, 99} {
		if _, err := browseFilter(zone, catalog.BrowseSelector{}); err == nil {
			t.Errorf("片库号 %d 应当被拦下（它决定打哪个库）", zone)
		}
	}
	for _, zone := range []int{0, 1, 2, 3} {
		if _, err := browseFilter(zone, catalog.BrowseSelector{}); err != nil {
			t.Errorf("片库号 %d 应当放行: %v", zone, err)
		}
	}
}

// TestBrowseRejectsBadSelectors 确认格式类错误都在本地拦住。
func TestBrowseRejectsBadSelectors(t *testing.T) {
	bad := []catalog.BrowseSelector{
		{Main: "cm"},                    // 主属性拼在一起 —— 上游静默忽略
		{Tags: "68,abc"},                // 标签不是数字
		{Year: "20"},                    // 年份不是四位
		{Year: "abcd"},                  //
		{Month: "13"},                   // 月份越界
		{Month: "0"},                    //
		{Year: "2020", Duration: "1小时"}, // 时长档位不认识
	}
	for _, sel := range bad {
		if _, err := browseFilter(0, sel); err == nil {
			t.Errorf("%+v 应当被拦下", sel)
		}
	}
}

// TestBrowseSendsTheMaskAndStripsOwnParams 确认掩码真的发出去了，
// 且自有参数不会被当成未知参数一起送给上游。
func TestBrowseSendsTheMaskAndStripsOwnParams(t *testing.T) {
	var got url.Values
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(envBody(`{"movies":[{"id":"m1","number":"A-1","magnets_count":0}]}`)))
	})

	sel := catalog.BrowseSelector{Main: "c", Tags: "68", Year: "2020", Duration: "gt-120", Month: "3"}
	works, err := c.Browse(context.Background(), 1, sel, url.Values{"sort_by": {"score"}})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(works) != 1 {
		t.Fatalf("得到 %d 部作品", len(works))
	}
	if f := got.Get("filter_by"); f != "1:t:c:68:2020:gt-120:3" {
		t.Errorf("filter_by = %q", f)
	}
	if got.Get("sort_by") != "score" {
		t.Errorf("sort_by 应当透传，得到 %q", got.Get("sort_by"))
	}
	if got.Get("limit") != "50" {
		t.Errorf("limit = %q，want 50（上游上限）", got.Get("limit"))
	}
}
