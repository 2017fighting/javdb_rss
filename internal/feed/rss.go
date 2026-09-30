package feed

import (
	"encoding/xml"
	"io"
	"net/url"
	"strings"
	"time"
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
	// TODO(ticket-06): App API 的 size 单位未经核实（假定为 兆字节）。
	Length int    `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// magnetType 是磁力链接的 enclosure MIME 类型。
// 用 application/x-bittorrent 而不是规范的 application/x-bittorrent.magnet，
// 因为前者是 qBittorrent 与主流客户端实际识别的那个。
const magnetType = "application/x-bittorrent"

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
		magnet := it.MagnetURI()
		out.Channel.Items = append(out.Channel.Items, item{
			Title:     it.Title(),
			Link:      magnet,
			GUID:      guid{IsPermaLink: false, Value: it.GUID()},
			PubDate:   itemPubDate(it, now),
			Category:  it.Work.Number,
			Enclosure: enclosure{URL: magnet, Length: it.Magnet.SizeMB * 1024 * 1024, Type: magnetType},
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
func itemPubDate(it Item, now time.Time) string {
	if t, ok := parseCreatedAt(it.Magnet.CreatedAt); ok {
		return t.UTC().Format(time.RFC1123Z)
	}
	return now.UTC().Format(time.RFC1123Z)
}

// parseCreatedAt 解析 App API 的磁链创建时间。
//
// 已知格式是 "09/27/2026"（月/日/年）。再接受一个 ISO 形式作为容错，
// 因为线格式未在文档中固定（ticket 06 待核实）。
func parseCreatedAt(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"01/02/2006", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
