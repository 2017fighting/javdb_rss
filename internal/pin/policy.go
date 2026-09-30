package pin

import (
	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// Policy 是「选哪条磁链」这条规则的接缝，也是 ticket 08 与 09 的分界线。
//
// 两个方法各自对应一类问题，刻意分开：
//
//	Desired      —— 纯函数：**假如重新选**，该选哪条。（04 的规则）
//	KeepOrSwitch —— 有状态策略：已经钉住了一条时，要不要换。（09 的规则）
//
// 把两者混在一起写，会让纯函数测试被「消失、同日、旧 cnsub 回潮」这类
// 状态边界污染 —— 那正是 09 单列一票要避免的。
//
// ticket 08 只提供这个接口与一个**临时**实现（TemporaryPolicy），
// 04 的 created_at 规则与 09 的切换语义都由 09 落地到具体类型上。
type Policy interface {
	// Desired 计算「若此刻重新选，应当下发哪条」。
	Desired(cands []catalog.Magnet) (catalog.Magnet, bool)

	// KeepOrSwitch 决定是沿用已钉住的 pin，还是换成 desired。
	//
	// pinned 是磁盘上那条记录（pinnedOK 为 false 表示还没有 pin ——
	// 那时一律采用 desired）。cands 是**当前**上游给的候选，
	// 想判断「pin 是否还在上游」的策略需要它。
	//
	// 返回的 bool 表示这次选择是否改变了 pin（调用方据此计数与落盘）。
	KeepOrSwitch(pinned Record, pinnedOK bool, cands []catalog.Magnet, desired catalog.Magnet) (catalog.Magnet, bool)
}

// TemporaryPolicy 是 ticket 08 的占位策略。
//
// 它的行为刻意是**最小**的：
//
//	Desired      = catalog.Select     —— 今天的槽位规则，一行没改
//	KeepOrSwitch = 永久沿用已有的 pin —— 钉住即不切
//
// 「钉住即不切」是刻意的：真正决定「什么时候切换」的是 ticket 09
// （什么算新 cnsub、pin 消失怎么办、同日怎么定序）。在 09 定稿前，
// 任何自作聪明的切换都会变成一条未经讨论的规则。
//
// TODO(ticket-09): 用实现 04 的 created_at 纯函数与 09 的切换语义的策略替换它。
type TemporaryPolicy struct{}

var _ Policy = TemporaryPolicy{}

// Desired 实现 Policy。它**就是** catalog.Select，不引入任何新规则。
func (TemporaryPolicy) Desired(cands []catalog.Magnet) (catalog.Magnet, bool) {
	return catalog.Select(cands)
}

// KeepOrSwitch 实现 Policy：有 pin 就用 pin，没有就用纯函数的结果。
func (TemporaryPolicy) KeepOrSwitch(pinned Record, pinnedOK bool, _ []catalog.Magnet, desired catalog.Magnet) (catalog.Magnet, bool) {
	if pinnedOK {
		return magnetOf(pinned), false
	}
	return desired, true
}

// magnetOf 把一条 pin 记录还原成 catalog.Magnet。
//
// 这就是记录里带磁链快照的用途：即使用户此刻的候选列表里已经没有它
// （上游删了/换了），我们仍能渲染出一条完整的 feed item。
func magnetOf(r Record) catalog.Magnet {
	return catalog.Magnet{
		Infohash:  r.Infohash,
		Name:      r.Name,
		SizeMB:    r.SizeMB,
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
		SizeMB:    m.SizeMB,
		CNSub:     m.CNSub,
		CreatedAt: m.CreatedAt,
	}
}
