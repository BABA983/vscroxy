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
	list     ui.ListState
	split    float32

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
		p.problem = "Saved protobuf schema: " + err.Error()
	}
}

func (p *protoInspector) stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.cancel, p.answers = nil, nil
	p.key = ""
}

func (p *protoInspector) inspect(c *ui.Context, a *app, f flow, response bool) {
	key := fmt.Sprintf("%d:%t:%s:%p", f.id, response, p.name, p.schema)
	if key != p.key {
		p.stop()
		p.key, p.selected, p.query = key, 0, ""
		p.result = protoResult{}
		ctx, cancel := context.WithCancel(context.Background())
		answers := make(chan protoResult, 1)
		p.cancel, p.answers = cancel, answers
		schema, name := p.schema, p.name
		go func() {
			result := inspectProto(ctx, f, response, schema, name)
			select {
			case answers <- result:
				a.invalidate()
			case <-ctx.Done():
			}
		}()
	}
	if p.answers != nil {
		select {
		case p.result = <-p.answers:
			p.answers = nil
		default:
			c.After(50 * time.Millisecond)
		}
	}
}

func (p *protoInspector) view(c *ui.Context, a *app, f flow, side int) {
	t := c.Theme()
	response := side == 0
	// Wraps in a narrow inspector rather than pushing its controls out.
	ui.Row(c).Padding(0, t.Space(3), t.Space(2), t.Space(3)).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
		options := []string{"Auto"}
		if p.schema != nil {
			options = append(options, p.schema.names...)
		}
		if p.name == "" {
			p.name = "Auto"
		}
		ui.Combobox(c, &p.name, options).Label("Protobuf message type").Width(280)
		if uikit.SecondaryButton(c, "Schema…").Clicked() {
			p.open = true
		}
		ui.SearchField(c, &p.query).Label("Search protobuf messages").Placeholder("Search messages…").Width(200)
	})
	p.inspect(c, a, f, response)
	if grpcContentType(f.contentType(response)) {
		uikit.Meta(c, grpcStatus(f)).Padding(0, t.Space(3), t.Space(2), t.Space(3))
	}
	if p.answers != nil {
		uikit.Hint(c, "Reading complete retained body…").Padding(t.Space(3))
		return
	}
	if p.result.problem != "" {
		ui.Text(c, p.result.problem).FontSize(uikit.TextMeta).TextColor(t.Danger).Padding(0, t.Space(3), t.Space(2), t.Space(3))
	}
	note := "Schema-less: field numbers, not names. Load a schema to resolve types."
	if p.result.typeName != "" {
		note = "Message type: " + p.result.typeName
	}
	uikit.Meta(c, note).Padding(0, t.Space(3), t.Space(2), t.Space(3))
	var visible []int
	for i, message := range p.result.messages {
		if p.query == "" || strings.Contains(strings.ToLower(message.text), strings.ToLower(p.query)) {
			visible = append(visible, i)
		}
	}
	uikit.Meta(c, fmt.Sprintf("%d of %d messages", len(visible), len(p.result.messages))).Padding(0, t.Space(3), t.Space(2), t.Space(3))
	if len(visible) == 0 {
		note := "No message matches"
		if len(p.result.messages) == 0 && p.result.problem == "" {
			note = "No messages in this body (a trailers-only gRPC response is valid)"
		}
		uikit.Hint(c, note).Padding(t.Space(3))
		return
	}
	if p.split == 0 {
		p.split = 180
	}
	p.selected = max(0, min(p.selected, len(visible)-1))
	ui.Split(c, &p.split, func() {
		p.list.Selected = &p.selected
		p.list.Key = func(i int) any { return visible[i] }
		p.list.Label = func(i int) string { return fmt.Sprintf("Message %d", visible[i]+1) }
		focused := p.list.Handle.Focused(c)
		c.SetTheme(uikit.ListTheme(t, focused))
		ui.List(c, &p.list, len(visible), func(i int) {
			if i == p.selected {
				uikit.RowMarker(c, focused)
			}
			message := p.result.messages[visible[i]]
			note := fmt.Sprintf("#%d · %s", visible[i]+1, humanBytes(message.size))
			if message.compressed {
				note += " · gzip"
			}
			ui.Text(c, note).FontSize(uikit.TextMeta).TextColor(t.Text).Padding(t.Space(2), t.Space(3)).SingleLine()
		}).Grow(1)
		c.SetTheme(t)
	}, func() {
		// List may remap a selected key to -1 when a search removes it.
		p.selected = max(0, min(p.selected, len(visible)-1))
		message := p.result.messages[visible[p.selected]]
		ui.Column(c).Fill().Children(func() {
			ui.Row(c).Padding(0, t.Space(3)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, fmt.Sprintf("Message %d · %s decoded", visible[p.selected]+1, humanBytes(message.size))).Grow(1)
				if uikit.SecondaryButton(c, "Copy decoded").Clicked() {
					c.Services().WriteClipboard(message.text)
					a.setMessage("Copied decoded protobuf message")
				}
			})
			text := message.text
			if len(text) > maxMessageText {
				text = text[:maxMessageText] + "\n\n… Text preview limited to 64 KiB; Copy decoded keeps the full text."
			}
			ui.Scroll(c).Grow(1).Padding(t.Space(3)).Children(func() {
				ui.Text(c, text).Font(uikit.FontMono).FontSize(uikit.TextBody).TextColor(t.Text)
			})
		})
	}).Grow(1)
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
	go func() {
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
	}()
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
				a.setMessage("Loaded protobuf schema")
			}
		default:
			c.After(50 * time.Millisecond)
		}
	}
	if !p.open {
		return
	}
	t := c.Theme()
	uikit.Modal(c, &p.open, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, "Protobuf schema")
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(4)).Children(func() {
				uikit.Hint(c, "Import a .proto source or a binary FileDescriptorSet (.pb / .protoset). gRPC methods select input/output types automatically; raw protobuf needs a message type. A successful load replaces the current registry.")
				if p.schema != nil {
					uikit.Meta(c, fmt.Sprintf("Loaded: %d message types", len(p.schema.names)))
				}
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					uikit.Input(c, &p.source).Label("Schema file").Placeholder("/path/to/service.proto or descriptors.pb").Grow(1).MinWidth(0)
					if uikit.SecondaryButton(c, "Browse…").Disabled(p.loading).Clicked() && a.win != nil {
						go func() {
							paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Import protobuf schema", Filters: []mygo.FileFilter{{Name: "Protobuf", Extensions: []string{"proto", "pb", "protoset", "desc"}}}})
							if err == nil && len(paths) > 0 {
								a.win.Update(func() { p.source = paths[0] })
							}
						}()
					}
				})
				uikit.Hint(c, "Include roots (optional, one per line). The source directory and standard Google imports are included automatically.")
				ui.TextArea(c, &p.roots).Height(70).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Label("Protobuf include roots")
				if p.problem != "" {
					ui.Text(c, p.problem).FontSize(uikit.TextBody).TextColor(t.Danger)
				}
				if p.loading {
					uikit.Meta(c, "Compiling and linking schema…")
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Clear schema").Disabled(p.schema == nil || p.loading).Clicked() {
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
				if uikit.SecondaryButton(c, "Close").Clicked() {
					p.open = false
				}
				if uikit.PrimaryButton(c, "Load schema").Disabled(p.loading || strings.TrimSpace(p.source) == "").Clicked() {
					p.loadSchema(a)
				}
			})
		})
	})
}
