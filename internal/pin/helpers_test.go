package pin

import "github.com/2017fighting/javdb_rss/internal/catalog"

// 本文件是 pin 包测试共用的构造器，避免 policy_test.go 与 source_test.go
// 各维护一份同形 helper。

// magnet 构造一条当天（固定日期）的磁链候选。
func magnet(hash string, cnsub bool) catalog.Magnet {
	return magnetAt(hash, cnsub, "09/01/2026")
}

// magnetAt 构造一条带指定 created_at 的磁链候选。
func magnetAt(hash string, cnsub bool, createdAt string) catalog.Magnet {
	return catalog.Magnet{Infohash: hash, Name: "N-" + hash, SizeMB: 100, CNSub: cnsub, CreatedAt: createdAt}
}

// record 构造一条 pin 记录。
func record(hash string, cnsub bool, createdAt string) Record {
	return Record{Infohash: hash, CNSub: cnsub, CreatedAt: createdAt}
}
