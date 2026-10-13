package mteam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

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

// Source 是在 catalog.Source 之上做 M-Team 判定的装饰器。
//
// # 它只改一条路由
//
// **只改 `WantToWatch`**，别的路由原样透传。理由不是偷懒：准入条件就是
// 「作品在 App 的想看清单里」，而这条路由的内容按定义就是那份清单 ——
// 在别处再查一遍没有新信息。铺到所有路由上的代价更大：「判定未就绪就扣住」
// 会传染到女优订阅，使那条 feed 的内容取决于馒头通不通。
//
// # 首发冻结
//
// 一部作品**已有钉住** ⇒ 连问都不问。这是「来源永不自动更换」的落点：
// 换来源必然换 infohash（M-Team 是私种、`info.source` 被改过），
// 于是换来源 = 磁盘上多一份。唯一的出口是「重置」（删判定 + 删钉住 + HUP）。
//
// 它坐在 pin **外面**（先于 pin 执行），因此它改写的候选池会由 pin 照常钉住。
// 装配顺序的约束见 cmd/javdb-rss/main.go 里那段注释。
type Source struct {
	inner  catalog.Source
	client *Client
	store  *Store
	pins   Pins
	log    *slog.Logger

	// now 可注入，让判定时间在测试里可确定。
	now func() time.Time

	// OnFatal 在判定表落盘失败时被调用。它应当让进程退出。
	//
	// 用回调而不是 os.Exit：本包是可测的库代码，退出是 main 的决定。
	OnFatal func(error)
}

// New 包装一个 Source。
//
// pins 为 nil 时视为「没有任何作品被钉住」—— 那会让每一次下发都走 M-Team，
// 装配层不会这样用，但一个库不该在那种情况下 panic。
func New(inner catalog.Source, client *Client, store *Store, pins Pins, log *slog.Logger) *Source {
	if log == nil {
		log = slog.Default()
	}
	return &Source{inner: inner, client: client, store: store, pins: pins, log: log, now: time.Now}
}

var _ catalog.Source = (*Source)(nil)

// WantToWatch 是唯一被增强的路由。
func (s *Source) WantToWatch(ctx context.Context) (catalog.WantList, error) {
	list, err := s.inner.WantToWatch(ctx)
	if err != nil {
		return list, err
	}
	for i := range list.Works {
		s.enrich(ctx, &list.Works[i])
	}
	// 收尾时**只落盘一次**。无变更则零写入 —— qBittorrent 的常态轮询不碰磁盘。
	if s.store != nil {
		if err := s.store.Flush(); err != nil {
			if s.OnFatal != nil {
				s.OnFatal(err)
			} else {
				s.log.Error("判定表落盘失败，无法继续安全服务", "err", err)
			}
			return catalog.WantList{}, err
		}
	}
	return list, nil
}

// enrich 决定一部作品下发哪一份，必要时把候选池整体换掉。
//
// ⚠️ **它绝不返回错误。** 任何 M-Team 侧的失败都只降级成「这次按上游下发」——
// 一条 feed 不该因为第二个来源出问题而整条失败。
func (s *Source) enrich(ctx context.Context, w *catalog.Work) {
	// 首发冻结：下发过就不动，连问都不问。
	if s.pins != nil {
		if _, ok := s.pins.Get(w.ID); ok {
			return
		}
	}

	if v, ok := s.store.Get(w.ID); ok {
		switch v.Outcome {
		case OutcomeHit:
			s.apply(w, v)
		case OutcomeMiss, OutcomeFailed:
			// 两种都不动候选池：
			//   负结果是**判决** —— 查过、没有，永不重问；
			//   失败**不是判决** —— 但它也不自动重试（自动重试会在抖动时引发换源，
			//   也就是重复下载），唯一的重试入口是「重置」。
		default:
			// 认不出的结果（判定表被手改，或者将来加了新值而这里忘了处理）。
			// **说一声再跳过** —— 静默跳过会让那部作品永远拿不到 M-Team，
			// 而且看不出原因。
			s.log.ErrorContext(ctx, "判定表里有一个认不出的结果，本次跳过 M-Team",
				"作品", w.Number, "outcome", string(v.Outcome))
		}
		return
	}

	hits, err := s.client.Search(ctx, w.Number)
	if err != nil {
		// 失败**不构成判决**（「查过、没有」才是）—— 但它也不自动重试。
		s.recordFailure(ctx, w, err)
		return
	}

	matches := matchCode(hits, w.Number)
	if len(matches) == 0 {
		// 负结果：**永久**。它不重试、也不因为「也许以后有了」而重查。
		// 措辞刻意不叫「失败」—— 那是另一回事，而两者处置完全相反。
		s.store.Put(w.ID, Verdict{Outcome: OutcomeMiss, At: s.now()})
		s.log.DebugContext(ctx, "M-Team 上没有这部作品（负结果，永久）",
			"作品", w.Number, "候选", len(hits))
		return
	}

	chosen := pickBest(matches)
	data, err := s.client.Download(ctx, chosen.ID)
	if err != nil {
		// ⚠️ **也要记失败**，不能只打日志就走。
		//
		// 不记的话，那部作品下一轮会**重新搜索、重新下载** —— 而下载配额是
		// 100 个/小时。这是评审抓出来的一个真 bug：搜索命中之后的所有失败
		// （下载、解析、落盘）当时都被漏掉了，于是它们每轮重来一遍。
		s.recordFailure(ctx, w, err)
		s.log.WarnContext(ctx, "取 M-Team 的种子失败，本次按上游下发",
			"作品", w.Number, "种子", chosen.ID, "err", err)
		return
	}
	infohash, err := Infohash(data)
	if err != nil {
		// 这一条值得 WARN 而不是 Debug：它是**M-Team 的契约变了**的信号
		// （种子形状不再是我们认得的），而不是某一次网络抖动。
		s.recordFailure(ctx, w, err)
		s.log.WarnContext(ctx, "M-Team 的种子认不出来，本次按上游下发",
			"作品", w.Number, "种子", chosen.ID, "err", err)
		return
	}
	if err := s.store.SaveTorrent(infohash, data); err != nil {
		// 落盘失败同样要记：不记的话它也会每轮重来一次。
		s.recordFailure(ctx, w, err)
		s.log.ErrorContext(ctx, "存 M-Team 的种子失败，本次按上游下发",
			"作品", w.Number, "infohash", infohash, "err", err)
		return
	}

	v := Verdict{
		Outcome: OutcomeHit,
		Chosen: Candidate{
			ID:        chosen.ID,
			Infohash:  infohash,
			Name:      chosen.Name,
			SizeMiB:   int(chosen.SizeBytes / (1 << 20)),
			CNSub:     chosen.CNSub,
			CreatedAt: chosen.CreatedAt,
		},
		At: s.now(),
	}
	s.store.Put(w.ID, v)
	s.apply(w, v)
	s.log.InfoContext(ctx, "M-Team 命中，本作品改发馒头那份",
		"作品", w.Number, "种子", chosen.ID, "infohash", infohash,
		"中字", chosen.CNSub, "候选", len(matches))
}

// recordFailure 记下一次**没查成**，并按类别决定日志的措辞。
//
// 它落盘，因此不会每轮重问 —— 但那条记录与「负结果」是**两回事**：
// 负结果是「查过、没有」，失败是「没查成」。前者是判决，后者不是，
// 因此只有后者会被「重置」再试一次。
func (s *Source) recordFailure(ctx context.Context, w *catalog.Work, err error) {
	kind := FailureOther
	if errors.Is(err, ErrCredential) {
		kind = FailureCredential
	}
	s.store.Put(w.ID, Verdict{
		Outcome:     OutcomeFailed,
		Failure:     err.Error(),
		FailureKind: kind,
		At:          s.now(),
	})

	// 两类失败的日志**必须不同**：一类要人去换 key，另一类等一等就好。
	// 混成一条，告警就会把人指向错的方向。
	if kind == FailureCredential {
		s.log.ErrorContext(ctx, "M-Team 凭据失效 —— 重试无用，去控制台换一个存取令牌",
			"作品", w.Number, "err", err)
		return
	}
	s.log.WarnContext(ctx, "M-Team 查询失败，本次按上游下发（不自动重试）",
		"作品", w.Number, "err", err)
}

// apply 把命中换成候选池 —— **整体替换**，不是合并。
//
// 池子里只有一条，因为 M-Team 的候选**要下载才知道 infohash**，
// 而我们只下载选中的那条。于是「选哪条」这件事发生在下载**之前**
// （见 pickBest），用的是搜索元数据；池子本身是单元素。
//
// 这与「来源优先」是同一件事的两种说法：M-Team 命中时，上游的候选被丢弃。
func (s *Source) apply(w *catalog.Work, v Verdict) {
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

// --- 命中判定 -----------------------------------------------------------------

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

// describe 是给日志/报错用的一行描述。
func (h SearchHit) String() string {
	return fmt.Sprintf("%s(%s, %d MiB, 中字=%v)", h.ID, h.Name, h.SizeBytes/(1<<20), h.CNSub)
}
