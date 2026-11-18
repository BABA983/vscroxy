package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// protoInspector owns one off-thread inspection and the local schema registry.
// Changing a flow, side, type or schema cancels the old answer, so a late decode
// can never appear under another request.
type protoInspector struct {
	schema   *protoSchema
	store    string
	name     string
	key      string
	result   protoResult
	answers  chan protoResult
	cancel   context.CancelFunc
	selected int
	query    string
	url      string // the flow the type is being chosen for
	ruleFor  string // the URL whose saved rule was applied
	list     ui.ListState
	split    float32
	splitV   float32
	// readerHeight is the reader's viewport as the last frame laid it out:
	// the pane fills it, and scrolls only when its controls and messages
	// need more than it has.
	readerHeight float32
	readerAsked  bool

	open    bool
	source  string
	roots   string
	problem string
	loading bool
	imports chan protoImportResult
}

type protoImportResult struct {
	schema *protoSchema
	err    error
}

func (p *protoInspector) init(path string) {
	p.store = path
	data, err := readProtoFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		p.schema, err = parseProtoSchema(data)
	}
	if err != nil {
		p.problem = locales.SavedProtobufSchema(err.Error())
	}
}

func (p *protoInspector) stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.cancel, p.answers = nil, nil
	p.key = ""
}

func (p *protoInspector) inspect(c *ui.Context, a *app, key string, read func(context.Context) protoResult) {
	if key != p.key {
		p.stop()
		p.key, p.selected, p.query = key, 0, ""
		p.result = protoResult{}
		ctx, cancel := context.WithCancel(context.Background())
		answers := make(chan protoResult, 1)
		p.cancel, p.answers = cancel, answers
		go guarded(func() {
			result := read(ctx)
			select {
			case answers <- result:
				a.invalidate()
			case <-ctx.Done():
			}
		})
	}
	if p.answers != nil {
		// The worker asks for a frame when it has the answer, but that needs a
		// window: without one, as in a test, the pane asks again while the read
		// is outstanding, and stops once the answer is in.
		select {
		case p.result = <-p.answers:
			p.answers = nil
		default:
			c.After(50 * time.Millisecond)
		}
	}
}

func (p *protoInspector) view(c *ui.Context, a *app, f flow, side int) {
	// The read stops with the view that asked for it, rather than the window
	// working out from the inspector's state whether this pane is showing.
	defer ui.Column(c).OnUnmount(p.stop)
	response := side == 0
	p.applyURLRule(a, f.url)
	p.url = f.url
	p.controls(c, a, p)
	schema, name := p.schema, p.name
	p.inspect(c, a, fmt.Sprintf("%d:%t:%s:%p", f.id, response, name, schema), func(ctx context.Context) protoResult {
		return inspectProto(ctx, f, response, schema, name)
	})
	if grpcContentType(f.contentType(response)) {
		uikit.Meta(c, grpcStatus(f)).Padding(0, c.Theme().Space(uikit.SpaceL), c.Theme().Space(uikit.SpaceM), c.Theme().Space(uikit.SpaceL))
	}
	p.resultView(c, a)
}

func (p *protoInspector) message(c *ui.Context, a *app, key string, m messageView) {
	p.schema = a.protobuf.schema
	ui.Scroll(c).Label(locales.MessageProtobufReader()).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).Fill().MinHeight(320).Children(func() {
			p.applyURLRule(a, p.url)
			p.controls(c, a, &a.protobuf)
			schema, name := p.schema, p.name
			p.inspect(c, a, fmt.Sprintf("message:%s:%s:%p", key, name, schema), func(ctx context.Context) protoResult {
				return inspectProtoMessage(ctx, m, schema, name)
			})
			p.resultView(c, a)
		})
	})
}

func (p *protoInspector) controls(c *ui.Context, a *app, registry *protoInspector) {
	t := c.Theme()
	// Keep the type and its schema together; only search wraps in a narrow
	// inspector. Both fields share the surrounding controls' density.
	ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Wrap().AlignItems(ui.Center).Children(func() {
		// The tip is on the row, not the combobox: a tip wrapped round the
		// combobox clipped the decoded text below it (paint only, same bounds).
		uikit.Tip(c, ui.Row(c).Grow(1).Basis(280).MinWidth(0).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			options := []string{"Auto"}
			if p.schema != nil {
				options = append(options, p.schema.names...)
			}
			if p.name == "" {
				p.name = "Auto"
			}
			uikit.Combobox(c, &p.name, options).Label(locales.ProtobufMessageType()).Grow(1).MinWidth(0)
			if uikit.SecondaryButton(c, locales.Schema()).Shrink(0).Clicked() {
				registry.open = true
			}
		}), locales.AutoResolvesHttpGrpcMethods())
		uikit.SearchField(c, &p.query, locales.SearchProtobufMessages(), locales.SearchMessages2(), false).Grow(1).Basis(180).MinWidth(0)
	})
	if p.url != "" && p.name != "" && p.name != "Auto" {
		ui.Row(c).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Wrap().Children(func() {
			if uikit.SecondaryButton(c, locales.SaveForThisUrl()).Clicked() && a.saveProtoTypeRule(p.url, p.name) {
				a.done(locales.SavedProtobufTypeForUrl())
			}
			if a.protoTypeRuleFor(p.url) != "" {
				if uikit.SecondaryButton(c, locales.ForgetUrlRule()).Clicked() {
					a.forgetProtoTypeRule(p.url)
				}
			}
			uikit.Meta(c, p.url).Font(uikit.FontMono).SingleLine().MaxWidth(320)
		})
	}
}

func (p *protoInspector) resultView(c *ui.Context, a *app) {
	t := c.Theme()
	if p.answers != nil {
		uikit.Hint(c, locales.ReadingCompleteRetainedPayload()).Padding(t.Space(uikit.SpaceL))
		return
	}
	if p.result.problem != "" {
		uikit.ErrorText(c, p.result.problem).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM), t.Space(uikit.SpaceL))
		if len(p.result.messages) == 0 {
			return
		}
	}
	note := locales.SchemaLess()
	if p.result.typeName != "" {
		note = locales.MessageTypeNamed(p.result.typeName)
	}
	uikit.Tip(c, uikit.Meta(c, note).SingleLine(), note).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM), t.Space(uikit.SpaceL))
	var visible []int
	for i, message := range p.result.messages {
		if p.query == "" || strings.Contains(strings.ToLower(message.text), strings.ToLower(p.query)) {
			visible = append(visible, i)
		}
	}
	uikit.Meta(c, locales.ProtobufMessages(len(visible), len(p.result.messages))).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM), t.Space(uikit.SpaceL))
	if len(visible) == 0 {
		note := locales.NoMessageMatches()
		if len(p.result.messages) == 0 && p.result.problem == "" {
			note = locales.NoMessagesInBody()
		}
		uikit.Hint(c, note).Padding(t.Space(uikit.SpaceL))
		return
	}
	if p.selected >= len(visible) {
		p.selected = len(visible) - 1
	}
	list := func() {
		p.list.Selected = &p.selected
		p.list.Key = func(i int) any { return visible[i] }
		p.list.Label = func(i int) string { return fmt.Sprintf("Message %d", visible[i]+1) }
		uikit.DataListIndexed(c, &p.list, len(visible), func(i int) {
			message := p.result.messages[visible[i]]
			note := fmt.Sprintf("#%d · %s", visible[i]+1, humanBytes(message.size))
			if message.compressed {
				note += " · gzip"
			}
			uikit.Caption(c, note).TextColor(t.Text).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).SingleLine()
		}).Grow(1)
	}
	detail := func() {
		// List maps a selected key it no longer has to -1: the message went
		// away, and the detail says so rather than showing the oldest one.
		settleEvent(&p.selected, len(visible))
		message := p.result.messages[visible[p.selected]]
		ui.Column(c).Fill().MinHeight(0).Children(func() {
			detail := locales.MessageIndexName(visible[p.selected]+1, humanBytes(message.size))
			ui.Row(c).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).AlignItems(ui.Center).Gap(t.Space(uikit.SpaceM)).Children(func() {
				uikit.Tip(c, uikit.Meta(c, detail).Grow(1).MinWidth(0).SingleLine(), detail)
				if uikit.IconButton(c, locales.CopyDecoded(), locales.CopyDecoded(), copyIcon).Clicked() {
					c.Services().WriteClipboard(message.text)
					a.done(locales.CopiedDecodedProtobufMessage())
				}
			})
			text := message.text
			if len(text) > maxMessageText {
				text = text[:maxMessageText] + locales.TextPreviewLimited64KiB()
			}
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceL)).Children(func() {
				ui.Column(c).Padding(0, 0, t.Space(uikit.SpaceM), 0).Children(func() {
					uikit.DataBody(c, text).TextColor(t.Text)
				})
			})
		})
	}
	// A narrow inspector gives the decoded text the full width: a long field
	// such as "Studio headphones" should not wrap into a second line. Wide
	// panes keep the list beside the reader.
	if a.inspectorWidth(c) < 560 {
		if p.splitV == 0 {
			p.splitV = 96
		}
		ui.SplitVertical(c, &p.splitV, list, detail).SplitReset(96).Grow(1).MinHeight(0)
		return
	}
	if p.split == 0 {
		p.split = 180
	}
	ui.Split(c, &p.split, list, detail).SplitReset(180).Grow(1).MinHeight(0)
}

// Schema loading is explicit and transactional: errors keep the previous
// registry, and only compiled descriptors (not paths to source trees) persist.
func (p *protoInspector) loadSchema(a *app) {
	if p.loading {
		return
	}
	p.loading, p.problem = true, ""
	p.imports = make(chan protoImportResult, 1)
	answers, source, store := p.imports, strings.TrimSpace(p.source), p.store
	var roots []string
	for line := range strings.SplitSeq(p.roots, "\n") {
		if root := strings.TrimSpace(line); root != "" {
			roots = append(roots, root)
		}
	}
	go guarded(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		schema, data, err := importProtoSchema(ctx, source, roots)
		if err == nil && store != "" {
			err = os.MkdirAll(filepath.Dir(store), 0o700)
			if err == nil {
				var file *os.File
				file, err = os.CreateTemp(filepath.Dir(store), ".protobuf-*")
				if err == nil {
					defer os.Remove(file.Name())
					_, err = file.Write(data)
					err = errors.Join(err, file.Close())
					if err == nil {
						err = os.Rename(file.Name(), store)
					}
				}
			}
		}
		answers <- protoImportResult{schema: schema, err: err}
		a.invalidate()
	})
}

func (p *protoInspector) modal(c *ui.Context, a *app) {
	if p.loading {
		select {
		case result := <-p.imports:
			p.loading, p.imports = false, nil
			if result.err != nil {
				p.problem = result.err.Error()
			} else {
				p.stop()
				p.schema, p.name = result.schema, "Auto"
				a.done(locales.LoadedProtobufSchema())
			}
		default:
			c.After(50 * time.Millisecond)
		}
	}
	if !p.open {
		return
	}
	t := c.Theme()
	uikit.Modal(c, &p.open, locales.ProtobufSchema2(), func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, locales.ProtobufSchema2())
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(uikit.SpaceXL)).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
				uikit.Hint(c, locales.ImportAProtoSourceOr())
				if p.schema != nil {
					uikit.Meta(c, locales.ProtobufLoaded(len(p.schema.names)))
				}
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
					uikit.Input(c, &p.source).Label(locales.SchemaFile()).Placeholder("/path/to/service.proto or descriptors.pb").Grow(1).MinWidth(0)
					if uikit.SecondaryButton(c, locales.Browse()).Disabled(p.loading).Clicked() && a.win != nil {
						go guarded(func() {
							paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: locales.ImportProtobufSchema(), Filters: []mygo.FileFilter{{Name: "Protobuf", Extensions: []string{"proto", "pb", "protoset", "desc"}}}})
							if err == nil && len(paths) > 0 {
								a.win.Update(func() { p.source = paths[0] })
							}
						})
					}
				})
				uikit.Hint(c, locales.IncludeRootsOptionalOnePer())
				uikit.TextArea(c, &p.roots).Height(70).Font(uikit.FontMono).Radius(uikit.RadiusControl).Label(locales.ProtobufIncludeRoots())
				if p.problem != "" {
					uikit.Callout(c, uikit.CalloutDanger, p.problem, nil)
				}
				if p.loading {
					uikit.Meta(c, locales.CompilingAndLinkingSchema())
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.ClearSchema()).Disabled(p.schema == nil || p.loading).Clicked() {
					err := error(nil)
					if p.store != "" {
						err = os.Remove(p.store)
					}
					if err != nil && !errors.Is(err, os.ErrNotExist) {
						p.problem = err.Error()
					} else {
						p.stop()
						p.schema, p.name, p.problem = nil, "Auto", ""
					}
				}
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					p.open = false
				}
				if uikit.PrimaryButton(c, locales.LoadSchema2()).Disabled(p.loading || strings.TrimSpace(p.source) == "").Clicked() {
					p.loadSchema(a)
				}
			})
		})
	})
}
