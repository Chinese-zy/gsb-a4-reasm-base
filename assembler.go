package reasm

// frag 是一段已缓存、尚未交付的字节。frags 始终按相对 next 的
// 回绕安全坐标升序排列，且两两不相交。
type frag struct {
	seq  uint32
	data []byte
}

// Assembler 按序号把分片拼回字节流。
// Ingest 是送入，Emit 是取出，这两处调用方式需要保持不变。
//
// 序号按 32 位回绕比较；重叠部分以先确认下来的字节为准；
// 完全重复的片段直接丢弃；Emit 只交付从下一个期望序号开始的
// 连续前缀，中间有洞就等洞补上；缓存字节数不超过 New 给的上限，
// 顶满时从位置最老的片段开始丢弃。
type Assembler struct {
	limit   int
	next    uint32
	started bool
	emitted bool
	frags   []frag
	stored  int
}

func New(limit int) *Assembler {
	if limit < 0 {
		limit = 0
	}
	return &Assembler{limit: limit}
}

// rel 把序号换算成相对 next 的回绕安全偏移。
func (a *Assembler) rel(seq uint32) int64 {
	return int64(int32(seq - a.next))
}

func (a *Assembler) Ingest(seq uint32, payload []byte) {
	if len(payload) == 0 {
		return
	}
	if !a.started {
		a.next = seq
		a.started = true
	}

	start := a.rel(seq)
	end := start + int64(len(payload))
	if end <= 0 {
		if a.emitted {
			return // 整段都落在已交付的位置之前，属于重发，丢弃
		}
		// 还没交付过字节，说明首次锚定的 next 偏后，向回绕方向重锚。
		a.next = seq
		start = 0
		end = int64(len(payload))
	}
	if start < 0 {
		if a.emitted {
			payload = payload[-start:]
			start = 0
		} else {
			a.next = seq
			start = 0
			end = int64(len(payload))
		}
	}

	// 剪掉与已缓存片段重叠的部分：先确认下来的字节优先。
	type span struct{ lo, hi int64 }
	var pieces []span
	cur := start
	for _, f := range a.frags {
		flo, fhi := a.rel(f.seq), a.rel(f.seq)+int64(len(f.data))
		if fhi <= cur {
			continue
		}
		if flo >= end {
			break
		}
		if flo > cur {
			pieces = append(pieces, span{cur, flo})
		}
		if fhi > cur {
			cur = fhi
		}
	}
	if cur < end {
		pieces = append(pieces, span{cur, end})
	}

	for _, p := range pieces {
		data := make([]byte, p.hi-p.lo)
		copy(data, payload[p.lo-start:p.hi-start])
		a.insert(a.next+uint32(p.lo), data)
	}
	a.enforceLimit()
}

// insert 按相对位置把不相交的新片段插进有序缓存。
func (a *Assembler) insert(seq uint32, data []byte) {
	at := len(a.frags)
	for i, f := range a.frags {
		if a.rel(f.seq) > a.rel(seq) {
			at = i
			break
		}
	}
	a.frags = append(a.frags, frag{})
	copy(a.frags[at+1:], a.frags[at:])
	a.frags[at] = frag{seq: seq, data: data}
	a.stored += len(data)
}

// enforceLimit 顶满时从位置最老的缓存片段开始丢弃，保证内存有上限。
// 被扔的片段若正顶着 next，就把 next 一并跳过，扔出来的空洞不再等。
func (a *Assembler) enforceLimit() {
	for a.stored > a.limit && len(a.frags) > 0 {
		f := a.frags[0]
		a.stored -= len(f.data)
		a.frags = a.frags[1:]
		if r := a.rel(f.seq); r <= 0 {
			a.next = f.seq + uint32(len(f.data))
			a.emitted = true // 位置已被丢弃消费，不再向前重锚
		}
	}
}

// Emit 只交付从 next 开始的连续前缀；最前面是洞就返回空。
func (a *Assembler) Emit() []byte {
	if !a.started {
		return nil
	}
	var out []byte
	for len(a.frags) > 0 {
		f := a.frags[0]
		if a.rel(f.seq) > 0 {
			break // 前面还有洞，后面的先不吐
		}
		out = append(out, f.data...)
		a.next += uint32(len(f.data))
		a.stored -= len(f.data)
		a.frags = a.frags[1:]
		a.emitted = true
	}
	return out
}
