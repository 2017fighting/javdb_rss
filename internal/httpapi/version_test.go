package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/stub"
)

// versionResponse 是 /version 的机读形状，作测试里的解码目标。
//
// 三个白名单布尔刻意与页面的消费方式一致 —— 页面拿它决定要不要说
// 「没列出的订阅会 404」，所以测试也按这三个键断言。
type versionResponse struct {
	Version   string `json:"version"`
	Provider  string `json:"provider"`
	Whitelist struct {
		Actresses bool `json:"actresses"`
		Lists     bool `json:"lists"`
		Zones     bool `json:"zones"`
	} `json:"whitelist"`
}

func decodeVersion(t *testing.T, body []byte) versionResponse {
	t.Helper()
	var got versionResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析 /version 失败: %v（body=%s）", err, body)
	}
	return got
}

// TestVersionWithoutWhitelistReportsAllCategoriesOpen：没写 feeds 段时，
// 三类都必须是 false —— 页面据此保持安静（不制造一条假的「会 404」警告）。
func TestVersionWithoutWhitelistReportsAllCategoriesOpen(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	rec := do(t, h, "/version")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /version = %d, want 200", rec.Code)
	}
	got := decodeVersion(t, rec.Body.Bytes())
	if got.Provider != "stub" {
		t.Errorf("provider = %q, want stub", got.Provider)
	}
	if got.Whitelist.Actresses || got.Whitelist.Lists || got.Whitelist.Zones {
		t.Errorf("没写 feeds 段时三类白名单都应为 false（全放行），得到 %+v", got.Whitelist)
	}
}

// TestVersionWhitelistAgreesWithSubscriptionGate 把 /version 的说法与实际的
// 404 行为钉在一起，并顺带覆盖「feeds 段生效时三类都为 true」。
//
// ⚠️ 下面只给女优白名单，而清单与全站标签同样是 true —— 那不是 bug，
// 而是 feeds 段作为整段闸门的直接后果（一个只列了女优的 feeds 段，会让所有
// 清单/片库订阅也 404）。
//
// 页面照着 /version 告诉用户「没列出的订阅会 404」。如果那个布尔与路由层的
// 实际放行判断脱节，页面就会**说谎** —— 而一个说谎的状态提示比没有提示更坏。
func TestVersionWhitelistAgreesWithSubscriptionGate(t *testing.T) {
	h := newTestServer(t, `
provider: stub
feeds:
  actresses:
    - id: EvkJ
`, &stub.Source{})

	got := decodeVersion(t, do(t, h, "/version").Body.Bytes())

	// 三类各取一条**没列进白名单**的订阅：实际都得 404，
	// 而 /version 说这三类都被白名单限制着。
	for _, target := range []string{
		"/rss/actress/D2EdJ.xml",
		"/rss/list/k4EVE4.xml",
		"/rss/tags/0.xml",
	} {
		if code := do(t, h, target).Code; code != http.StatusNotFound {
			t.Errorf("白名单生效时 %s = %d，want 404（/version 说这一类受限，实际却没拒）", target, code)
		}
	}
	if !got.Whitelist.Actresses || !got.Whitelist.Lists || !got.Whitelist.Zones {
		t.Errorf("/version 应当报告三类都受限，得到 %+v", got.Whitelist)
	}

	// 列出来的那一条仍然可用：「白名单生效」与「放行这一条」是两件事，
	// 页面既要说「会 404」，也不能把白名单内的链接当成拿不到。
	if code := do(t, h, "/rss/actress/EvkJ.xml").Code; code != http.StatusOK {
		t.Errorf("白名单内的女优订阅 = %d，want 200", code)
	}
}
