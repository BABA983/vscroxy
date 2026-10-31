package app

import (
	"context"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type replaySetup struct {
	open    bool
	flows   []flow
	timeout string
	done    int
	failed  int
	problem string
	cancel  context.CancelFunc
	updates chan error
}

func (a *app) openReplay(flows []flow) {
	r := &a.replayUI
	if r.updates != nil {
		r.open = true
		return
	}
	r.flows = slices.DeleteFunc(slices.Clone(flows), func(f flow) bool { return f.url == "" || f.kind != "" })
	if r.timeout == "" {
		r.timeout = "30"
	}
	r.done, r.failed, r.problem, r.open = 0, 0, "", true
}

func (a *app) startReplay(seconds int) {
	r := &a.replayUI
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	flows := slices.Clone(r.flows)
	updates := make(chan error, len(flows))
	r.updates = updates
	go func() {
		defer cancel()
		defer close(updates)
		for _, f := range flows {
			if ctx.Err() != nil {
				break
			}
			requestCtx, finish := context.WithCancel(ctx)
			if seconds > 0 {
				finish()
				requestCtx, finish = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
			}
			err := a.replay(requestCtx, f)
			finish()
			updates <- err
			a.invalidate()
		}
		a.invalidate()
	}()
}

func (a *app) replayModal(c *ui.Context, t *ui.Theme) {
	r := &a.replayUI
	for r.updates != nil {
		select {
		case err, ok := <-r.updates:
			if !ok {
				r.updates, r.cancel = nil, nil
				a.setMessage(fmt.Sprintf("Replay finished · %d/%d sent · %d failed", r.done, len(r.flows), r.failed))
				break
			}
			r.done++
			if err != nil {
				r.failed++
				r.problem = err.Error()
			}
		default:
			goto drained
		}
	}
drained:
	if r.updates != nil {
		c.After(100 * time.Millisecond)
	}
	if !r.open {
		return
	}
	uikit.Modal(c, &r.open, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, fmt.Sprintf("Replay %d requests", len(r.flows)))
			seconds, err := strconv.Atoi(strings.TrimSpace(r.timeout))
			valid := err == nil && seconds >= 0 && seconds <= 86400
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(4)).Children(func() {
				uikit.Hint(c, "Sends in capture order through the current rules. POST, PUT, PATCH and DELETE can change server data. WebSocket, SSE and tunnels are excluded.")
				ui.Field(c, "Timeout per request (seconds; 0 = no timeout)", func() {
					uikit.Input(c, &r.timeout).Label("Replay timeout").Disabled(r.updates != nil)
				})
				if !valid {
					ui.Text(c, "Enter 0–86400 seconds.").FontSize(uikit.TextBody).TextColor(t.Danger)
				}
				if a.srv != nil && a.srv.NoCaching() {
					ui.Text(c, "No Caching is on: conditional cache headers will be removed.").FontSize(uikit.TextBody).TextColor(t.Accent)
				}
				if r.done > 0 || r.updates != nil {
					uikit.Body(c, fmt.Sprintf("%d/%d sent · %d failed", r.done, len(r.flows), r.failed))
				}
				if r.problem != "" {
					ui.Text(c, r.problem).FontSize(uikit.TextBody).TextColor(t.Danger).MaxLines(3).Tooltip(r.problem)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if r.cancel != nil && uikit.SecondaryButton(c, "Stop replay").Clicked() {
					r.cancel()
				}
				if uikit.SecondaryButton(c, "Close").Clicked() {
					r.open = false
				}
				if uikit.PrimaryButton(c, "Send replays").Disabled(!valid || len(r.flows) == 0 || r.updates != nil || r.done > 0 || a.srv == nil).Clicked() {
					a.startReplay(seconds)
				}
			})
		})
	})
}
