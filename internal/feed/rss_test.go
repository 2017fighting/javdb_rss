package feed

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

var fixedNow = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func render(t *testing.T, items []Item) string {
	t.Helper()
	var buf bytes.Buffer
	meta := Meta{Title: "T", Link: "http://x/rss", Description: "D", Language: "zh-CN"}
	if err := Render(&buf, meta, items, fixedNow); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

// TestGUIDIsPureInfohash 钉住 ticket 08 最要紧的一条：
// guid 只由 infohash 决定，不受番号、标题、槽位影响。
// 这是「跨 feed 去重」与「无状态重启不变」两件事的共同根据。
func TestGUIDIsPureInfohash(t *testing.T) {
	m := catalog.Magnet{Infohash: "deadbeef", Name: "KV-328"}

	same := Item{Work: catalog.Work{Number: "KV-328", Title: "标题 A"}, Magnet: m}
	if same.GUID() != "deadbeef" {
		t.Fatalf("GUID = %q, want deadbeef", same.GUID())
	}

	// 同一部作品换一个番号/标题，guid 必须不变 —— 否则跨订阅去重就失效。
	other := Item{Work: catalog.Work{Number: "OTHER-999", Title: "完全不同的标题"}, Magnet: m}
	if other.GUID() != same.GUID() {
		t.Errorf("guid 随番号/标题变化了：%q vs %q", other.GUID(), same.GUID())
	}

	// 洗版（新 infohash）必须产生新 guid，否则不会重新下载。
	remux := Item{Work: same.Work, Magnet: catalog.Magnet{Infohash: "cafebabe"}}
	if remux.GUID() == same.GUID() {
		t.Error("洗版后 guid 没有变化，客户端不会重新下载")
	}
}

func TestTitle(t *testing.T) {
	tests := []struct {
		name string
		it   Item
		want string
	}{
		{
			"字幕版加中文字幕前缀",
			Item{Work: catalog.Work{Number: "KV-328", Title: "おしゃぶり予備校110"},
				Magnet: catalog.Magnet{Infohash: "x", CNSub: true}},
			"[KV-328] 中文字幕 · おしゃぶり予備校110",
		},
		{
			"普通版无前缀",
			Item{Work: catalog.Work{Number: "KV-328", Title: "おしゃぶり予備校110"},
				Magnet: catalog.Magnet{Infohash: "x"}},
			"[KV-328] おしゃぶり予備校110",
		},
		{
			// 实测 App API 返回的磁链 name 常常就是番号本身，
			// 所以作品标题才是主基名（对 ticket 08 措辞的修订）。
			"无作品标题时退化为磁链显示名",
			Item{Work: catalog.Work{Number: "KV-328"},
				Magnet: catalog.Magnet{Infohash: "x", Name: "KV-328 4K"}},
			"[KV-328] KV-328 4K",
		},
		{
			"两者皆空时退化为 infohash，不产出空标题",
			Item{Work: catalog.Work{Number: "KV-328"}, Magnet: catalog.Magnet{Infohash: "abc123"}},
			"[KV-328] abc123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.it.Title(); got != tt.want {
				t.Errorf("Title() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildSkipsWorksWithoutMagnets 确认没有磁链候选的作品被跳过 ——
// 一个没有 enclosure 的 item 对 qBittorrent 是没有意义的。
func TestBuildSkipsWorksWithoutMagnets(t *testing.T) {
	works := []catalog.Work{
		{Number: "A-1", Magnets: nil},
		{Number: "A-2", Magnets: []catalog.Magnet{{Infohash: "h2"}}},
		{Number: "A-3", Magnets: []catalog.Magnet{}},
	}
	items := Build(works)
	if len(items) != 1 {
		t.Fatalf("得到 %d 条 item，want 1", len(items))
	}
	if items[0].Work.Number != "A-2" {
		t.Errorf("保留的是 %q，want A-2", items[0].Work.Number)
	}
}

// TestBuildAppliesSlotRule 确认 Build 真的走了槽位规则。
func TestBuildAppliesSlotRule(t *testing.T) {
	works := []catalog.Work{{
		Number: "A-1",
		Title:  "T",
		Magnets: []catalog.Magnet{
			{Infohash: "plain"},
			{Infohash: "zh", CNSub: true},
		},
	}}
	items := Build(works)
	if len(items) != 1 {
		t.Fatalf("得到 %d 条 item，want 1（中文字幕优先只发一条）", len(items))
	}
	if items[0].Magnet.Infohash != "zh" {
		t.Errorf("选中的是 %q，want zh", items[0].Magnet.Infohash)
	}
}

// TestRenderIsValidXML 把渲染结果反解回来，确认结构合法、字段落位正确。
func TestRenderIsValidXML(t *testing.T) {
	items := []Item{{
		Work: catalog.Work{Number: "KV-328", Title: "标题 & 需要转义"},
		Magnet: catalog.Magnet{
			Infohash: "0e8f4789bdcab713effc3a07d1309a776c867b3e",
			SizeMB:   3110, CNSub: true, CreatedAt: "09/27/2026",
		},
	}}
	out := render(t, items)

	// 必须能被 XML 解析器读回（含 & 转义是否做对）。
	var doc rss
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("渲染结果不是合法 XML: %v\n%s", err, out)
	}
	if doc.Version != "2.0" {
		t.Errorf("rss version = %q, want 2.0", doc.Version)
	}
	if len(doc.Channel.Items) != 1 {
		t.Fatalf("得到 %d 条 item，want 1", len(doc.Channel.Items))
	}
	it := doc.Channel.Items[0]

	if want := "[KV-328] 中文字幕 · 标题 & 需要转义"; it.Title != want {
		t.Errorf("title = %q, want %q", it.Title, want)
	}
	if it.GUID.Value != "0e8f4789bdcab713effc3a07d1309a776c867b3e" {
		t.Errorf("guid = %q", it.GUID.Value)
	}
	if it.GUID.IsPermaLink {
		t.Error("guid 的 isPermaLink 必须是 false —— infohash 不是可访问的 URL")
	}
	if it.Category != "KV-328" {
		t.Errorf("category = %q, want KV-328", it.Category)
	}
	if !strings.HasPrefix(it.Enclosure.URL, "magnet:?xt=urn:btih:0e8f4789") {
		t.Errorf("enclosure url = %q", it.Enclosure.URL)
	}
	if it.Link != it.Enclosure.URL {
		t.Error("link 与 enclosure url 应当一致 —— 覆盖更多客户端的取链方式")
	}
	if it.Enclosure.Length != 3110*1024*1024 {
		t.Errorf("enclosure length = %d", it.Enclosure.Length)
	}
	// created_at 09/27/2026 应被解析并进入 pubDate。
	if !strings.Contains(it.PubDate, "27 Sep 2026") {
		t.Errorf("pubDate = %q, want 含 27 Sep 2026", it.PubDate)
	}
	// & 在原样输出里必须是实体，否则整体就不是合法 XML。
	if strings.Contains(out, "标题 & 需要转义") {
		t.Error("& 没有被转义")
	}
}

// TestRenderPubDateFallsBackToNow 确认无法解析的 created_at 不会让渲染失败。
func TestRenderPubDateFallsBackToNow(t *testing.T) {
	items := []Item{{
		Work:   catalog.Work{Number: "A-1", Title: "T"},
		Magnet: catalog.Magnet{Infohash: "h", CreatedAt: "格式变了"},
	}}
	out := render(t, items)
	var doc rss
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("不是合法 XML: %v", err)
	}
	if !strings.Contains(doc.Channel.Items[0].PubDate, "28 Sep 2026") {
		t.Errorf("pubDate 未退化为 now: %q", doc.Channel.Items[0].PubDate)
	}
}

// TestMagnetDNEncodesSpaceAsPercent20 钉住一个真发生过的 bug：
//
// url.QueryEscape 按 form-urlencoded 把空格编成 '+'。这在 query string 里确实是合法的
// 空格表示，但 dn 的值会变成客户端界面上的显示名，水画的加号会直接给用户看到。
//
// 这个断言看起来吹毛求疵，但它捕获的是「看得到但不报错」的一类回归 ——
// 除了用户自己看到奇怪的文件名，没有任何告警会提醒你。
func TestMagnetDNEncodesSpaceAsPercent20(t *testing.T) {
	it := Item{
		Work:   catalog.Work{Number: "A-1", Title: "带 空格 的标题"},
		Magnet: catalog.Magnet{Infohash: "h"},
	}
	uri := it.MagnetURI()
	if !strings.Contains(uri, "dn=%E5%B8%A6%20%E7%A9%BA%E6%A0%BC") {
		t.Errorf("dn 的空格没有编成 %%20: %s", uri)
	}
	if strings.Contains(uri, "+") {
		t.Errorf("dn 里出现了 '+'，客户端会把它显示成加号: %s", uri)
	}
}

// TestRenderEmptyFeedIsValid feed 为空是正常状态（还没有新作品），
// 必须仍然产出合法文档 —— 否则客户端会报错而不是显示「暂无内容」。
func TestRenderEmptyFeedIsValid(t *testing.T) {
	out := render(t, nil)
	var doc rss
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("空 feed 不是合法 XML: %v\n%s", err, out)
	}
	if len(doc.Channel.Items) != 0 {
		t.Errorf("空 feed 里有 %d 条 item", len(doc.Channel.Items))
	}
}
