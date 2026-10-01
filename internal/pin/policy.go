package pin

import (
	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// Policy 是「选哪条磁链」这条规则的接缝，也是 ticket 08 与 09 的分界线。
//
// 两个方法各自对应一类问题，刻意分开：
//
//	Desired      —— 纯函数：**假如重新选**，该选哪条。（ticket 04 的规则）
//	KeepOrSwitch —— 有状态策略：已经钉住了一条时，要不要换。（ticket 09 的规则）
//
// 把两者混在一起写，会让纯函数测试被「消失、同日、旧 cnsub 回潮」这类
// 状态边界污染 —— 那正是 09 单列一票要避免的。
type Policy interface {
	// Desired 计算「若此刻重新选，应当下发哪条」。
	Desired(cands []catalog.Magnet) (catalog.Magnet, bool)

	// KeepOrSwitch 决定是沿用已钉住的 pin，还是换成 desired。
	//
	// pinned 是磁盘上那条记录（pinnedOK 为 false 表示还没有 pin ——
	// 那时一律采用 desired）。“pin 是否还在上游”不由这里判断：ticket 09
	// 已定 pin 消失时沿用快照，由 Source 负责记日志（因此这里不需要候选列表）。
	//
	// 返回的 bool 表示这次选择是否改变了 pin（调用方据此计数与落盘）。
	KeepOrSwitch(pinned Record, pinnedOK bool, desired catalog.Magnet) (catalog.Magnet, bool)
}

// DefaultPolicy 是 ticket 09 定稿的策略。
//
// # Desired：纯函数
//
// 就是 catalog.Select —— 最新的 cnsub，否则最新的候选；同日用 infohash 定序。
//
// # KeepOrSwitch：有状态，只有出现 cnsub 才切换
//
//	pin 不存在            → 采用 desired（首次选定）
//	pin 不是 cnsub        → desired 是 cnsub 就切，否则不切
//	pin 是 cnsub          → desired 是 cnsub 且 created_at **严格更新**才切
//
// 「只有普通候选变了」永远不触发切换 —— 这正是 pin 要消除的抖动。
//
// 两条刻意的边界：
//
//   - **同日不切**（第三行要求严格更新）。created_at 是日粒度，同日多条靠
//     infohash 定序；若允许同日 tie-break 触发切换，上游一次重排就能让 guid 抖。
//     日期无法解析时按 catalog.CompareCreatedAt 的规则：只有一边可解析时
//     可解析的算更新（因此快照无日期会被一次带日期的 cnsub 切换一次），
//     两边都不可解析时视为同日。
//   - **pin 指向上游消失时不在这里处理**。本策略只看 `desired`，不看 pin 是否
//     仍在候选里，因此 pin 消失后仍会沿用快照（可能死链）—— 由 Source 记录
//     一条日志让它可见（ticket 09：宁可稳定的死链，也不静默换 guid）。
type DefaultPolicy struct{}

var _ Policy = DefaultPolicy{}

// Desired 实现 Policy：把纯函数完全委托给 catalog.Select。
func (DefaultPolicy) Desired(cands []catalog.Magnet) (catalog.Magnet, bool) {
	return catalog.Select(cands)
}

// KeepOrSwitch 实现 Policy。见 DefaultPolicy 的文档。
func (DefaultPolicy) KeepOrSwitch(pinned Record, pinnedOK bool, desired catalog.Magnet) (catalog.Magnet, bool) {
	if !pinnedOK {
		return desired, true
	}

	current := magnetOf(pinned)

	// 没有 cnsub 就没有切换的理由 —— 普通候选的变化不该动 guid。
	if !desired.CNSub {
		return current, false
	}
	// pin 还不是 cnsub：cnsub 优先，立刻切。
	if !current.CNSub {
		return desired, true
	}
	// 两边都是 cnsub：只在日期严格更新时切（同日不算更新）。
	if catalog.CompareCreatedAt(desired.CreatedAt, current.CreatedAt) > 0 {
		return desired, true
	}
	return current, false
}

// magnetOf 把一条 pin 记录还原成 catalog.Magnet。
//
// 这就是记录里带磁链快照的用途：即使用户此刻的候选列表里已经没有它
// （上游删了/换了），我们仍能渲染出一条完整的 feed item。
func magnetOf(r Record) catalog.Magnet {
	return catalog.Magnet{
		Infohash:  r.Infohash,
		Name:      r.Name,
		SizeMiB:   r.SizeMiB,
		CNSub:     r.CNSub,
		CreatedAt: r.CreatedAt,
	}
}

// recordOf 把一条被选中的磁链快照成 pin 记录（magnetOf 的逆）。
//
// ⚠️ 这两个函数共享一份字段清单，改 contract 时**必须一起改**：
// 漏一个字段不会编译失败，只会让钉住的选择在重启后静默丢掉那个属性
// （例如 cnsub 变 false，于是 feed 标题少一个「中文字幕」前缀）。
func recordOf(m catalog.Magnet) Record {
	return Record{
		Infohash:  m.Infohash,
		Name:      m.Name,
		SizeMiB:   m.SizeMiB,
		CNSub:     m.CNSub,
		CreatedAt: m.CreatedAt,
	}
}
