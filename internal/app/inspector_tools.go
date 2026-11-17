package app

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type quickDecoder struct {
	open                         bool
	kind, input, inputFor, error string
	inputEditor, result          *editor.State
	fields                       []inspect.FormField
	image                        *ui.Bitmap
	mediaType                    string
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
	uikit.Modal(c, &u.open, locales.DecodeText2(), func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Height(min(530, height-70)).Children(func() {
			uikit.DialogTitle(c, locales.DecodeText2())
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				uikit.SelectLabels(c, &u.kind, []string{"Base64", "Base64 image", "JWT", "JSON", "XML", "Key-value", "URL component", "URL query", "Unix seconds", "Unix milliseconds"}, optionLabel).Label(locales.Decoder()).Width(185)
				uikit.Meta(c, locales.LocalPreviewDoesNotChange()).Grow(1)
			})
			ui.Column(c).Height(110).Children(func() {
				authoredText(c, &u.input, &u.inputEditor, &u.inputFor, "Decode input", "")
			})
			if u.error != "" {
				uikit.ErrorText(c, u.error)
			}
			switch {
			case len(u.fields) > 0:
				decodedFields(c, u.fields)
			case u.image != nil:
				decodedImage(c, u.image, u.mediaType)
			case u.result != nil:
				requestCodeEditor(c, u.result)
			default:
				uikit.Hint(c, locales.ChooseAnEncodingAndDecode()).Grow(1)
			}
			uikit.DialogFooter(c).Children(func() {
				ui.Row(c).Disabled(u.result == nil || u.image != nil).Children(func() {
					if uikit.SecondaryButton(c, locales.CopyResult()).Clicked() {
						c.Services().WriteClipboard(u.result.Buffer.String())
					}
				})
				ui.Spacer(c)
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					u.open = false
				}
				if uikit.PrimaryButton(c, locales.Decode()).Clicked() {
					decoded, err := inspect.DecodeValue(u.kind, u.input)
					u.error, u.result, u.fields, u.image, u.mediaType = "", nil, nil, nil, ""
					if err != nil {
						u.error = err.Error()
					} else {
						u.fields, u.mediaType = decoded.Fields, decoded.MediaType
						text := decoded.Text
						if len(decoded.Fields) > 0 {
							var lines strings.Builder
							for _, field := range decoded.Fields {
								lines.WriteString(field.Key + "\t" + field.Value + "\n")
							}
							text = lines.String()
						}
						if len(decoded.Image) > 0 {
							bitmap, err := decodePreviewImage(decoded.Image)
							if err != nil {
								var tooLarge imageTooLargeError
								if errors.As(err, &tooLarge) {
									u.error = locales.ImagePreviewTooLarge(tooLarge.width, tooLarge.height, previewImagePixels>>20)
								} else {
									u.error = locales.CannotDecodeImage(err.Error())
								}
							} else {
								u.image = bitmap
							}
						}
						if text != "" {
							u.result = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, SoftWrap: true, Label: locales.DecodedResult()}
						}
					}
				}
			})
		})
	})
}

// decodedFields shows key-value pairs as a table instead of one text blob.
func decodedFields(c *ui.Context, fields []inspect.FormField) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceXS)).Children(func() {
		for _, field := range fields {
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				uikit.Caption(c, field.Key).Width(uikit.WidthFieldWide).TextColor(uikit.Colors(t).TextMuted)
				uikit.Caption(c, field.Value).Grow(1)
			})
		}
	})
}

// decodedImage shows a Base64 picture at the dialog's width, never larger
// than its source.
func decodedImage(c *ui.Context, bitmap *ui.Bitmap, mediaType string) {
	t := c.Theme()
	width, height := bitmap.Size()
	ui.Column(c).Grow(1).AlignItems(ui.Center).Padding(t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
		ui.Image(c, bitmap).MaxWidthPercent(100).MaxHeight(320)
		uikit.Meta(c, fmt.Sprintf("%s · %d×%d", mediaType, width, height))
	})
}

// cookieView reads only this exchange's headers, not the live cookie jar.
// Preserve repeated names and Set-Cookie lines; never split them on commas.
func cookieView(c *ui.Context, headers http.Header, response bool, reveal *bool, save func()) {
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
		ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Children(func() {
			uikit.Meta(c, locales.CookiesCaptured(len(cookies))).Grow(1)
		})
		ui.Row(c).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			if save != nil && len(cookies) > 0 && uikit.SecondaryButton(c, locales.SaveCookiesToJar()).Clicked() {
				save()
			}
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
			uikit.Caption(c, locales.InvalidCookies(invalid)).TextColor(t.Warning).Padding(0, t.Space(uikit.SpaceL))
		}
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			if len(cookies) == 0 {
				uikit.Hint(c, locales.NoCookiesRecordedOnThis()).Padding(t.Space(uikit.SpaceL))
			}
			for i, cookie := range cookies {
				ui.Column(c.Key(i)).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					value := "••••••••  masked"
					if *reveal {
						value = cookie.Value
					}
					uikit.DataBody(c, cookie.Name+" = "+value).Selectable().FillWidth()
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
