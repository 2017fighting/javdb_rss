package catalog

import "testing"

// TestSelect 钉住 ticket 08 定下的槽位规则：「字幕优先，每部作品恒发 1 条」。
func TestSelect(t *testing.T) {
	plain1 := Magnet{Infohash: "aaa", CNSub: false}
	plain2 := Magnet{Infohash: "bbb", CNSub: false}
	sub1 := Magnet{Infohash: "ccc", CNSub: true}
	sub2 := Magnet{Infohash: "ddd", CNSub: true}

	tests := []struct {
		name   string
		in     []Magnet
		want   string
		wantOK bool
	}{
		{"无候选", nil, "", false},
		{"空切片", []Magnet{}, "", false},
		{"只有无字幕候选取第一条", []Magnet{plain1, plain2}, "aaa", true},
		{"有无字幕就取无字幕", []Magnet{plain1, plain2}, "aaa", true},
		{"有字幕时跳过前面的无字幕", []Magnet{plain1, sub1}, "ccc", true},
		{"多个字幕取第一个字幕", []Magnet{plain1, sub1, sub2}, "ccc", true},
		// 这条是本规则刻意消掉的边界情况：magnets[0] 本身就是字幕版时，
		// 「普通槽位」与「字幕槽位」指向同一条，因此只选出一条，
		// 不会产生两条 guid 指向同一个 infohash（那会让客户端重复下载）。
		{"第一条本身就是字幕时只选这一条", []Magnet{sub1, sub2}, "ccc", true},
		{"只有一条无字幕", []Magnet{plain1}, "aaa", true},
		{"只有一条字幕", []Magnet{sub1}, "ccc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Select(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("Select ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.Infohash != tt.want {
				t.Errorf("Select = %q, want %q", got.Infohash, tt.want)
			}
		})
	}
}

// TestSelectDoesNotReorder 确认 Select 不偷偷重排。
// 排序规则属于上游（用户已选定「信任 App 顺序」），本服务不得插手。
func TestSelectDoesNotReorder(t *testing.T) {
	small := Magnet{Infohash: "small", SizeMB: 1}
	big := Magnet{Infohash: "big", SizeMB: 99999}
	if got, _ := Select([]Magnet{small, big}); got.Infohash != "small" {
		t.Errorf("Select 不该按体积挑：得到 %q", got.Infohash)
	}
	// 字幕版本在最后，仍应被选中 —— 说明只做「找第一条满足条件的」，不做排序。
	if got, _ := Select([]Magnet{big, small, {Infohash: "zh", CNSub: true}}); got.Infohash != "zh" {
		t.Errorf("应选第一条字幕候选：得到 %q", got.Infohash)
	}
}
