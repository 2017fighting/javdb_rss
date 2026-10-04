package appapi

import (
	"fmt"
	"testing"
)

// 本文件直接钉住 readAllPages 的语义。
//
// 两个调用方（收藏女优 / 想看清单）各自还有自己的端到端测试，但那些测试
// 只覆盖到「它们各自用到的路径」；抽出来之后，那些**两边都依赖**的性质
// （空 key 被丢弃、跨页去重、探针只在触顶时发生、出错不保留半份）需要一个
// 直接的落点，否则某天有人为了修一边而改歪了共享语义，两边都只会静默出错。

// pageOf 造一个「页 → 元素」的取数函数，并记下被问过的页码。
func pageOf(t *testing.T, pages map[int][]string, asked *[]int) func(int) ([]string, error) {
	t.Helper()
	return func(page int) ([]string, error) {
		if asked != nil {
			*asked = append(*asked, page)
		}
		items, ok := pages[page]
		if !ok {
			t.Fatalf("被问了第 %d 页，而这一页没有安排（分页行为写歪了）", page)
		}
		return items, nil
	}
}

func ident(s string) string { return s }

// TestReadAllPagesStopsAtFirstEmptyPage 确认「空页 = 到底」，且空页本身算读过。
func TestReadAllPagesStopsAtFirstEmptyPage(t *testing.T) {
	var asked []int
	got, pg, err := readAllPages(20, ident, pageOf(t, map[int][]string{
		1: {"a", "b"},
		2: {"c"},
		3: {},
	}, &asked))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("读到 %v，顺序应当保持", got)
	}
	if pg.truncated {
		t.Error("遇到空页就是完整清单，不该报截断")
	}
	if pg.pagesFetched != 3 {
		t.Errorf("PagesFetched = %d，空页也算读过一次", pg.pagesFetched)
	}
	if pg.maxPages != 20 {
		t.Errorf("MaxPages = %d，应当原样报告生效的上限", pg.maxPages)
	}
	if len(asked) != 3 {
		t.Errorf("问了 %v，应当在第 3 页（空页）停下", asked)
	}
}

// TestReadAllPagesDedupesAcrossPages 确认跨页去重按 key 生效、顺序保持。
//
// 上游分页在清单变动时确实会重叠 —— 不去重会让同一部作品在 feed 里出现两次。
func TestReadAllPagesDedupesAcrossPages(t *testing.T) {
	got, _, err := readAllPages(20, ident, pageOf(t, map[int][]string{
		1: {"a", "b"},
		2: {"b", "c"}, // b 重复
		3: {},
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("得到 %v，去重后应当是 a b c（且保持首次出现的顺序）", got)
	}
}

// TestReadAllPagesDropsElementsWithoutIdentity 钉住一个容易改歪的边界：
//
// key 返回空字符串的元素被**丢弃**，而不是被当成「不参与去重但仍然收下」。
// 两个调用方（没有 id 的女优条目 / 没有 movie id 的作品）原本都是这么做的，
// 而「丢」与「收」在 feed 里的区别是：一个不存在的身份会变成一条 guid 为
// 空串的条目 —— 客户端把空 guid 全都当成同一条，行为无法预料。
func TestReadAllPagesDropsElementsWithoutIdentity(t *testing.T) {
	got, _, err := readAllPages(20, ident, pageOf(t, map[int][]string{
		1: {"a", "", "b"},
		2: {},
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("得到 %v，没有身份的元素应当被丢掉", got)
	}
}

// TestReadAllPagesReportsTruncationWhenDataBeyondCap 确认上限之外还有数据时
// 报截断，且**只给到上限为止**（探针那一页的内容不入清单）。
func TestReadAllPagesReportsTruncationWhenDataBeyondCap(t *testing.T) {
	pages := map[int][]string{}
	for page := 1; page <= 5; page++ {
		pages[page] = []string{fmt.Sprintf("p%d", page)}
	}
	got, pg, err := readAllPages(3, ident, pageOf(t, pages, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !pg.truncated {
		t.Error("上限之外还有数据，必须报 truncated")
	}
	if len(got) != 3 {
		t.Errorf("触顶时只给到上限为止，得到 %v", got)
	}
	if pg.pagesFetched != 3 || pg.maxPages != 3 {
		t.Errorf("页数信号 = %d/%d，want 3/3", pg.pagesFetched, pg.maxPages)
	}
}

// TestReadAllPagesCompleteWhenCapExactlyFills 是**最重要的一条边界**：
//
// 恰好装满上限时，第 cap+1 页是空的 —— 这是一份完整清单，不能报截断。
// 少了那次探针，一个恰好读到上限位数的用户会永远看到一条假警告；
// 而会假响的警告等于让真响的那次也没人信。
func TestReadAllPagesCompleteWhenCapExactlyFills(t *testing.T) {
	var asked []int
	pages := map[int][]string{
		1: {"a"},
		2: {"b"},
		3: {"c"},
		4: {}, // 探针页
	}
	got, pg, err := readAllPages(3, ident, pageOf(t, pages, &asked))
	if err != nil {
		t.Fatal(err)
	}
	if pg.truncated {
		t.Error("恰好装满上限是完整的清单，不该报截断")
	}
	if len(got) != 3 {
		t.Errorf("得到 %v", got)
	}
	if pg.pagesFetched != 4 {
		t.Errorf("PagesFetched = %d，应当把那次探针也算进去", pg.pagesFetched)
	}
	if len(asked) != 4 {
		t.Errorf("问了 %v，上限处必须多探一页", asked)
	}
}

// TestReadAllPagesErrorDiscardsPartialResult 确认出错时**不**端出半份清单。
//
// 保留已读到的部分看起来「更友好」，但那会让调用方无法判断手里这份清单的
// 完整性 —— 而本服务的立场一直是「宁可看见错误，也不要说不清完整性的数据」。
func TestReadAllPagesErrorDiscardsPartialResult(t *testing.T) {
	boom := fmt.Errorf("上游炸了")
	fetch := func(page int) ([]string, error) {
		if page == 2 {
			return nil, boom
		}
		return []string{"a"}, nil
	}
	got, _, err := readAllPages(20, ident, fetch)
	if err != boom {
		t.Errorf("err = %v，应当原样透出（可判定）", err)
	}
	if got != nil {
		t.Errorf("出错时不该保留已读到的部分，得到 %v", got)
	}
}
