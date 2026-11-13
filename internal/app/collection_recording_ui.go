package app

import (
	"cmp"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
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
			uikit.DialogTitle(c, locales.RecordIntoCollection2())
			s := a.collectionRecording
			running := s != nil && (s.Active || s.Busy)
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(3)).Padding(0, t.Space(1)).Children(func() {
				if s != nil {
					state := "Stopped"
					if s.Active {
						state = "Recording"
					} else if s.Busy {
						state = "Stopping · waiting for body read"
					}
					uikit.Body(c, state+" · "+s.Collection).FontWeight(500)
					uikit.Meta(c, locales.RecordingSummary(s.Added, s.Updated, s.Skipped, s.Discarded, len(s.queue)))
					if s.Problem != "" {
						ui.Text(c, s.Problem).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Danger)
					}
					if !running {
						uikit.SecondaryButton(c, locales.ReviewRecordedTokenLinks()).Disabled(len(s.evidence) == 0).OnClick(func() {
							w.open = false
							a.openTokenLinks(s.evidence)
						})
					}
					uikit.Hint(c, locales.TokenLinkEvidenceIsBounded())
				}
				ui.Column(c).Gap(t.Space(3)).Disabled(running).Children(func() {
					ui.Field(c, locales.DestinationCollection(), func() {
						ui.Row(c).Gap(t.Space(2)).Children(func() {
							uikit.Input(c, &w.collection).Label(locales.RecordingCollection()).Grow(1).MinWidth(0)
							uikit.MenuSelect(c, locales.ChooseExisting(), func(m *ui.Menu) {
								for _, name := range a.collectionNames() {
									if name != "" && m.Item(name).Chosen() {
										w.collection = name
									}
								}
							})
						})
					}).Description(locales.NewOrExistingCollectionFolder())
					ui.Field(c, locales.VariableEnvironment(), func() {
						uikit.Select(c, &w.environment, a.envNames()).Label(locales.RecordingEnvironment())
					}).Description(locales.ExistingEnvironmentRecordingDoesNot())
					if len(a.envs) == 0 {
						uikit.Hint(c, locales.CreateAnEnvironmentBeforeStarting())
					}
					uikit.SecondaryButton(c, locales.ManageEnvironments()).OnClick(func() { w.open = false; a.openVars() })
					ui.Field(c, locales.HostsOnePerLine(), func() {
						ui.TextArea(c, &w.hosts).Label(locales.RecordingHosts()).Height(60).FontSize(uikit.Scaled(uikit.TextBody)).Placeholder("api.example.com\nlocalhost:3000")
					}).Description(locales.ExactHostnameAnyPortOr())
					ui.Field(c, locales.AppsOnePerLine(), func() {
						ui.TextArea(c, &w.apps).Label(locales.RecordingApps()).Height(60).FontSize(uikit.Scaled(uikit.TextBody)).Placeholder(locales.Browser())
					}).Description(locales.ExactSidebarAppLabelsEmpty())
				})
				uikit.Hint(c, locales.NewAmbientHttpTrafficOnly())
				uikit.Hint(c, locales.AuthCookieApiKeyHeaders())
				if !running {
					ui.Checkbox(c, &w.reviewed, "I understand captured content will be saved locally").FontSize(uikit.Scaled(uikit.TextBody))
				}
				if w.problem != "" {
					ui.Text(c, w.problem).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Danger)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				uikit.Meta(c, locales.StopDiscardsUnsavedWorkSaved()).Grow(1).MinWidth(0)
				uikit.SecondaryButton(c, locales.Close()).OnClick(func() { w.open = false })
				if s != nil && s.Active {
					uikit.PrimaryButton(c, locales.StopCollectionRecording()).OnClick(func() { a.stopCollectionRecording("") })
				} else {
					uikit.PrimaryButton(c, locales.StartCollectionRecording()).Disabled(running || !w.reviewed || w.environment == "").OnClick(func() {
						err := a.startCollectionRecording(collectionRecordingConfig{Collection: cleanPath(w.collection), Environment: w.environment, Hosts: captureLines(w.hosts), Apps: captureLines(w.apps)})
						if err != nil {
							w.problem = err.Error()
							return
						}
						w.problem, w.open = "", false
						a.done(locales.RecordingInto(w.collection))
					})
				}
			})
		})
	})
}

// The always-visible tools strip also shows remote-started sessions. Closing a
// chip stops recording; dismissing an already stopped result never deletes data.
func captureLines(text string) []string {
	var values []string
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			values = append(values, line)
		}
	}
	return values
}
