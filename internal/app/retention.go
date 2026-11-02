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

// retainedTotal estimates the memory every kept flow takes, and the disk
// their spill files take.
func (a *app) retainedTotal() (memory, disk int64) {
	for i := range a.flows {
		ms := a.frames[a.flows[i].id]
		memory += a.flows[i].retained() + messagesRetained(ms)
		disk += a.flows[i].spilled() + messagesSpilled(ms)
	}
	return memory, disk
}

// trimFlows drops the oldest unpinned flows while the table holds more than
// any limit and deletes their spill files.
func (a *app) trimFlows() {
	maxCount := cmp.Or(a.flowLimit, defaultFlowLimit)
	maxMemory := int64(cmp.Or(a.memoryLimit, defaultMemoryLimit)) << 20
	maxDisk := int64(cmp.Or(a.diskLimit, defaultDiskLimit)) << 30
	memory, disk := make([]int64, len(a.flows)), make([]int64, len(a.flows))
	var memoryTotal, diskTotal int64
	for i := range a.flows {
		ms := a.frames[a.flows[i].id]
		memory[i] = a.flows[i].retained() + messagesRetained(ms)
		disk[i] = a.flows[i].spilled() + messagesSpilled(ms)
		memoryTotal += memory[i]
		diskTotal += disk[i]
	}
	count := len(a.flows)
	over := func() bool { return count > maxCount || memoryTotal > maxMemory || diskTotal > maxDisk }
	if !over() {
		return
	}
	var files []string
	kept := a.flows[:0]
	for i, f := range a.flows {
		if over() && !f.pinned {
			count--
			memoryTotal -= memory[i]
			diskTotal -= disk[i]
			files = append(files, f.reqFile, f.resFile)
			for _, m := range a.frames[f.id] {
				files = append(files, m.file)
			}
			continue
		}
		kept = append(kept, f)
	}
	clear(a.flows[len(kept):]) // let the dropped bodies be collected
	a.flows = kept
	a.frames = pruneMessages(a.frames, a.flows)
	if a.srv != nil {
		a.srv.Discard(files)
	}
}
