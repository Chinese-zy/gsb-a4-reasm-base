package reasm_test

import (
	"testing"

	"reasm"
)

func TestContiguousAndSlightReorder(t *testing.T) {
	cases := []struct {
		name string
		in   []struct {
			seq  uint32
			data string
		}
		want string
	}{
		{
			name: "顺序",
			in: []struct {
				seq  uint32
				data string
			}{{0, "ab"}, {2, "cd"}},
			want: "abcd",
		},
		{
			name: "轻微乱序",
			in: []struct {
				seq  uint32
				data string
			}{{2, "cd"}, {0, "ab"}},
			want: "abcd",
		},
		{
			name: "三段颠倒",
			in: []struct {
				seq  uint32
				data string
			}{{4, "ef"}, {0, "ab"}, {2, "cd"}},
			want: "abcdef",
		},
		{
			name: "不从零开始",
			in: []struct {
				seq  uint32
				data string
			}{{100, "foo"}, {103, "bar"}},
			want: "foobar",
		},
		{
			name: "空片段忽略",
			in: []struct {
				seq  uint32
				data string
			}{{0, ""}, {0, "ok"}},
			want: "ok",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := reasm.New(64)
			for _, p := range tc.in {
				a.Ingest(p.seq, []byte(p.data))
			}
			if got := string(a.Emit()); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestEmitBetweenContiguousChunks(t *testing.T) {
	a := reasm.New(64)
	a.Ingest(0, []byte("ab"))
	if got := string(a.Emit()); got != "ab" {
		t.Fatalf("first %q", got)
	}
	a.Ingest(2, []byte("cd"))
	if got := string(a.Emit()); got != "cd" {
		t.Fatalf("second %q", got)
	}
	if got := a.Emit(); len(got) != 0 {
		t.Fatalf("third %q", got)
	}
}

type part struct {
	seq  uint32
	data string
}

// TestWrapAround 序号绕回零时仍按回绕比较拼接。
func TestWrapAround(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		in    []part
		want  string
	}{
		{
			name: "绕回零顺序到达",
			in:   []part{{0xFFFFFFFE, "ab"}, {0, "cd"}, {2, "ef"}},
			want: "abcdef",
		},
		{
			name: "绕回零乱序到达",
			in:   []part{{0, "cd"}, {0xFFFFFFFE, "ab"}},
			want: "abcd",
		},
		{
			name: "回绕点附近三段颠倒",
			in:   []part{{2, "ef"}, {0xFFFFFFFE, "ab"}, {0, "cd"}},
			want: "abcdef",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := reasm.New(64)
			for _, p := range tc.in {
				a.Ingest(p.seq, []byte(p.data))
			}
			if got := string(a.Emit()); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestOverlapFirstWins 重叠处认先确认下来的字节。
func TestOverlapFirstWins(t *testing.T) {
	cases := []struct {
		name string
		in   []part
		want string
	}{
		{
			name: "右侧重叠",
			in:   []part{{0, "abcd"}, {2, "cdef"}},
			want: "abcdef",
		},
		{
			name: "左侧重叠",
			in:   []part{{4, "efgh"}, {2, "cdef"}},
			want: "cdefgh",
		},
		{
			name: "骑在已有数据左右两边",
			in:   []part{{4, "EF"}, {2, "cdEFgh"}},
			want: "cdEFgh",
		},
		{
			name: "后到的被先到的整段包住",
			in:   []part{{0, "abcdef"}, {2, "XX"}},
			want: "abcdef",
		},
		{
			name: "跨回绕重叠",
			in:   []part{{0xFFFFFFFE, "abCD"}, {0, "CDef"}},
			want: "abCDef",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := reasm.New(64)
			for _, p := range tc.in {
				a.Ingest(p.seq, []byte(p.data))
			}
			if got := string(a.Emit()); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestDuplicateDropped 同一段重发直接丢掉，不盖掉已确认的字节。
func TestDuplicateDropped(t *testing.T) {
	cases := []struct {
		name string
		in   []part
		want string
	}{
		{
			name: "完全重复",
			in:   []part{{0, "ab"}, {0, "ab"}, {2, "cd"}},
			want: "abcd",
		},
		{
			name: "同位置不同内容不重盖",
			in:   []part{{0, "ab"}, {0, "XY"}, {2, "cd"}},
			want: "abcd",
		},
		{
			name: "交付后的重发",
			in:   []part{{0, "ab"}, {2, "cd"}},
			want: "abcd",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := reasm.New(64)
			for _, p := range tc.in {
				a.Ingest(p.seq, []byte(p.data))
			}
			if tc.name == "交付后的重发" {
				if got := string(a.Emit()); got != "abcd" {
					t.Fatalf("first emit %q", got)
				}
				a.Ingest(0, []byte("ab"))
				if got := a.Emit(); len(got) != 0 {
					t.Fatalf("retransmit leaked %q", got)
				}
				return
			}
			if got := string(a.Emit()); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestGapHoldsTail 中间缺洞时只吐连续前缀，洞补上后再吐后面的。
func TestGapHoldsTail(t *testing.T) {
	a := reasm.New(64)
	a.Ingest(0, []byte("ab"))
	a.Ingest(4, []byte("ef"))
	if got := string(a.Emit()); got != "ab" {
		t.Fatalf("prefix %q", got)
	}
	if got := a.Emit(); len(got) != 0 {
		t.Fatalf("gap not held, got %q", got)
	}
	a.Ingest(2, []byte("cd"))
	if got := string(a.Emit()); got != "cdef" {
		t.Fatalf("after fill %q", got)
	}
}

// TestLimitEvictsOldest 缓存顶满上限时扔最老的片段，内存不超限。
func TestLimitEvictsOldest(t *testing.T) {
	a := reasm.New(4)
	a.Ingest(0, []byte("ab"))
	a.Ingest(2, []byte("cd"))
	a.Ingest(4, []byte("ef")) // 超限，最老的 "ab" 被扔
	if got := string(a.Emit()); got != "cdef" {
		t.Fatalf("got %q want %q", got, "cdef")
	}
	if got := a.Emit(); len(got) != 0 {
		t.Fatalf("drained %q", got)
	}
}
