package app

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type quickDecoder struct {
	open                         bool
	kind, input, inputFor, error string
	inputEditor, result          *editor.State
}

func (a *app) openDecoder(reader *editor.State, fallback string) {
	if reader != nil {
		if selected := reader.SelectedText(); selected != "" {
			fallback = selected
		}
	}
	// The source is a snapshot, never a binding back into a request/editor.
	a.decoder = quickDecoder{open: true, kind: "Base64", input: fallback}
}

func (a *app) decoderMenu(reader *editor.State) {
	reader.ContextMenu = func(m *ui.Menu) {
		if m.Item("Decode selection…").Disabled(reader.SelectedText() == "").Chosen() {
			a.openDecoder(reader, "")
		}
	}
}

func (a *app) decoderModal(c *ui.Context) {
	u := &a.decoder
	if !u.open {
		return
	}
	t := c.Theme()
	_, height := c.Size()
	uikit.Modal(c, &u.open, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Height(min(530, height-70)).Children(func() {
			uikit.DialogTitle(c, "Decode text")
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				uikit.Select(c, &u.kind, []string{"Base64", "JWT", "URL component", "URL query", "Unix seconds", "Unix milliseconds"}).Label("Decoder").Width(185)
				uikit.Meta(c, "Local preview · does not change or send the request").Grow(1)
			})
			ui.Column(c).Height(110).Children(func() {
				authoredText(c, &u.input, &u.inputEditor, &u.inputFor, "Decode input", "")
			})
			if u.error != "" {
				ui.Text(c, u.error).FontSize(uikit.TextMeta).TextColor(t.Danger)
			}
			if u.result != nil {
				requestCodeEditor(c, u.result)
			} else {
				uikit.Hint(c, "Choose an encoding and Decode. JWT signatures are not verified.").Grow(1)
			}
			uikit.DialogFooter(c).Children(func() {
				ui.Row(c).Disabled(u.result == nil).Children(func() {
					if uikit.SecondaryButton(c, "Copy result").Clicked() {
						c.Services().WriteClipboard(u.result.Buffer.String())
					}
				})
				ui.Spacer(c)
				if uikit.SecondaryButton(c, "Close").Clicked() {
					u.open = false
				}
				if uikit.PrimaryButton(c, "Decode").Clicked() {
					text, err := inspect.Decode(u.kind, u.input)
					u.error, u.result = "", nil
					if err != nil {
						u.error = err.Error()
					} else {
						u.result = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, SoftWrap: true, Label: "Decoded result"}
					}
				}
			})
		})
	})
}

// cookieView reads only this exchange's headers, not the live cookie jar.
// Preserve repeated names and Set-Cookie lines; never split them on commas.
func cookieView(c *ui.Context, headers http.Header, response bool, reveal *bool) {
	t := c.Theme()
	var cookies []*http.Cookie
	invalid := 0
	if response {
		for _, line := range headers.Values("Set-Cookie") {
			cookie, err := http.ParseSetCookie(line)
			if err != nil {
				invalid++
			} else {
				cookies = append(cookies, cookie)
			}
		}
	} else {
		for _, line := range headers.Values("Cookie") {
			parsed, err := http.ParseCookie(line)
			if err != nil {
				invalid++
			} else {
				cookies = append(cookies, parsed...)
			}
		}
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Meta(c, fmt.Sprintf("%d cookies · captured headers", len(cookies))).Grow(1)
			label := "Reveal cookie values"
			if *reveal {
				label = "Hide cookie values"
			}
			ui.Row(c).Disabled(len(cookies) == 0).Children(func() {
				if uikit.SecondaryButton(c, label).Clicked() {
					*reveal = !*reveal
				}
			})
		})
		if invalid > 0 {
			ui.Text(c, fmt.Sprintf("%d invalid cookie headers; inspect Raw for original bytes", invalid)).FontSize(uikit.TextMeta).TextColor(t.Warning).Padding(0, t.Space(3))
		}
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			if len(cookies) == 0 {
				uikit.Hint(c, "No cookies recorded on this side").Padding(t.Space(3))
			}
			for i, cookie := range cookies {
				ui.Column(c).Key(i).Padding(t.Space(2), t.Space(3)).Gap(t.Space(1)).Children(func() {
					value := "••••••••  masked"
					if *reveal {
						value = cookie.Value
					}
					ui.Text(c, cookie.Name+" = "+value).Font(uikit.FontMono).FontSize(uikit.TextBody).Selectable().FillWidth()
					if response {
						expires := "Session"
						if !cookie.Expires.IsZero() {
							expires = cookie.Expires.UTC().Format(time.RFC3339)
						}
						attrs := []string{fmt.Sprintf("Domain %q · Path %q", cookie.Domain, cookie.Path), fmt.Sprintf("Expires %s · Max-Age %d", expires, cookie.MaxAge), fmt.Sprintf("Secure %t · HttpOnly %t · Partitioned %t", cookie.Secure, cookie.HttpOnly, cookie.Partitioned)}
						sameSite := map[http.SameSite]string{http.SameSiteDefaultMode: "Default", http.SameSiteLaxMode: "Lax", http.SameSiteStrictMode: "Strict", http.SameSiteNoneMode: "None"}[cookie.SameSite]
						if sameSite != "" {
							attrs = append(attrs, "SameSite "+sameSite)
						}
						uikit.Meta(c, strings.Join(attrs, "\n")).Selectable().FillWidth()
					}
				})
				ui.Divider(c)
			}
		})
	})
}
