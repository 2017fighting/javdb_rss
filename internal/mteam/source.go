package mteam

import (
	"context"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/pin"
)

// Pins 是装饰器对「钉住表」的**只读**需求。
//
// 只声明这一个方法，而不是把整个 pin.Store 传进来：本包要问的只有
// 「这部作品下发过没有」，而「谁在写这张表」不该是它的事。
// `*pin.Store` 天然满足它（Go 的接口是结构性的）。
type Pins interface {
	Get(workID string) (pin.Record, bool)
}

// Source 是把 M-Team 的判定**应用到 feed 上**的装饰器。
//
// # 它只读，不查
//
// 它**不发任何 M-Team 请求**。判定是后台的 Loop 做的（那个是写者），
// 而这里只做两件事：把已有的命中换成候选池、把**还没判定**的扣住。
//
// 这条分工不是审美：`/rss/want.xml` 的一次渲染已经要 ~20 秒
// （24 页清单 + 222 次磁链请求），而 qBittorrent 在 **30 秒无字节传输**时就掐断。
// 把判定搬回请求路径的代价不是「慢一点」，是整条 feed 报错。
//
// # 它只改一条路由
//
// **只改 `WantToWatch`**，别的路由原样透传。准入条件就是「作品在 App 的想看清单里」，
// 而这条路由的内容按定义就是那份清单 —— 在别处再查一遍没有新信息。
//
// # 首发冻结
//
// 一部作品**已有钉住** ⇒ 原样放行（连判定表都不看）。这是「来源永不自动更换」的落点：
// 换来源必然换 infohash（M-Team 是私种、`info.source` 被改过），于是换来源 = 多一份。
// 唯一的出口是「重置」（删判定 + 删钉住 + HUP）。
type Source struct {
	inner catalog.Source
	store *Store
	pins  Pins
	log   *slog.Logger
}

// New 包装一个 Source。
//
// pins 为 nil 时视为「没有任何作品被钉住」—— 那会让每一次下发都走判定表，
// 装配层不会这样用，但一个库不该在那种情况下 panic。
func New(inner catalog.Source, store *Store, pins Pins, log *slog.Logger) *Source {
	if log == nil {
		log = slog.Default()
	}
	return &Source{inner: inner, store: store, pins: pins, log: log}
}

var _ catalog.Source = (*Source)(nil)

// WantToWatch 应用判定，并把还没判定的作品扣住。
func (s *Source) WantToWatch(ctx context.Context) (catalog.WantList, error) {
	list, err := s.inner.WantToWatch(ctx)
	if err != nil {
		return list, err
	}

	// ⚠️ **不能原地改** list.Works 的底层数组。
	//
	// 本装饰器在 dedupe **里面**，因此它看到的切片不会被别的 goroutine 同时读 ——
	// 但把结果缩容到同一个底层数组上（`works[:0]`）会让「上游那批」与「我们这批」
	// 共享内存，而调用方拿到的是前者。新建一个切片最省事，代价可忽略。
	kept := make([]catalog.Work, 0, len(list.Works))
	pending := 0

	for i := range list.Works {
		w := list.Works[i] // 值拷贝：下面改写 Magnets 时不动上游那份

		// 首发冻结：下发过就原样放行。
		if s.pins != nil {
			if _, ok := s.pins.Get(w.ID); ok {
				kept = append(kept, w)
				continue
			}
		}

		v, ok := s.store.Get(w.ID)
		if !ok {
			// **待定来源**：扣住不发。计数交给上层写进标题 ——
			// 静默扣住就是静默少给数据。
			pending++
			continue
		}

		switch v.Outcome {
		case OutcomeHit:
			apply(&w, v)
		case OutcomeMiss, OutcomeFailed:
			// 两种都原样放行（发上游那条）：负结果是判决，失败不是判决但也不自动重试。
			// 它们由 pin 照常钉住 —— 而那正是「来源永不自动更换」要的。
		default:
			// 认不出的结果（判定表被手改，或者将来加了新值而这里忘了处理）。
			// **说一声再跳过** —— 静默跳过会让那部作品永远拿不到 M-Team，
			// 而且看不出原因。
			s.log.ErrorContext(ctx, "判定表里有一个认不出的结果，按未判定处理",
				"作品", w.Number, "outcome", string(v.Outcome))
			pending++
			continue
		}
		kept = append(kept, w)
	}

	list.Works = kept
	list.PendingSource = pending
	return list, nil
}

// apply 把命中换成候选池 —— **整体替换**，不是合并。
//
// 池子里只有一条，因为 M-Team 的候选**要下载才知道 infohash**，
// 而我们只下载选中的那条。于是「选哪条」这件事发生在下载**之前**
// （见 Loop 里的 pickBest），用的是搜索元数据；池子本身是单元素。
//
// 这与「来源优先」是同一件事的两种说法：命中时，上游的候选被丢弃。
func apply(w *catalog.Work, v Verdict) {
	w.Magnets = []catalog.Magnet{{
		Source:    catalog.SourceMTeam,
		Infohash:  v.Chosen.Infohash,
		Name:      v.Chosen.Name,
		SizeMiB:   v.Chosen.SizeMiB,
		CNSub:     v.Chosen.CNSub,
		CreatedAt: v.Chosen.CreatedAt,
	}}
}

// --- 其余路由：原样透传 -------------------------------------------------------

func (s *Source) Code(ctx context.Context, code string) ([]catalog.Work, error) {
	return s.inner.Code(ctx, code)
}

func (s *Source) Actress(ctx context.Context, id string, params url.Values) ([]catalog.Work, error) {
	return s.inner.Actress(ctx, id, params)
}

func (s *Source) ActressName(ctx context.Context, id string) (string, error) {
	return s.inner.ActressName(ctx, id)
}

func (s *Source) CollectedActresses(ctx context.Context) (catalog.Collection, error) {
	return s.inner.CollectedActresses(ctx)
}

func (s *Source) CollectedLists(ctx context.Context) (catalog.ListCollection, error) {
	return s.inner.CollectedLists(ctx)
}

func (s *Source) List(ctx context.Context, id string, params url.Values) ([]catalog.Work, error) {
	return s.inner.List(ctx, id, params)
}

func (s *Source) ListName(ctx context.Context, id string) (string, error) {
	return s.inner.ListName(ctx, id)
}

func (s *Source) TagVocabulary(ctx context.Context, zone int) (catalog.TagVocabulary, error) {
	return s.inner.TagVocabulary(ctx, zone)
}

func (s *Source) ActressTags(ctx context.Context, id string) (catalog.ActressTags, error) {
	return s.inner.ActressTags(ctx, id)
}

func (s *Source) Browse(ctx context.Context, zone int, sel catalog.BrowseSelector, params url.Values) ([]catalog.Work, error) {
	return s.inner.Browse(ctx, zone, sel, params)
}

// --- 命中判定（Loop 用） -------------------------------------------------------

// matchCode 挑出名字里**真的**是这部作品的候选。
//
// 判据是「番号两侧不是字母数字」—— 分隔符允许 `-` / `_` / 空格 / 直接相连，
// 于是 `SONE-168`、`SONE168`、`SONE 168` 都算，而 `SONE-1680` 与 `XSONE-168` 都不算。
//
// ⚠️ **不能**实现成「按非字母数字切词再比词元」：日文汉字不是字母数字，
// 于是 `SONE-168 激イキ…` 会被切成 `SONE` + `168` + …，一条都匹配不上，
// 而它的表现是「馒头没有」—— 看起来像 M-Team 没收录，其实是判据坏了。
//
// ⚠️ 边界判断是**手工**做的，因为 Go 的 regexp（RE2）**不支持环视**
// （`(?<!…)` / `(?=…)` 都会 panic）。别顺手把它改回环视写法。
func matchCode(hits []SearchHit, code string) []SearchHit {
	rx := codePattern(code)
	if rx == nil {
		return nil
	}
	var out []SearchHit
	for _, h := range hits {
		if hasBoundedMatch(rx, h.Name) {
			out = append(out, h)
		}
	}
	return out
}

// codeSeparator 是番号里那些「不算字符」的分隔符。
//
// 提到包级是为了不在每次匹配时重新编译 —— 它在一轮渲染里会被调用上百次。
var codeSeparator = regexp.MustCompile(`[^A-Za-z0-9]+`)

// codePattern 把番号编成一条「分隔符可浮动」的正则。
func codePattern(code string) *regexp.Regexp {
	var parts []string
	for _, p := range codeSeparator.Split(code, -1) {
		if p != "" {
			parts = append(parts, regexp.QuoteMeta(p))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return regexp.MustCompile(`(?i)` + strings.Join(parts, `[\s\-_]*`))
}

// hasBoundedMatch 报告 s 里有没有一次「两侧都不是字母数字」的匹配。
func hasBoundedMatch(rx *regexp.Regexp, s string) bool {
	for _, loc := range rx.FindAllStringIndex(s, -1) {
		if !isAlnumAt(s, loc[0]-1) && !isAlnumAt(s, loc[1]) {
			return true
		}
	}
	return false
}

// isAlnumAt 报告下标 i 处的字符是不是 ASCII 字母或数字（越界算「不是」）。
func isAlnumAt(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// pickBest 在命中的候选里选一条。
//
// 排序键依次是：中文字幕优先 → `created_at` 较新 → torrent id 字典序。
//
// ⚠️ 与 `catalog.Select` 的槽位规则**差一个键**：那里第三条是 infohash 字典序，
// 而这里用 torrent id —— 因为 M-Team 的候选要下载才知道 infohash，
// 选哪条必须发生在下载之前。这是「来源优先」这条设计的必然代价，不是疏漏。
//
// `created_at` 直接按字符串比：线格式是 `2021-02-22 15:19:51`（定宽、零填充），
// 因此字典序与时间序一致。**不解析成 time.Time** —— 线格式变时，
// 解析失败会退化成「全都相等」，而字符串比较只会退化成「按字面排」，都还能用。
func pickBest(hits []SearchHit) SearchHit {
	best := hits[0]
	for _, h := range hits[1:] {
		if betterHit(h, best) {
			best = h
		}
	}
	return best
}

func betterHit(a, b SearchHit) bool {
	if a.CNSub != b.CNSub {
		return a.CNSub
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt > b.CreatedAt
	}
	return a.ID < b.ID
}
