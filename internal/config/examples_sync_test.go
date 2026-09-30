package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 本服务三份配置示例是同一套配置结构的三个手抄本：
//
//	config.example.yaml        裸机 / 本地
//	deploy/config.docker.yaml  容器
//	deploy/k8s.yaml            k8s ConfigMap（配置内嵌在 data.config.yaml）
//
// 它们表达同一套设置，只有少量取值不同（listen、base_url、token_file）。
// 键集合必须完全一致 —— 否则出现过的漂移（k8s 漏了 device_uuid）会重演，
// 而漂移的失败方式是**静默的**：配置照样解析成功，只是某一项没按预期生效。
//
// 检查分三层：
//
//  1. 三份文件两两键集合相同（TestExampleConfigsAreInSync）。
//  2. 键集合覆盖 Config 结构体声明的 schema（同一个测试）。这一层是关键：
//     只让三份文件互相看齐的话，三份一起漏掉同一个键时没人会发现；而把
//     结构体当单一来源，「往结构体加字段却忘了写进示例」就会失败。
//  3. 元测试：随便删掉一份里的一个键，检查必须失败（TestKeyCheckCatchesDrift /
//     TestKeyCheckCatchesKeyRemovedFromSource）—— 证明它不是空转。
//
// 为什么不是「从一份生成另外两份」：三份的取值与注释都不同（本地/容器/k8s
// 各有各的说明），生成会牺牲它们作为文档的价值。于是这张票选了「一改就报错」，
// 让三份文件一旦不同步就无法提交。

// exampleConfigSources 列出要互相校验的三份配置来源。
var exampleConfigSources = []struct {
	// name 是报错时用的名字，也是键集合的 map key。
	name string
	// file 是相对仓库根目录的路径。
	file string
	// embedded 为真时，file 是一份 k8s 清单，配置在内嵌的 ConfigMap 里。
	embedded bool
}{
	{"config.example.yaml", "config.example.yaml", false},
	{"deploy/config.docker.yaml", "deploy/config.docker.yaml", false},
	{"deploy/k8s.yaml (ConfigMap)", "deploy/k8s.yaml", true},
}

// loadExampleConfigKeys 读取三份真实文件，返回「来源名 -> 键路径集合」。
//
// 键路径用点号连接，例如 app_api.device_uuid。段本身的键（app_api）也算一个，
// 这样整段被删掉也能被发现。
func loadExampleConfigKeys(t *testing.T) map[string]map[string]bool {
	t.Helper()
	out := make(map[string]map[string]bool, len(exampleConfigSources))
	for _, src := range exampleConfigSources {
		raw, err := os.ReadFile(filepath.Join("..", "..", src.file))
		if err != nil {
			t.Fatalf("读取 %s: %v", src.file, err)
		}
		body := raw
		if src.embedded {
			body = k8sConfigMapConfig(t, raw)
		}
		out[src.name] = yamlKeyPaths(t, body, src.name)
	}
	return out
}

// k8sConfigMapConfig 从多文档的 k8s 清单里取出 javdb-rss-config 的
// data.config.yaml 内容。
func k8sConfigMapConfig(t *testing.T, manifest []byte) []byte {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(manifest))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Data map[string]string `yaml:"data"`
		}
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("解析 k8s 清单: %v", err)
		}
		if doc.Kind != "ConfigMap" || doc.Metadata.Name != "javdb-rss-config" {
			continue
		}
		body, ok := doc.Data["config.yaml"]
		if !ok {
			t.Fatalf("ConfigMap %s 没有 data.config.yaml", doc.Metadata.Name)
		}
		return []byte(body)
	}
	t.Fatal("deploy/k8s.yaml 里找不到 ConfigMap javdb-rss-config")
	return nil
}

// yamlKeyPaths 把一段 YAML 摊平成点号连接的键路径集合。
//
// 只关心键，不关心值 —— 三份文件的取值本就不同（listen 等）。
func yamlKeyPaths(t *testing.T, data []byte, source string) map[string]bool {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("解析 %s: %v", source, err)
	}
	keys := make(map[string]bool)
	var walk func(n *yaml.Node, prefix string)
	walk = func(n *yaml.Node, prefix string) {
		switch n.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, child := range n.Content {
				walk(child, prefix)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				path := n.Content[i].Value
				if prefix != "" {
					path = prefix + "." + path
				}
				keys[path] = true
				walk(n.Content[i+1], path)
			}
		}
	}
	walk(&root, "")
	return keys
}

// schemaKeyPaths 用反射把 Config 结构体摊平成同一套点号键路径。
//
// 结构体是配置结构的**单一来源**：yaml tag 就是键名。因此往结构体加字段
// （或在 tag 里改名）会让三份示例立刻失败，而不是等用户发现某一项没生效。
//
// 下钻包括**切片/数组的元素结构体**：`feeds.actresses` 是 `[]ActressSub`，
// YAML 里直接写元素字段（`- id: ...`），所以键是 feeds.actresses.id 而不是
// 某个带下标的路径。漏了下钻，feeds 段一旦取消注释就会被误报成未知键。
func schemaKeyPaths(t reflect.Type) []string {
	t = indirectType(t)
	if t.Kind() != reflect.Struct {
		return nil
	}
	var paths []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		paths = append(paths, name)
		for _, sub := range schemaKeyPaths(f.Type) {
			paths = append(paths, name+"."+sub)
		}
	}
	return paths
}

// indirectType 一路剥掉指针与切片/数组，取到最内层的类型。
// 用于让 schema 下钻到 []ActressSub 这样的元素结构体。
func indirectType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	return t
}

// checkConfigKeySets 校验键集合。返回 nil 表示一致。
//
// sets 的 key 是来源名（用于报错），value 是该来源的键路径集合。
// 单独抽成函数，是为了让「删一个键必须失败」的元测试能直接调用同一段逻辑。
func checkConfigKeySets(sets map[string]map[string]bool) error {
	var problems []string

	names := make([]string, 0, len(sets))
	for name := range sets {
		names = append(names, name)
	}
	sort.Strings(names)

	// 第 1 层：三份文件互相对齐。
	if len(names) >= 2 {
		base := names[0]
		for _, other := range names[1:] {
			onlyBase, onlyOther := keyDiff(sets[base], sets[other])
			for _, k := range onlyBase {
				problems = append(problems, fmt.Sprintf("%s 有而 %s 没有: %s", base, other, k))
			}
			for _, k := range onlyOther {
				problems = append(problems, fmt.Sprintf("%s 有而 %s 没有: %s", other, base, k))
			}
		}
	}

	// 第 2 层：覆盖 Config 结构体声明的 schema。
	schema := map[string]bool{}
	for _, p := range schemaKeyPaths(reflect.TypeOf(Config{})) {
		schema[p] = true
	}
	// feeds 白名单段在示例里整段注释掉，属于可选段 —— 允许整体缺席，
	// 但不允许只写一半。
	feedsKeys := map[string]bool{}
	for k := range schema {
		if k == "feeds" || strings.HasPrefix(k, "feeds.") {
			feedsKeys[k] = true
		}
	}

	for _, name := range names {
		got := sets[name]

		// schema 之外的键：与 Load 的 KnownFields(true) 是两套独立实现，
		// 这里再挡一道，防止解析器的行为哪天变了却没人发现。
		for _, k := range sortedKeys(got) {
			if !schema[k] {
				problems = append(problems, fmt.Sprintf("%s 有 schema 里没有的键: %s", name, k))
			}
		}

		// feeds 要么整段都在，要么整段都不在。
		feedsCount := 0
		for k := range got {
			if feedsKeys[k] {
				feedsCount++
			}
		}
		switch {
		case feedsCount == 0:
			// 整段注释掉，合法。
		case feedsCount != len(feedsKeys):
			problems = append(problems, fmt.Sprintf("%s 的 feeds 段不完整（%d/%d 个键）",
				name, feedsCount, len(feedsKeys)))
		}

		// 其余 schema 键一个都不能少。
		for _, k := range sortedKeys(schema) {
			if feedsKeys[k] || got[k] {
				continue
			}
			problems = append(problems, fmt.Sprintf("%s 缺少 schema 里的键: %s", name, k))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("三份配置示例不一致:\n  %s", strings.Join(problems, "\n  "))
}

// keyDiff 返回只在 a 里、只在 b 里的键（都排好序）。
func keyDiff(a, b map[string]bool) (onlyA, onlyB []string) {
	for k := range a {
		if !b[k] {
			onlyA = append(onlyA, k)
		}
	}
	for k := range b {
		if !a[k] {
			onlyB = append(onlyB, k)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return onlyA, onlyB
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// 测试
// ---------------------------------------------------------------------------

// TestExampleConfigsAreInSync 是本票的核心断言：
// 三份示例的键集合两两相同，且覆盖 Config 结构体的全部字段。
func TestExampleConfigsAreInSync(t *testing.T) {
	if err := checkConfigKeySets(loadExampleConfigKeys(t)); err != nil {
		t.Fatal(err)
	}
}

// TestK8sConfigHasDeviceUUID 单独钉住那次真实漂移。
//
// 它其实是 TestExampleConfigsAreInSync 的一个实例，但值得显式留下 ——
// 这就是上一轮 code-review 在 k8s ConfigMap 里漏掉的那个键。
func TestK8sConfigHasDeviceUUID(t *testing.T) {
	keys := loadExampleConfigKeys(t)
	if !keys["deploy/k8s.yaml (ConfigMap)"]["app_api.device_uuid"] {
		t.Fatal("k8s ConfigMap 缺少 app_api.device_uuid —— 正是曾经漂移掉的键")
	}
}

// TestKeyCheckCatchesDrift 证明检查不是空转：
// 从任意一份里删掉任意一个键，checkConfigKeySets 都必须报错。
//
// 遍历全部键 × 全部来源，是为了证明没有任何键被特殊照顾 ——
// 本项目已经出现过多次「看起来在测、其实永远通过」的测试。
func TestKeyCheckCatchesDrift(t *testing.T) {
	base := loadExampleConfigKeys(t)

	// 前提：当前示例本来就是一致的，否则这个测试可能只是一个恒真的错误。
	if err := checkConfigKeySets(base); err != nil {
		t.Fatalf("前提不成立：当前示例就不一致: %v", err)
	}

	for name := range base {
		for key := range base[name] {
			drifted := cloneKeySets(base)
			delete(drifted[name], key)
			if err := checkConfigKeySets(drifted); err == nil {
				t.Errorf("从 %s 删掉 %q 后检查仍然通过 —— 检查是空转的", name, key)
			}
		}
	}
}

// TestKeyCheckCatchesKeyRemovedFromSource 走完整的「读文件 → 解析 → 比对」链路，
// 证明失败来自文件里真的少了一个键，而不是只在内存 map 上做文章。
func TestKeyCheckCatchesKeyRemovedFromSource(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	const key = "device_uuid:"
	lines := strings.Split(string(raw), "\n")
	kept := lines[:0:0]
	removed := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), key) {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		t.Fatalf("config.example.yaml 里找不到 %q，测试需要更新", key)
	}

	keys := loadExampleConfigKeys(t)
	keys["config.example.yaml"] = yamlKeyPaths(t, []byte(strings.Join(kept, "\n")), "config.example.yaml (删了 device_uuid)")
	if err := checkConfigKeySets(keys); err == nil {
		t.Fatal("从 config.example.yaml 文本里删掉 device_uuid 后检查仍然通过")
	}
}

// TestSchemaCoversSliceElements 钉住一个真实的反射缺口：
// feeds.actresses 是 []ActressSub，元素是结构体。schema 必须下钻到元素字段
// （feeds.actresses.id 等），否则把示例里的 feeds 段取消注释后，检查会把它
// 当成「schema 里没有的键」而误报。
func TestSchemaCoversSliceElements(t *testing.T) {
	schema := map[string]bool{}
	for _, p := range schemaKeyPaths(reflect.TypeOf(Config{})) {
		schema[p] = true
	}
	for _, want := range []string{"feeds.actresses.id", "feeds.actresses.params", "feeds.actresses.since"} {
		if !schema[want] {
			t.Errorf("schema 缺少 %s —— 切片元素没有被下钻", want)
		}
	}
}

// TestKeyCheckAcceptsFullFeeds 把三份都补上完整的 feeds 段（模拟以后取消注释），
// 检查必须通过。它覆盖「feeds 完整」这条路径，也证明切片下钻后不会把
// feeds.actresses.* 误判成未知键。
func TestKeyCheckAcceptsFullFeeds(t *testing.T) {
	sets := cloneKeySets(loadExampleConfigKeys(t))
	for name := range sets {
		for _, k := range feedsSchemaKeys(t) {
			sets[name][k] = true
		}
	}
	if err := checkConfigKeySets(sets); err != nil {
		t.Fatalf("feeds 段完整时不该失败: %v", err)
	}
}

// TestKeyCheckCatchesPartialFeeds 三份都只写一半 feeds，schema 检查必须报
// 「不完整」—— 半写的白名单是真实的错法（例如漏了 actresses）。
// 三份都改，是为了把它与「两两对齐」那一层分开测。
func TestKeyCheckCatchesPartialFeeds(t *testing.T) {
	sets := cloneKeySets(loadExampleConfigKeys(t))
	for name := range sets {
		sets[name]["feeds"] = true
		sets[name]["feeds.codes"] = true
	}
	err := checkConfigKeySets(sets)
	if err == nil {
		t.Fatal("feeds 段只写一半时应当失败")
	}
	if !strings.Contains(err.Error(), "feeds 段不完整") {
		t.Errorf("错误信息没指出 feeds 段不完整: %v", err)
	}
}

// TestCrossFileCheckCatchesFeedsDrift 单独证明「三份两两对齐」那一层不是空转。
//
// 把完整的 feeds 段只加进其中一份：三份都满足 schema（feeds 可选，且这一份是
// 完整的），所以 schema 那层不会报错 —— 只有两两对齐那层能发现它。
// 这正是 TestKeyCheckCatchesDrift 无法单独证明的部分：删任何必填键都会同时触发
// schema 那层，因此那个测试不足以证明对齐层本身有效。
func TestCrossFileCheckCatchesFeedsDrift(t *testing.T) {
	const drifted = "deploy/config.docker.yaml"
	sets := cloneKeySets(loadExampleConfigKeys(t))
	for _, k := range feedsSchemaKeys(t) {
		sets[drifted][k] = true
	}
	err := checkConfigKeySets(sets)
	if err == nil {
		t.Fatal("只给一份加 feeds 段时应当失败（两两对齐那层没起作用）")
	}
	if !strings.Contains(err.Error(), drifted) {
		t.Errorf("错误信息没指向漂移的那份: %v", err)
	}
}

// feedsSchemaKeys 返回 schema 里 feeds 段下的全部键路径。
func feedsSchemaKeys(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, p := range schemaKeyPaths(reflect.TypeOf(Config{})) {
		if p == "feeds" || strings.HasPrefix(p, "feeds.") {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Fatal("schema 里没有 feeds 段，测试前提不成立")
	}
	return out
}

// cloneKeySets 深拷贝「来源 -> 键集合」，便于元测试里安全地改动。
func cloneKeySets(sets map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(sets))
	for name, keys := range sets {
		c := make(map[string]bool, len(keys))
		for k := range keys {
			c[k] = true
		}
		out[name] = c
	}
	return out
}
