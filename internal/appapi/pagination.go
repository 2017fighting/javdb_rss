package appapi

// paging 是一次「按页读到空为止」读取的分页结果。
//
// 字段刻意是**未导出**的：它是 readAllPages 的内部产物，两个调用方各自把它
// 摊进自己的领域类型（catalog.Collection / catalog.WantList）—— 那两份类型是
// 对外的形状，不该被这里的实现细节牵着走。
type paging struct {
	truncated    bool
	pagesFetched int
	maxPages     int
}

// readAllPages 按页读取一份「按页返回、遇空页即到底」的清单。
//
// # 为什么它必须只有一份实现
//
// 上游对这类清单（收藏女优、想看）**不返回总数**，「到底了没」只能靠空页判断；
// 而我们又设了页数上限，于是有两条不同的停下方式必须被分清：
//
//	恰好装满上限（再探一页是空的）   → 这是一份**完整**的清单
//	上限之外还有数据（再探一页非空） → truncated，清单**已知不完整**
//
// 那次「多探一页」是本函数存在的核心理由。少了它，一个恰好读到上限位数的用户
// 会永远看到一条**假的**截断警告 —— 而一条会假响的警告，会让真响的那一次也没人信。
// 这条语义在两个调用方各写一遍，就多了一次写歪的机会，而写歪的表现是静默的
// （少报或假报截断），正是本服务反复要消除的那类失败。
//
// # 与 `internal/dedupe` 那处「刻意不抽」的区别
//
// dedupe 包里三个逐行重复的方法**故意**没抽成泛型 helper
// （见 .scratch/javdb-rss-followups/README.md）：那里的重复是**模板**，
// 抽出来要付 `any` 断言的代价。这里抽出的是**一条语义**，用类型参数表达，
// 不需要任何断言；而且两者写歪的代价不对等 —— 模板写歪了编译期或测试立刻知道，
// 截断语义写歪了只会安静地给出错误答案。
//
// # 参数
//
//	key   给出元素的去重身份。返回空字符串表示该元素被**丢弃**：
//	      身份不明（没有 id）的元素不该进一份按身份去重的清单，
//	      而两个调用方原本就都是这么做的。
//	      去重本身也是必要的：上游分页在数据变动时可能重叠。
//	fetch 拉第 page 页（从 1 开始）。它自己闭包捕获 ctx —— 本函数不碰网络。
//
// 出错时返回 nil 切片与错误（**不**保留已经读到的部分）：调用方据此让它
// 响亮地失败，而不是端出一份自己都说不清完整性的清单。
func readAllPages[T any](
	maxPages int,
	key func(T) string,
	fetch func(page int) ([]T, error),
) ([]T, paging, error) {
	var out []T
	seen := make(map[string]bool)
	keep := func(page []T) {
		for _, item := range page {
			k := key(item)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, item)
		}
	}

	for page := 1; page <= maxPages; page++ {
		items, err := fetch(page)
		if err != nil {
			return nil, paging{}, err
		}
		if len(items) == 0 {
			// 空页 = 到底。这是一份**完整**的清单。
			return out, paging{pagesFetched: page, maxPages: maxPages}, nil
		}
		keep(items)
	}

	// 到达上限仍未遇空页。但「满页」不等于「还有更多」：恰好读到上限时，
	// 下一页同样是空的。再探一页才能分清。探针的内容**不入清单** ——
	// 触顶时刻意只给到上限为止。
	probe, err := fetch(maxPages + 1)
	if err != nil {
		return nil, paging{}, err
	}
	if len(probe) == 0 {
		return out, paging{pagesFetched: maxPages + 1, maxPages: maxPages}, nil
	}
	return out, paging{truncated: true, pagesFetched: maxPages, maxPages: maxPages}, nil
}
