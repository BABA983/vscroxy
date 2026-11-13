package app

// settleEvent keeps an event list's selection on a row it has. A list keeps its
// rows by key, and maps a selected key it no longer has to -1: the ring dropped
// that event, or the search hid it. The selection goes to the newest event that
// is still there, as the message list does, instead of falling back to the
// oldest one, which read as the detail jumping back in time.
func settleEvent(selected *int, n int) {
	if *selected >= 0 && *selected < n {
		return
	}
	*selected = n - 1
}
