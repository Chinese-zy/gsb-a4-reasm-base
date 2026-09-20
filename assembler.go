package reasm

import "sort"

type frag struct {
	seq  uint32
	data []byte
}

// Assembler 按序号把分片拼回字节流。
// Ingest 是送入，Emit 是取出，这两处调用方式需要保持不变。
// 序号按三十二位回绕语义比较；重叠处认先确认的字节；
// 有空洞时只吐出连续前缀；缓存字节数不超过 limit，
// 顶满时扔掉最老的空洞（放弃等待，base 跳到空洞之后）。
type Assembler struct {
	limit     int
	frags     []frag
	base      uint32
	started   bool
	committed bool
	stored    int
}

func New(limit int) *Assembler {
	if limit < 0 {
		limit = 0
	}
	return &Assembler{limit: limit}
}

// seqLess 按回绕语义比较序号（RFC 1982），窗口内差值小于 2^31 时有效。
func seqLess(x, y uint32) bool { return int32(x-y) < 0 }

// dist 返回 seq 相对 base 的有向距离。
func dist(seq, base uint32) int { return int(int32(seq - base)) }

func (a *Assembler) Ingest(seq uint32, payload []byte) {
	if len(payload) == 0 {
		return
	}
	if !a.started {
		a.base = seq
		a.started = true
	}
	if !a.committed && seqLess(seq, a.base) {
		// 还没吐过字节，流起点可以向前回退，容纳起点附近的乱序。
		a.base = seq
	}
	end := seq + uint32(len(payload))
	if !seqLess(a.base, end) {
		// 整段都落在已确认区间，属于重发，丢弃。
		return
	}
	if seqLess(seq, a.base) {
		// 头部是已确认的旧字节，裁掉。
		cut := int(a.base - seq)
		payload = payload[cut:]
		seq = a.base
	}
	// 与已缓存片段重叠的部分认先确认的字节，只保留不重叠的剩余段。
	pieces := []frag{{seq: seq, data: append([]byte(nil), payload...)}}
	for _, f := range a.frags {
		var next []frag
		for _, p := range pieces {
			next = append(next, a.subtract(p, f)...)
		}
		pieces = next
	}
	if len(pieces) == 0 {
		return
	}
	for _, p := range pieces {
		a.frags = append(a.frags, p)
		a.stored += len(p.data)
	}
	a.evict()
}

// subtract 从 p 中挖掉已被 f 覆盖的部分，返回剩余零到两段。
func (a *Assembler) subtract(p, f frag) []frag {
	ps, pe := dist(p.seq, a.base), dist(p.seq, a.base)+len(p.data)
	fs, fe := dist(f.seq, a.base), dist(f.seq, a.base)+len(f.data)
	if fe <= ps || fs >= pe {
		return []frag{p}
	}
	var out []frag
	if fs > ps {
		out = append(out, frag{seq: p.seq, data: p.data[:fs-ps]})
	}
	if fe < pe {
		out = append(out, frag{seq: p.seq + uint32(fe-ps), data: p.data[fe-ps:]})
	}
	return out
}

// evict 在缓存超过 limit 时扔掉最老的空洞；没有空洞仍超限时丢最靠后的片段。
func (a *Assembler) evict() {
	if a.limit <= 0 {
		return
	}
	for a.stored > a.limit && len(a.frags) > 0 {
		a.sortFrags()
		cur := 0
		gapAt := -1
		for i, f := range a.frags {
			fs := dist(f.seq, a.base)
			if fs > cur {
				gapAt = i
				break
			}
			if fe := fs + len(f.data); fe > cur {
				cur = fe
			}
		}
		if gapAt >= 0 {
			a.base = a.frags[gapAt].seq
			a.committed = true
			a.dropBeforeBase()
		} else {
			last := a.frags[len(a.frags)-1]
			a.stored -= len(last.data)
			a.frags = a.frags[:len(a.frags)-1]
		}
	}
}

// dropBeforeBase 丢弃完全落在 base 之前的片段，并裁掉骑跨 base 的头部。
func (a *Assembler) dropBeforeBase() {
	kept := a.frags[:0]
	for _, f := range a.frags {
		end := f.seq + uint32(len(f.data))
		if !seqLess(a.base, end) {
			a.stored -= len(f.data)
			continue
		}
		if seqLess(f.seq, a.base) {
			cut := int(a.base - f.seq)
			f.data = f.data[cut:]
			f.seq = a.base
			a.stored -= cut
		}
		kept = append(kept, f)
	}
	a.frags = kept
}

func (a *Assembler) sortFrags() {
	sort.Slice(a.frags, func(i, j int) bool {
		return seqLess(a.frags[i].seq, a.frags[j].seq)
	})
}

func (a *Assembler) Emit() []byte {
	if !a.started || len(a.frags) == 0 {
		return nil
	}
	a.committed = true
	a.sortFrags()
	var out []byte
	cur := 0
	used := 0
	for _, f := range a.frags {
		if dist(f.seq, a.base) != cur {
			// 空洞，只吐连续前缀。
			break
		}
		out = append(out, f.data...)
		cur += len(f.data)
		used++
	}
	a.frags = a.frags[used:]
	a.stored -= len(out)
	a.base += uint32(cur)
	return out
}
