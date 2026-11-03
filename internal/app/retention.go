package app

// Retention: how much of a long capture the table keeps. Three limits, set in
// Settings → Capture, bound it: a number of requests, the memory their texts
// and messages take, and the disk their spill files take. Past any, the oldest
// requests go first; pinned ones stay. A request that goes takes its spill
// files with it, so a capture left running fills neither memory nor the disk.

import "cmp"

const (
	defaultFlowLimit   = 5000
	defaultMemoryLimit = 512 // MB
	defaultDiskLimit   = 5   // GB
)

var (
	flowLimits   = []int{1000, 5000, 20000, 50000}
	memoryLimits = []int{128, 256, 512, 1024, 2048} // MB
	diskLimits   = []int{1, 2, 5, 10, 20}           // GB
)

// retained estimates the memory a flow keeps: its texts, with the headers
// counted twice for their parsed maps, and the fields around them.
func (f *flow) retained() int64 {
	n := len(f.url) + len(f.host) + len(f.path) + len(f.reqBody) + len(f.resBody) +
		len(f.conn.log) + len(f.note) + 2*(len(f.reqHeaders)+len(f.resHeaders))
	return int64(n + 512)
}

// spilled is what a flow's bodies take in the capture's spill files: a
// spilled body's file holds all of it.
func (f *flow) spilled() int64 {
	var n int64
	if f.reqFile != "" {
		n += int64(f.reqSize)
	}
	if f.resFile != "" {
		n += int64(f.resSize)
	}
	return n
}

// messagesRetained estimates the memory a flow's kept messages take, and
// messagesSpilled what their spill files take.
func messagesRetained(ms []frame) int64 {
	var n int64
	for _, m := range ms {
		n += int64(len(m.data) + 128)
	}
	return n
}

func messagesSpilled(ms []frame) int64 {
	var n int64
	for _, m := range ms {
		if m.file != "" {
			n += int64(m.kept)
		}
	}
	return n
}

// retainedTotal is the memory every kept flow takes, and the disk their spill
// files take.
func (a *app) retainedTotal() (memory, disk int64) {
	return a.tally.memory, a.tally.disk
}

// trimFlows drops the oldest unpinned flows while the table holds more than
// any limit and deletes their spill files. The flows before past are gone
// but for the pinned ones, and every later flow moved down by from: rows
// that point into a.flows are remapped here, and callers holding others map
// them the same way.
func (a *app) trimFlows() (from, past int) {
	if a.tallyStale() {
		a.recount()
	}
	maxCount := cmp.Or(a.flowLimit, defaultFlowLimit)
	maxMemory := int64(cmp.Or(a.memoryLimit, defaultMemoryLimit)) << 20
	maxDisk := int64(cmp.Or(a.diskLimit, defaultDiskLimit)) << 30
	t := &a.tally
	count := len(a.flows)
	over := func() bool { return count > maxCount || t.memory > maxMemory || t.disk > maxDisk }
	if !over() {
		return 0, 0
	}
	var files []string
	var pinned []flow
	moved := make(map[int]int) // a kept pinned flow's old index to its new
	for ; past < len(a.flows) && over(); past++ {
		f := &a.flows[past]
		if f.pinned {
			moved[past] = len(pinned)
			pinned = append(pinned, *f)
			continue
		}
		a.count(f, -1)
		count--
		files = append(files, f.reqFile, f.resFile)
		for _, m := range a.frames[f.id] {
			files = append(files, m.file)
		}
		delete(a.frames, f.id)
	}
	// The pinned flows of the dropped stretch close it, in their order, and
	// the slice starts at them: the flows after them never move in memory.
	from = past - len(pinned)
	copy(a.flows[from:past], pinned)
	clear(a.flows[:from]) // let the dropped bodies be collected
	a.flows = a.flows[from:]
	remap := func(rows []int) []int {
		kept := rows[:0]
		for _, at := range rows {
			if at >= past {
				kept = append(kept, at-from)
			} else if n, ok := moved[at]; ok {
				kept = append(kept, n)
			}
		}
		return kept
	}
	a.visible, a.activityRows = remap(a.visible), remap(a.activityRows)
	a.activityCounts.valid = false
	a.views.rows = len(a.visible)
	a.restoreSelection()
	a.listCounts()
	a.discardCaptureFiles(files)
	return from, past
}
