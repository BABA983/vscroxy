package app

import (
	"context"
	"testing"
)

type countingDisposable struct{ calls int }

func (d *countingDisposable) Dispose() { d.calls++ }

func TestFirstReplaceSessionStopsLiveSearches(t *testing.T) {
	a := newApp()
	t.Cleanup(func() { _ = a.srv.Close() })

	// Both searches are live on the traffic the replacement is about to
	// discard. The message search is the important one: applying the filter
	// after the replacement also stops a body search on its own.
	a.messageUI.query = "needle"
	a.messageUI.searchMessages(a, []messageView{{seq: 1, data: "other"}})
	messages := a.messageUI.searchContext
	if messages == nil || messages.Err() != nil {
		t.Fatal("message search did not start")
	}

	a.views.init(a, "")
	view := a.views.activeView()
	a.views.search = func(ctx context.Context, _ bodyJob, _ string) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	}
	a.views.startSearch(a, view, "needle", []bodyJob{{id: 1}})
	body := view.run
	if body == nil {
		t.Fatal("body search did not start")
	}
	t.Cleanup(body.cancel)

	a.replaceSession(nil, nil)

	if messages.Err() == nil {
		t.Error("first replacement left the message search running")
	}
	if body.ctx.Err() == nil {
		t.Error("first replacement left the body search running")
	}
	if view.run != nil {
		t.Error("first replacement left a body search on the new capture")
	}
}

func TestReplaceSessionDisposesOwnedResourcesOnceAndStartsAFreshStore(t *testing.T) {
	a := newApp()
	t.Cleanup(func() { _ = a.srv.Close() })
	owned := make([]*countingDisposable, 3)
	for i := range owned {
		owned[i] = new(countingDisposable)
		a.sessionStore.Add(owned[i])
	}

	a.replaceSession(nil, nil)

	for i, d := range owned {
		if d.calls != 1 {
			t.Errorf("owned[%d] disposed %d times, want 1", i, d.calls)
		}
	}
	if a.sessionStore == nil || a.sessionStore.IsDisposed() {
		t.Fatal("replaceSession left no usable store for the next capture")
	}

	next := new(countingDisposable)
	a.sessionStore.Add(next)
	a.replaceSession(nil, nil)
	for i, d := range owned {
		if d.calls != 1 {
			t.Errorf("owned[%d] disposed %d times after the next replacement, want 1", i, d.calls)
		}
	}
	if next.calls != 1 {
		t.Errorf("next capture's resource disposed %d times, want 1", next.calls)
	}
}
