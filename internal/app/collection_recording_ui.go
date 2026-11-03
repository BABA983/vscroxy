package app

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

type collectionRecordingEditor struct {
	open, reviewed          bool
	collection, environment string
	hosts, apps, problem    string
}

func (a *app) openCollectionRecording(collection, host, process string) {
	w := &a.recordCollectionUI
	if s := a.collectionRecording; s != nil {
		*w = collectionRecordingEditor{open: true, collection: s.Collection, environment: s.Environment, hosts: strings.Join(s.Hosts, "\n"), apps: strings.Join(s.Apps, "\n")}
		if s.Active || s.Busy {
			return // opening another entry never changes a running scope
		}
	} else {
		*w = collectionRecordingEditor{open: true, collection: a.newCollectionName("Recorded API"), environment: a.activeEnv}
		if w.environment == "" && len(a.envs) > 0 {
			w.environment = a.envs[0].Name
		}
	}
	w.collection = cmp.Or(collection, w.collection)
	if host != "" || process != "" {
		w.hosts, w.apps = host, process
	}
}

func (a *app) collectionRecordingModal(c *ui.Context, t *ui.Theme) {
	w := &a.recordCollectionUI
	if !w.open {
		return
	}
	if s := a.collectionRecording; s != nil && (s.Active || s.Busy) {
		// Automation can start while this form is already open. Disabled
		// controls must show the actual scope, not an older local draft.
		w.collection, w.environment = s.Collection, s.Environment
		w.hosts, w.apps = strings.Join(s.Hosts, "\n"), strings.Join(s.Apps, "\n")
	}
	uikit.Modal(c, &w.open, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, "Record into collection")
			s := a.collectionRecording
			running := s != nil && (s.Active || s.Busy)
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(3)).Children(func() {
				if s != nil {
					state := "Stopped"
					if s.Active {
						state = "Recording"
					} else if s.Busy {
						state = "Stopping · waiting for body read"
					}
					uikit.Body(c, state+" · "+s.Collection).FontWeight(600)
					uikit.Meta(c, fmt.Sprintf("%d added · %d updated · %d skipped · %d discarded · %d queued", s.Added, s.Updated, s.Skipped, s.Discarded, len(s.queue)))
					if s.Problem != "" {
						ui.Text(c, s.Problem).FontSize(uikit.TextBody).TextColor(t.Danger)
					}
				}
				ui.Column(c).Gap(t.Space(3)).Disabled(running).Children(func() {
					ui.Field(c, "Destination collection", func() {
						ui.Row(c).Gap(t.Space(2)).Children(func() {
							uikit.Input(c, &w.collection).Label("Recording collection").Grow(1).MinWidth(0)
							uikit.MenuSelect(c, "Choose existing…", func(m *ui.Menu) {
								for _, name := range a.collectionNames() {
									if name != "" && m.Item(name).Chosen() {
										w.collection = name
									}
								}
							})
						})
					}).Description("New or existing collection/folder. Created when the first request is saved.")
					ui.Field(c, "Variable environment", func() {
						uikit.Select(c, &w.environment, a.envNames()).Label("Recording environment")
					}).Description("Existing environment; recording does not switch the active environment.")
					if len(a.envs) == 0 {
						uikit.Hint(c, "Create an environment before starting.")
					}
					uikit.SecondaryButton(c, "Manage environments…").OnClick(func() { w.open = false; a.openVars() })
					ui.Field(c, "Hosts · one per line", func() {
						ui.TextArea(c, &w.hosts).Label("Recording hosts").Height(60).FontSize(uikit.TextBody).Placeholder("api.example.com\nlocalhost:3000")
					}).Description("Exact hostname (any port) or host:port. Empty = all hosts; no wildcards.")
					ui.Field(c, "Apps · one per line", func() {
						ui.TextArea(c, &w.apps).Label("Recording apps").Height(60).FontSize(uikit.TextBody).Placeholder("Browser")
					}).Description("Exact sidebar App labels. Empty = all apps. Host and App filters intersect.")
				})
				uikit.Hint(c, "New ambient HTTP traffic only. Static resources, streams, network failures and app sends/replays are excluded. Repeated endpoints update Examples, never the authored request.")
				uikit.Hint(c, "Auth/Cookie/API-key headers use local Current values (plaintext, not exported). Other headers, queries and bodies may contain secrets; review before sharing.")
				if !running {
					ui.Checkbox(c, &w.reviewed, "I understand captured content will be saved locally").FontSize(uikit.TextBody)
				}
				if w.problem != "" {
					ui.Text(c, w.problem).FontSize(uikit.TextBody).TextColor(t.Danger)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				uikit.Meta(c, "Stop discards unsaved work; saved requests stay.").Grow(1).MinWidth(0)
				uikit.SecondaryButton(c, "Close").OnClick(func() { w.open = false })
				if s != nil && s.Active {
					uikit.PrimaryButton(c, "Stop collection recording").OnClick(func() { a.stopCollectionRecording("") })
				} else {
					uikit.PrimaryButton(c, "Start collection recording").Disabled(running || !w.reviewed || w.environment == "").OnClick(func() {
						err := a.startCollectionRecording(collectionRecordingConfig{Collection: cleanPath(w.collection), Environment: w.environment, Hosts: captureLines(w.hosts), Apps: captureLines(w.apps)})
						if err != nil {
							w.problem = err.Error()
							return
						}
						w.problem, w.open = "", false
						a.done("Recording new traffic into " + w.collection)
					})
				}
			})
		})
	})
}

// The always-visible tools strip also shows remote-started sessions. Closing a
// chip stops recording; dismissing an already stopped result never deletes data.
func (a *app) collectionRecordingChip(c *ui.Context, t *ui.Theme) {
	s := a.collectionRecording
	if s == nil {
		return
	}
	state := "Stopped"
	if s.Active {
		state = "Recording"
	} else if s.Busy {
		state = "Stopping"
	}
	ui.Row(c.Key("collection-recording-chip")).Height(uikit.HeightCompact).AlignItems(ui.Center).Background(t.Background).Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
		uikit.ToolbarButton(c, "Collection recording status", "View recording scope, counters and errors", false).Height(uikit.HeightCompact).Children(func() {
			ui.Icon(c, captureIcon).Size(uikit.IconInline, uikit.IconInline)
			uikit.Meta(c, state+" → "+s.Collection).MaxWidth(180).SingleLine()
			uikit.Meta(c, fmt.Sprintf("+%d / ↻%d", s.Added, s.Updated)).FontFeatures("tnum")
		}).OnClick(func() { a.openCollectionRecording("", "", "") })
		if s.Active {
			uikit.IconButton(c, "Stop collection recorder", "Stop and discard queued work; keep saved requests", closeIcon).OnClick(func() { a.stopCollectionRecording("") })
		} else {
			uikit.IconButton(c, "Dismiss recording status", "Dismiss status; saved requests stay", closeIcon).Disabled(s.Busy).OnClick(func() { a.collectionRecording = nil })
		}
	})
}

func captureLines(text string) []string {
	var values []string
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			values = append(values, line)
		}
	}
	return values
}
