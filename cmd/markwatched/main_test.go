package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 这些测试只覆盖记录文件 —— 也就是这个工具**唯一真正有逻辑**的那部分。
//
// 为什么值得测：这个工具会对**真实账号**做批量写，而记录文件是那些写操作
// 唯一的撤销凭证。凭证丢了的后果不是「功能少一点」，是「两百多部作品只能回
// App 里一部部点回来」。写操作本身（HTTP POST）没办法离线测，也不该在测试里
// 碰真实上游；但凭证的合并、保权、以及**拒绝覆盖损坏文件**这三条能。

func readRecord(t *testing.T, path string) []markedWork {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读记录文件: %v", err)
	}
	var got []markedWork
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("记录文件不是合法 JSON: %v\n%s", err, raw)
	}
	return got
}

// TestSaveRecordMergesBatches 钉住「试水后再跑全量」这个真实顺序：
//
// -limit 1 之后接着跑全量是很自然的用法，覆盖而不是合并会把试水那部的凭证冲掉，
// 于是撤销时会漏掉一部 —— 而漏掉是**静默**的（用户以为 UNDO 撤销了全部）。
func TestSaveRecordMergesBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")

	if err := saveRecord(path, []markedWork{{ID: "pilot", Number: "AAA-001"}}); err != nil {
		t.Fatal(err)
	}
	if err := saveRecord(path, []markedWork{
		{ID: "b", Number: "BBB-002"},
		{ID: "c", Number: "CCC-003"},
	}); err != nil {
		t.Fatal(err)
	}

	got := readRecord(t, path)
	if len(got) != 3 {
		t.Fatalf("得到 %d 条，试水那一批应当被合并保留：%+v", len(got), got)
	}
	if got[0].ID != "b" || got[1].ID != "c" || got[2].ID != "pilot" {
		t.Errorf("顺序应当按 id 稳定排序，得到 %+v", got)
	}
}

// TestSaveRecordIsIdempotent 确认同一部重复记录只会留一条 —— 重跑本工具是
// 正常的恢复手段，不该让记录文件里的条目翻倍。
func TestSaveRecordIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	batch := []markedWork{{ID: "a", Number: "AAA-001"}}

	if err := saveRecord(path, batch); err != nil {
		t.Fatal(err)
	}
	if err := saveRecord(path, batch); err != nil {
		t.Fatal(err)
	}
	if got := readRecord(t, path); len(got) != 1 {
		t.Errorf("得到 %d 条，重复写同一部应当只有一条", len(got))
	}
}

// TestSaveRecordRefusesToClobberCorruptFile 是最要紧的一条。
//
// 读不动的记录文件**必须**拒绝写，而不是当成空表覆盖 —— 后者会把之前批次的
// 撤销凭证静默销毁，而用户只会在想撤销的那一刻才发现。
// 这与 pin 表对损坏文件的处理是同一条立场（宁可拒绝，也不要静默丢状态）。
func TestSaveRecordRefusesToClobberCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	const corrupt = "{这不是 JSON"
	if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := saveRecord(path, []markedWork{{ID: "a", Number: "AAA-001"}}); err == nil {
		t.Fatal("应当报错，而不是覆盖一份读不动的凭证")
	}

	// 原文件必须原样保留 —— 「报错」与「已经写坏了」是两回事。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != corrupt {
		t.Errorf("损坏的文件被改动了：%q", raw)
	}
}

// TestSaveRecordIsPrivate 钉住权限：这份文件记的是你的观影列表。
func TestSaveRecordIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	if err := saveRecord(path, []markedWork{{ID: "a", Number: "AAA-001"}}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("权限 = %o, want 600", perm)
	}
	// 临时文件不该留下 —— 半截文件比没有文件更坏。
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("临时文件没有清掉: %v", err)
	}
}
