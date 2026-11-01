package app

// Retention: how much of a long capture the table keeps. Two limits, set in
// Settings → Capture, bound it: a number of requests and the memory their
// texts and messages take. Past either, the oldest requests go first; pinned
// ones stay. A request that goes takes its spill files with it, so a capture
// left running does not fill the disk either.

import "cmp"

const (
	defaultFlowLimit   = 5000
	defaultMemoryLimit = 512 // MB
)

var (
	flowLimits   = []int{1000, 5000, 20000, 50000}
	memoryLimits = []int{128, 256, 512, 1024, 2048} // MB
)

// retained estimates the memory a flow keeps: its texts, with the headers
// counted twice for their parsed maps, and the fields around them.
func (f *flow) retained() int64 {
	n := len(f.url) + len(f.host) + len(f.path) + len(f.reqBody) + len(f.resBody) +
		len(f.conn.log) + len(f.note) + 2*(len(f.reqHeaders)+len(f.resHeaders))
	return int64(n + 512)
}

// messagesRetained estimates the memory a flow's kept messages take.
func messagesRetained(ms []frame) int64 {
	var n int64
	for _, m := range ms {
		n += int64(len(m.data) + 128)
	}
	return n
}

// retainedTotal estimates the memory every kept flow takes.
func (a *app) retainedTotal() int64 {
	var n int64
	for i := range a.flows {
		n += a.flows[i].retained() + messagesRetained(a.frames[a.flows[i].id])
	}
	return n
}

// trimFlows drops the oldest unpinned flows while the table holds more than
// either limit and deletes their spill files.
func (a *app) trimFlows() {
	maxCount := cmp.Or(a.flowLimit, defaultFlowLimit)
	maxBytes := int64(cmp.Or(a.memoryLimit, defaultMemoryLimit)) << 20
	sizes := make([]int64, len(a.flows))
	var total int64
	for i := range a.flows {
		sizes[i] = a.flows[i].retained() + messagesRetained(a.frames[a.flows[i].id])
		total += sizes[i]
	}
	count := len(a.flows)
	if count <= maxCount && total <= maxBytes {
		return
	}
	var files []string
	kept := a.flows[:0]
	for i, f := range a.flows {
		if (count > maxCount || total > maxBytes) && !f.pinned {
			count--
			total -= sizes[i]
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
