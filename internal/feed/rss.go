package feed

import (
	"encoding/xml"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// urlEncode 把字符串编成可以塞进 query 参数的形式。
//
// 用 url.QueryEscape 后再把 '+' 换回 %20：QueryEscape 按 application/x-www-form-urlencoded
// 编码，把空格编成 '+'，而 '+' 在 query string 里确实等价于空格 —— 但 dn 的值最终会变成
// 客户端界面上的显示名，水画的加号会直接给用户看到。
// %20 在所有语境下都是空格的规范编码，没有这个岐义。
func urlEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// Meta 是 channel 级的信息（feed 自身的标题与链接）。
type Meta struct {
	Title       string
	Link        string
	Description string
	Language    string
	// BaseURL 是本服务的对外地址（配置里的 feed.base_url），**不带**路径。
	//
	// 它存在是因为条目的交付物可能是一个本服务的地址（M-Team 来源的候选发种子文件），
	// 而那个地址必须是绝对的 —— 相对的 URL 在 RSS 阅读器里无处可依。
	// 它为空时条目的地址退化成相对路径，与既有行为一致（<link> 本来就这样）。
	BaseURL string
}

// RSS 2.0 的线格式。字段顺序即输出顺序，qBittorrent 与主流阅读器对此不敏感，
// 但稳定的输出顺序让测试能直接做字节比对。
type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel channel  `xml:"channel"`
}

type channel struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Language    string `xml:"language,omitempty"`
	LastBuild   string `xml:"lastBuildDate,omitempty"`
	Items       []item `xml:"item"`
}

type item struct {
	Title     string    `xml:"title"`
	Link      string    `xml:"link"`
	GUID      guid      `xml:"guid"`
	PubDate   string    `xml:"pubDate,omitempty"`
	Category  string    `xml:"category,omitempty"`
	Enclosure enclosure `xml:"enclosure"`
}

type guid struct {
	// isPermaLink 必须为 false —— 我们的 guid 是 infohash，不是一个可访问的 URL。
	// 若声明为 true，部分阅读器会把 guid 当成链接去抓，得到 404。
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type enclosure struct {
	URL string `xml:"url,attr"`
	// Length 在磁力链接语境下没有真实含义，RSS 规范要求它存在。
	// 我们填 size 换算出的字节数；qBittorrent 不会用它做判断。
	//
	// size 的单位是 **MiB**（ticket 03 用网页的 data-size 核对确认），
	// 因此换算是 * 1024 * 1024。这句话先前只是「假定为兆字节」——
	// 假定的方向恰好是对的，但当时的代码不该被当成依据。
	Length int    `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// enclosureMIMEType 是交付物的 enclosure MIME 类型。
//
// 用 application/x-bittorrent 而不是规范的 application/x-bittorrent.magnet，
// 因为前者是 qBittorrent 与主流客户端实际识别的那个。
//
// ⚠️ 它**两种来源共用**：qBittorrent 只在 type 等于这个值时才把 enclosure 当成种子来源
// （type 为空会落到它的备选字段，别的值**直接忽略**）—— 所以 M-Team 来源的条目
// 也不能换成 application/x-bittorrent.torrent 之类「更准确」的名字。
const enclosureMIMEType = "application/x-bittorrent"

// Render 写出一个完整的 RSS 2.0 文档。
//
// now 是显式参数而非 time.Now()，有两个原因：让测试可确定，
// 以及让「pubDate 退化路径」的行为在调用点就可见（见 itemPubDate）。
func Render(w io.Writer, meta Meta, items []Item, now time.Time) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}

	out := rss{
		Version: "2.0",
		Channel: channel{
			Title:       meta.Title,
			Link:        meta.Link,
			Description: meta.Description,
			Language:    meta.Language,
			LastBuild:   now.UTC().Format(time.RFC1123Z),
			Items:       make([]item, 0, len(items)),
		},
	}

	for _, it := range items {
		// 交付物由**来源**决定（上游→磁链，M-Team→本服务端出的种子文件），
		// 而 <link> 与 <enclosure url> 取同一个值：覆盖更多客户端的取链方式。
		delivery := it.EnclosureURL(meta.BaseURL)
		out.Channel.Items = append(out.Channel.Items, item{
			Title:     it.Title(),
			Link:      delivery,
			GUID:      guid{IsPermaLink: false, Value: it.GUID()},
			PubDate:   itemPubDate(it, now),
			Category:  it.Work.Number,
			Enclosure: enclosure{URL: delivery, Length: it.Magnet.SizeMiB * 1024 * 1024, Type: enclosureMIMEType},
		})
	}

	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	// Encode 不写结尾换行；补一个，让文件以换行结束。
	_, err := io.WriteString(w, "\n")
	return err
}

// itemPubDate 解析磁链的 created_at，失败则退化为 now。
//
// 注意退化路径会让 pubDate 随请求变化。这是刻意的取舍：本服务无状态，
// 记不住「第一次见到它是什么时候」，而 RSS 里省略 pubDate 虽然合法，
// 却会让部分阅读器把条目当成刚发布。改变 pubDate 不影响去重 ——
// 去重只看 guid（见 Item.GUID）。
//
// 解析逻辑集中在 catalog（选择/切换规则也要用它），与去重同源。
func itemPubDate(it Item, now time.Time) string {
	if t, ok := catalog.ParseCreatedAt(it.Magnet.CreatedAt); ok {
		return t.UTC().Format(time.RFC1123Z)
	}
	return now.UTC().Format(time.RFC1123Z)
}
