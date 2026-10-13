// Package mteam 是第二个数据来源：M-Team（一个私有 PT 站）。
//
// 它**只在 `/rss/want.xml` 上生效** —— 「想看」清单里的作品，如果馒头上也有，
// 就交出馒头那一份（种子文件），否则照旧发上游那份磁链。
// 完整的取舍见 `.scratch/javdb-rss-mteam/spec.md` 与 `docs/adr/0004`。
//
// # 它和上游最不一样的一点
//
// **它不给磁链。** 搜索与详情都不返回 infohash（实测：两处的 `infoHash` 都是 `null`），
// 而它是私种 —— announce 里那份按次签发的凭据只存在于 `.torrent` 文件里。
// 因此本包必须：下载种子 → 自己算 infohash（那是 feed 条目的身份）→ 把种子字节存下来
// 再端给客户端。
package mteam

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
)

// errTruncated 表示字节在读满一个值之前就用完了。
var errTruncated = errors.New("bencode: 数据被截断")

// decoder 是一个**只用来定位字节区间**的最小 bencode 遍历器。
//
// 它刻意不构造 Go 值：本包要的是 `info` 字典的**原始字节切片** ——
// infohash 是对那份切片取 SHA-1，而不是对它解析后再编码的结果。
// 理由见 Infohash 的注释，证据是 torrent_test.go 里那份键序非规范的夹具。
type decoder struct{ b []byte }

// byteString 读位置 i 处的字节串，返回它的内容与结束位置。
func (d decoder) byteString(i int) ([]byte, int, error) {
	if i >= len(d.b) {
		return nil, 0, errTruncated
	}
	j := bytes.IndexByte(d.b[i:], ':')
	if j < 0 {
		return nil, 0, errTruncated
	}
	n := 0
	for _, c := range d.b[i : i+j] {
		if c < '0' || c > '9' {
			return nil, 0, errors.New("bencode: 字节串的长度不是数字")
		}
		n = n*10 + int(c-'0')
		if n > len(d.b) { // 早退，免得下面的 start+n 溢出
			return nil, 0, errTruncated
		}
	}
	start := i + j + 1
	if start+n > len(d.b) {
		return nil, 0, errTruncated
	}
	return d.b[start : start+n], start + n, nil
}

// skip 跳过位置 i 处的一个完整值，返回它的结束位置。
//
// 它只关心**结构**（哪一段字节属于这个值），不校验内容 ——
// 整数里写了什么、字典的键是什么，对定位都没有影响。
func (d decoder) skip(i int) (int, error) {
	if i >= len(d.b) {
		return 0, errTruncated
	}
	switch d.b[i] {
	case 'i':
		j := bytes.IndexByte(d.b[i:], 'e')
		if j < 0 {
			return 0, errTruncated
		}
		return i + j + 1, nil
	case 'l', 'd':
		isDict := d.b[i] == 'd'
		i++
		for {
			if i >= len(d.b) {
				return 0, errTruncated
			}
			if d.b[i] == 'e' {
				return i + 1, nil
			}
			var err error
			if i, err = d.skip(i); err != nil { // 键（或列表元素）
				return 0, err
			}
			if isDict {
				if i, err = d.skip(i); err != nil { // 字典还要一个值
					return 0, err
				}
			}
		}
	default:
		_, end, err := d.byteString(i)
		return end, err
	}
}

// dictValue 在位置 i（必须是一个字典）里找 key，返回它**值**的原始字节区间 [start, end)。
//
// 第三个返回值报告「找没找到」—— 与「出错了」分开，因为调用方对这两种情况的处置
// 完全不同（缺 info 是「这不是种子」，截断是「这份种子坏了」）。
func (d decoder) dictValue(i int, key string) (start, end int, ok bool, err error) {
	if i >= len(d.b) || d.b[i] != 'd' {
		return 0, 0, false, errors.New("bencode: 期望一个字典")
	}
	i++
	for {
		if i >= len(d.b) {
			return 0, 0, false, errTruncated
		}
		if d.b[i] == 'e' {
			return 0, 0, false, nil
		}
		k, next, err := d.byteString(i)
		if err != nil {
			return 0, 0, false, err
		}
		vs := next
		ve, err := d.skip(vs)
		if err != nil {
			return 0, 0, false, err
		}
		if string(k) == key {
			return vs, ve, true, nil
		}
		i = ve
	}
}

// Infohash 从一份 .torrent 的字节算出它的 v1 infohash，并确认这份种子**能用**。
//
// 两件事合在一个函数里，是因为它们服务于同一个判断：「这份种子能不能交给客户端」。
// 拆开的话，调用方可以只取 infohash 而忘掉校验，而那种漏掉的表现是
// **客户端红种、我们这边无声**。
//
// # 为什么必须切原始切片
//
// infohash 是 `info` 字典**原始字节**的 SHA-1。把它解析出来再重新编码一遍看起来等价，
// 但那要求你的编码器逐字节复现原始的所有选择（键序、整数写法、字节串边界）——
// 而现实里的种子**不保证键有序**（bencode 规范要求，但不是所有工具都遵守）。
// 重编码会得到一个**语法合法但错误**的 infohash，而它会直接变成 feed 的 `guid`：
// 客户端会把它当成另一个种子，没有任何告警。
// 测试里有一份键序非规范的夹具专门钉这件事 —— 它是两种实现唯一的区分点。
//
// # 为什么 v2 要拒绝
//
// 本服务只认 v1。v2 的 infohash 是另一套算法，拿 v1 的算法去算 v2 的 info
// 会得到一个「看起来像」的十六进制串。静默给一个错的，比报错糟得多。
func Infohash(data []byte) (string, error) {
	if len(data) == 0 || data[0] != 'd' {
		return "", errors.New("这不是一份种子：bencode 的顶层必须是字典")
	}
	d := decoder{b: data}

	infoStart, infoEnd, ok, err := d.dictValue(0, "info")
	if err != nil {
		return "", fmt.Errorf("解析种子: %w", err)
	}
	if !ok {
		return "", errors.New("这份种子里没有 info 字典")
	}
	if infoStart >= len(data) || data[infoStart] != 'd' {
		return "", errors.New("这份种子的 info 不是一个字典")
	}

	annStart, annEnd, hasAnn, err := d.dictValue(0, "announce")
	if err != nil {
		return "", fmt.Errorf("解析种子: %w", err)
	}
	if !hasAnn {
		return "", errors.New("这份种子里没有 announce")
	}
	if !bytes.Contains(data[annStart:annEnd], []byte("credential=")) {
		return "", errors.New("announce 里没有凭据（credential=）—— 这份种子连不上 tracker，发出去就是红种")
	}

	if err := checkV1(d, infoStart); err != nil {
		return "", err
	}

	sum := sha1.Sum(data[infoStart:infoEnd])
	return hex.EncodeToString(sum[:]), nil
}

// checkV1 确认 info 字典是 v1 形态，并拒绝 v2-only。
//
// 判据：v1 的单文件种子有 `length`，多文件有 `files`。
// **hybrid（v1+v2）通过** —— 它有 `length`/`files`，因此有 v1 infohash，能用。
func checkV1(d decoder, infoStart int) error {
	for _, key := range []string{"length", "files"} {
		if _, _, ok, err := d.dictValue(infoStart, key); err != nil {
			return fmt.Errorf("解析种子: %w", err)
		} else if ok {
			return nil
		}
	}
	if _, _, ok, err := d.dictValue(infoStart, "meta version"); err == nil && ok {
		return errors.New("这是 v2-only 的种子（有 meta version，但没有 v1 的 length/files）—— 本服务只认 v1")
	}
	return errors.New("这份种子的 info 里既没有 length 也没有 files，认不出是 v1 形态")
}
