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

func feed(a *reasm.Assembler, parts []part) {
	for _, p := range parts {
		a.Ingest(p.seq, []byte(p.data))
	}
}

func TestWraparound(t *testing.T) {
	cases := []struct {
		name string
		in   []part
		want string
	}{
		{
			name: "绕回零",
			in:   []part{{0xFFFFFFFE, "ab"}, {0, "cd"}, {2, "ef"}},
			want: "abcdef",
		},
		{
			name: "绕回乱序",
			in:   []part{{2, "ef"}, {0, "cd"}, {0xFFFFFFFE, "ab"}},
			want: "abcdef",
		},
		{
			name: "绕回点重发丢弃",
			in:   []part{{0xFFFFFFFE, "ab"}, {0xFFFFFFFE, "XX"}, {0, "cd"}},
			want: "abcd",
		},
		{
			name: "绕回后骑跨已确认区间",
			in:   []part{{0xFFFFFFFE, "ab"}, {0, "cd"}, {0xFFFFFFFF, "bcde"}},
			want: "abcde",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := reasm.New(64)
			feed(a, tc.in)
			if got := string(a.Emit()); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestOverlapFirstConfirmedWins(t *testing.T) {
	cases := []struct {
		name string
		in   []part
		want string
	}{
		{
			name: "右侧重叠认先确认",
			in:   []part{{0, "abcd"}, {2, "XXef"}},
			want: "abcdef",
		},
		{
			name: "左侧骑跨认先确认",
			in:   []part{{4, "efgh"}, {0, "ab"}, {2, "cdXX"}},
			want: "abcdefgh",
		},
		{
			name: "左右两边骑跨已有数据",
			in:   []part{{2, "cd"}, {0, "aXcXe"}},
			want: "aXcde",
		},
		{
			name: "新桥接两个旧片段",
			in:   []part{{0, "ab"}, {4, "ef"}, {1, "XcdX"}},
			want: "abcdef",
		},
		{
			name: "完全被旧数据覆盖",
			in:   []part{{0, "abcdef"}, {2, "XX"}},
			want: "abcdef",
		},
		{
			name: "完全重复丢弃",
			in:   []part{{0, "ab"}, {0, "ab"}},
			want: "ab",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := reasm.New(64)
			feed(a, tc.in)
			if got := string(a.Emit()); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if got := a.Emit(); len(got) != 0 {
				t.Fatalf("second emit %q, want empty", got)
			}
		})
	}
}

func TestGapEmitsOnlyContiguousPrefix(t *testing.T) {
	cases := []struct {
		name  string
		steps [][]part
		want  []string
	}{
		{
			name:  "缺洞只吐前缀",
			steps: [][]part{{{0, "ab"}, {4, "ef"}}},
			want:  []string{"ab"},
		},
		{
			name:  "洞补上后吐后续",
			steps: [][]part{{{0, "ab"}, {4, "ef"}}, {{2, "cd"}}},
			want:  []string{"ab", "cdef"},
		},
		{
			name:  "吐出后中间缺洞",
			steps: [][]part{{{0, "ab"}}, {{4, "ef"}}, {{2, "cd"}}},
			want:  []string{"ab", "", "cdef"},
		},
		{
			name:  "两个洞分段补",
			steps: [][]part{{{0, "ab"}, {4, "ef"}, {8, "ij"}}, {{2, "cd"}}, {{6, "gh"}}},
			want:  []string{"ab", "cdef", "ghij"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := reasm.New(64)
			for i, step := range tc.steps {
				feed(a, step)
				if got := string(a.Emit()); got != tc.want[i] {
					t.Fatalf("step %d got %q want %q", i, got, tc.want[i])
				}
			}
		})
	}
}

func TestDuplicateResendDropped(t *testing.T) {
	a := reasm.New(64)
	a.Ingest(0, []byte("ab"))
	if got := string(a.Emit()); got != "ab" {
		t.Fatalf("first %q", got)
	}
	// 已吐出区间的重发，整段丢弃。
	a.Ingest(0, []byte("ab"))
	if got := a.Emit(); len(got) != 0 {
		t.Fatalf("resend emitted %q", got)
	}
	// 骑跨已吐出区间，只认新部分。
	a.Ingest(0, []byte("abcd"))
	if got := string(a.Emit()); got != "cd" {
		t.Fatalf("straddling resend %q", got)
	}
}

func TestLimitEvictsOldestGap(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		steps [][]part
		want  []string
	}{
		{
			name:  "顶满扔最老的空洞",
			limit: 8,
			steps: [][]part{
				// 缓存 10 > 8，扔掉 2..4 的空洞，空洞前的 ab 一并放弃
				{{0, "ab"}, {4, "cdef"}, {8, "ghij"}},
			},
			want: []string{"cdefghij"},
		},
		{
			name:  "被扔掉的洞迟到补发也丢弃",
			limit: 8,
			steps: [][]part{
				{{0, "ab"}, {4, "cdef"}, {8, "ghij"}},
				{{2, "XX"}}, // 2..4 已被放弃，重发整段落在已确认区间之前
			},
			want: []string{"cdefghij", ""},
		},
		{
			name:  "无空洞超限丢最靠后片段",
			limit: 4,
			steps: [][]part{
				{{0, "ab"}, {2, "cd"}, {4, "ef"}},
			},
			want: []string{"abcd"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := reasm.New(tc.limit)
			for i, step := range tc.steps {
				feed(a, step)
				if got := string(a.Emit()); got != tc.want[i] {
					t.Fatalf("step %d got %q want %q", i, got, tc.want[i])
				}
			}
		})
	}
}
