package app

import (
	"cmp"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/grpcclient"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type grpcDocument struct {
	schema                     *protoSchema
	loaded                     bool
	source, roots, problem     string
	loading                    chan grpcSchemaResult
	cancelImport               context.CancelFunc
	session                    *grpcSession
	tab, responseTab, selected int
	reader                     *editor.State
	readerKey                  string
	split                      float32
	list                       ui.ListState
}

type grpcSchemaResult struct {
	schema *protoSchema
	data   []byte
	err    error
}

func (d *requestDocument) grpcState() *grpcDocument {
	if d.grpcUI == nil {
		d.grpcUI = &grpcDocument{split: 220}
	}
	return d.grpcUI
}

func (g *grpcDocument) stop() {
	if g.cancelImport != nil {
		g.cancelImport()
	}
	if g.session != nil {
		g.session.cancel()
	}
	g.loading = nil
}

func (a *app) drainGRPC() {
	for _, d := range a.documents {
		g := d.grpcUI
		if g == nil {
			continue
		}
		select {
		case result := <-g.loading:
			g.loading, g.cancelImport = nil, nil
			if result.err != nil {
				g.problem = result.err.Error()
			} else {
				g.schema, d.grpcConfig.Schema, g.problem = result.schema, result.data, ""
				if d.grpcConfig.Method == "" && len(g.schema.methods) > 0 {
					d.grpcConfig.Method = g.schema.methods[0]
				}
			}
		default:
		}
		if g.session != nil && g.session.snapshot().finished {
			d.composeWaiting, d.sendCancel, d.sendCancelling = false, nil, false
		}
	}
}

func (a *app) loadGRPCSchema(d *requestDocument, reflect, saved bool) {
	g := d.grpcState()
	if g.loading != nil || d.composeWaiting {
		return
	}
	var prepared *request.Prepared
	if reflect {
		r := d.value().Request
		r.Body = "" // Discovery sends metadata, never a request message.
		var err error
		prepared, err = r.PrepareGRPC(a.sendContext(r), a.activeVars())
		if err != nil {
			g.problem = err.Error()
			return
		}
	}
	var proxy string
	var tlsConfig *tls.Config
	if reflect {
		var configErr error
		proxy, tlsConfig, configErr = a.grpcTLSConfig(prepared.URL)
		if configErr != nil {
			g.problem = configErr.Error()
			return
		}
	}
	g.loaded, g.problem = true, ""
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	g.cancelImport = cancel
	answers := make(chan grpcSchemaResult, 1)
	g.loading = answers
	source, data := strings.TrimSpace(g.source), d.grpcConfig.Schema
	var roots []string
	for line := range strings.SplitSeq(g.roots, "\n") {
		if root := strings.TrimSpace(line); root != "" {
			roots = append(roots, root)
		}
	}
	go func() {
		defer cancel()
		var schema *protoSchema
		var err error
		switch {
		case reflect:
			conn, dialErr := grpcclient.DialWithTLS(prepared.URL, proxy, tlsConfig)
			err = dialErr
			if err == nil {
				defer conn.Close()
				md, mdErr := grpcMetadata(prepared.Header)
				err = mdErr
				if err == nil {
					data, err = grpcclient.Reflect(ctx, conn, md)
				}
			}
			if err == nil {
				schema, err = parseProtoSchema(data)
			}
		case saved:
			schema, err = parseProtoSchema(data)
		default:
			schema, data, err = importProtoSchema(ctx, source, roots)
		}
		if err == nil {
			err = ctx.Err()
		}
		answers <- grpcSchemaResult{schema: schema, data: data, err: err}
		a.invalidate()
	}()
}

func (a *app) grpcTLSConfig(endpoint string) (string, *tls.Config, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", nil, err
	}
	proxy := ""
	var proxyCA []byte
	if a.srv != nil && a.srv.Running() {
		proxy, proxyCA = a.srv.Addr(), a.srv.CACertificate()
	}
	if u.Scheme != "https" {
		return proxy, nil, nil
	}
	config, err := a.certificates.TLSConfig(certs.Address(u), proxyCA, false)
	if err != nil {
		return "", nil, err
	}
	return proxy, config, nil
}

func (a *app) sendGRPC(d *requestDocument) {
	g := d.grpcState()
	if g.loading != nil {
		g.problem = "Wait for schema discovery to finish"
		return
	}
	method, err := g.schema.method(d.grpcConfig.Method)
	if err != nil {
		g.problem = err.Error()
		return
	}
	r := d.value().Request
	in, vars := a.sendContext(r), a.activeVars()
	req, err := r.PrepareGRPC(in, vars)
	if err != nil {
		g.problem = err.Error()
		return
	}
	seconds := cmp.Or(d.grpcConfig.TimeoutSeconds, 30)
	if seconds < 1 || seconds > 3600 {
		g.problem = "Set a deadline between 1 and 3600 seconds"
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	proxy, tlsConfig, err := a.grpcTLSConfig(req.URL)
	if err != nil {
		cancel()
		g.problem = err.Error()
		return
	}
	g.session = openGRPCSession(ctx, cancel, r, in, vars, req, g.schema, method, proxy, tlsConfig, a.invalidate)
	g.problem, g.readerKey, g.reader, g.selected = "", "", nil, 0
	d.composeWaiting, d.sendCancel, d.sendCancelling = true, cancel, false
	d.historyPick, d.comparing = 0, false
}

func (a *app) grpcActions(d *requestDocument, editing bool) []action {
	ready := editing && d.protocol == "gRPC"
	var g *grpcDocument
	if ready {
		g = d.grpcState()
	}
	idle := ready && !d.composeWaiting && g.loading == nil
	canWrite := ready && g.session != nil
	if canWrite {
		s := g.session.snapshot()
		canWrite = !s.finished && !s.sending && !s.halfClosed
	}
	return []action{
		do("request.grpc-import", locales.ImportGrpcSchema(), func() { a.loadGRPCSchema(d, false, false) }).when(idle && g.source != "", "Enter a schema file in the gRPC Schema tab"),
		do("request.grpc-reflect", locales.ReflectGrpcServices(), func() { a.loadGRPCSchema(d, true, false) }).when(idle, "Open an idle gRPC document"),
		do("request.grpc-cancel-discovery", locales.CancelGrpcDiscovery(), func() { g.cancelImport() }).when(ready && g.loading != nil, ""),
		do("request.grpc-send-message", locales.SendGrpcMessage(), func() {
			r := g.session.input
			r.Body = d.composeBody
			req, err := r.PrepareGRPC(g.session.scope, g.session.vars)
			if err != nil {
				g.problem = err.Error()
				return
			}
			g.problem = ""
			g.session.write(req.Body, false)
		}).when(canWrite, "Start a client or bidirectional stream first"),
		do("request.grpc-finish", locales.FinishGrpcSending(), func() { g.session.write("", true) }).when(canWrite, "Start a client or bidirectional stream first"),
	}
}

func (a *app) grpcEditor(c *ui.Context, t *ui.Theme, d *requestDocument) {
	g := d.grpcState()
	if !g.loaded {
		g.loaded = true
		if len(d.grpcConfig.Schema) > 0 {
			a.loadGRPCSchema(d, false, true)
		}
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).Children(func() {
			uikit.Tabs(c, &g.tab, locales.GrpcEditorTabPrefix(), locales.Message(), locales.Metadata(), locales.Auth(), locales.Schema2()).Grow(1)
		})
		ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
			var methods []string
			if g.schema != nil {
				methods = g.schema.methods
			}
			field := ui.Combobox(c, &d.grpcConfig.Method, methods).Label(locales.GrpcMethod()).Placeholder(locales.SelectServiceMethod()).
				Grow(1).MinWidth(0).Height(uikit.HeightControl).FontSize(uikit.Scaled(uikit.TextBody)).Disabled(d.composeWaiting || g.loading != nil)
			uikit.FieldFace(c, field, field.Focused())
			deadline := strconv.Itoa(cmp.Or(d.grpcConfig.TimeoutSeconds, 30))
			uikit.Select(c, &deadline, []string{"10", "30", "60", "300", "3600"}).Label(locales.GrpcDeadlineSeconds()).Width(88).
				Tooltip(locales.CallDeadlineInSecondsCancelling()).Disabled(d.composeWaiting).OnChange(func() {
				d.grpcConfig.TimeoutSeconds, _ = strconv.Atoi(deadline)
			})
		})
		if method, err := g.schema.method(d.grpcConfig.Method); err == nil {
			kind := grpcMethodKind(method)
			uikit.Meta(c, kind+" · "+string(method.Input().FullName())+" → "+string(method.Output().FullName())).
				Padding(0, t.Space(3)).SingleLine()
		}
		if g.problem != "" {
			ui.Text(c, g.problem).TextColor(t.Danger).FontSize(uikit.Scaled(uikit.TextMeta)).Padding(0, t.Space(3)).MaxHeight(55)
		}
		if g.loading != nil {
			ui.Row(c).Padding(0, t.Space(3)).Gap(t.Space(2)).Children(func() {
				uikit.Meta(c, locales.LoadingSchema()).Grow(1)
				a.actionButton(c, "request.grpc-cancel-discovery", "Cancel discovery")
			})
		}
		ui.Column(c).Grow(1).MinHeight(0).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
			switch g.tab {
			case 0:
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					uikit.Meta(c, locales.ProtobufJson1MibPer()).Grow(1)
					if d.composeWaiting {
						a.actionButton(c, "request.grpc-send-message", "Send message")
						a.actionButton(c, "request.grpc-finish", "Finish sending")
					}
				})
				authoredText(c, &d.composeBody, &d.bodyEditor, &d.bodyFor, "gRPC request JSON", "json")
			case 1:
				ui.Column(c).Fill().Disabled(d.composeWaiting).Children(func() { a.headerEditor(c, d, t) })
			case 2:
				ui.Column(c).Fill().Disabled(d.composeWaiting).Children(func() { a.authEditor(c, d, t) })
			case 3:
				ui.Scroll(c).Grow(1).Gap(t.Space(2)).Disabled(d.composeWaiting).Padding(0, t.Space(1)).Children(func() {
					uikit.Hint(c, locales.ImportProtoDescriptorSetOr())
					uikit.Input(c, &g.source).Label(locales.GrpcSchemaFile()).Placeholder("/path/to/service.proto").Disabled(g.loading != nil)
					ui.TextArea(c, &g.roots).Label(locales.GrpcIncludeRoots()).Placeholder(locales.IncludeRootsOnePerLine()).Height(50).Disabled(g.loading != nil)
					ui.Row(c).Gap(t.Space(2)).Children(func() {
						a.actionButton(c, "request.grpc-import", "Import schema")
						a.actionButton(c, "request.grpc-reflect", "Reflect services")
					})
					if g.schema != nil {
						n := len(g.schema.methods)
						uikit.Meta(c, locales.GrpcMethods(n))
					}
				})
			}
		})
		transport := "TLS verifies certificates; HTTPS uses capture while the proxy is running."
		if strings.HasPrefix(d.composeURL, "http://") {
			transport = "Plaintext HTTP/2 · direct connection, not captured (h2c)."
		}
		uikit.Tip(c, uikit.Meta(c, transport).SingleLine(), transport).Padding(0, t.Space(3), t.Space(1), t.Space(3))
	})
}

func grpcMethodKind(m protoreflect.MethodDescriptor) string {
	switch {
	case m.IsStreamingClient() && m.IsStreamingServer():
		return "Bidirectional stream"
	case m.IsStreamingClient():
		return "Client stream"
	case m.IsStreamingServer():
		return "Server stream"
	default:
		return "Unary"
	}
}

func grpcMetadataText(md metadata.MD) string {
	var b strings.Builder
	for _, key := range slices.Sorted(maps.Keys(md)) {
		for _, value := range md[key] {
			if strings.HasSuffix(key, "-bin") {
				value = base64.StdEncoding.EncodeToString([]byte(value))
			}
			fmt.Fprintf(&b, "%s: %s\n", key, value)
		}
	}
	return b.String()
}

func (a *app) grpcResponse(c *ui.Context, t *ui.Theme, d *requestDocument) {
	g := d.grpcState()
	s := g.session.snapshot()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).Children(func() {
			uikit.Tabs(c, &g.responseTab, "gRPC response", locales.MessagesTab(), locales.Metadata(), locales.Status()).Grow(1)
			uikit.Meta(c, stateLabel(s.state)).SingleLine()
		})
		if g.session == nil {
			uikit.EmptyState(c, locales.NoGrpcCallYet(), locales.LoadASchemaSelectA(), nil)
			return
		}
		if g.responseTab != 0 {
			text := stateLabel(s.state) + "\n" + s.problem + "\n\n" + s.details
			if g.responseTab == 1 {
				text = "Initial metadata\n" + grpcMetadataText(s.header) + "\nFinal trailers\n" + grpcMetadataText(s.trailer)
			}
			ui.Scroll(c).Grow(1).Padding(t.Space(3)).Children(func() { ui.Text(c, text).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)) })
			return
		}
		if s.problem != "" {
			ui.Text(c, s.problem).TextColor(t.Danger).FontSize(uikit.Scaled(uikit.TextMeta)).MaxHeight(55).Padding(t.Space(1), t.Space(3))
		}
		uikit.Meta(c, locales.RetainedPreviewMethods(len(s.events), s.evicted)).Padding(t.Space(1), t.Space(3))
		ui.Split(c, &g.split, func() {
			g.list.Selected = &g.selected
			g.list.Key = func(i int) any { return s.events[i].seq }
			g.list.Label = func(i int) string { return fmt.Sprintf("gRPC message %d", s.events[i].seq) }
			focused := g.list.Handle.Focused(c)
			c.SetTheme(uikit.ListTheme(t, focused))
			ui.List(c, &g.list, len(s.events), func(i int) {
				e := s.events[i]
				if i == g.selected {
					uikit.RowMarker(c, focused)
				}
				ui.Text(c, locales.GrpcEventLine(e.seq, e.at.Format("15:04:05.000"), e.direction, e.size)).
					FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Text).Padding(t.Space(2)).SingleLine()
			}).Grow(1)
			c.SetTheme(t)
		}, func() {
			if len(s.events) == 0 {
				uikit.Hint(c, locales.WaitingForMessages()).Padding(t.Space(3))
				return
			}
			// The list maps a selected key it no longer has to -1: the event
			// went away with the ring, and the detail says so rather than
			// jumping to the oldest one left.
			settleEvent(&g.selected, len(s.events))
			e := s.events[g.selected]
			key := fmt.Sprint(e.seq)
			if g.readerKey != key {
				text := e.text
				if len(text) < e.size {
					text += fmt.Sprintf("\n\nRetained %d of %d bytes", len(text), e.size)
				}
				g.readerKey = key
				g.reader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.GrpcMessagePayload()}
			}
			ui.Column(c).Fill().Children(func() {
				ui.Row(c).Padding(t.Space(1), t.Space(2)).Children(func() {
					uikit.Meta(c, e.direction+" · protobuf JSON").Grow(1)
					uikit.SecondaryButton(c, locales.CopyShownMessage()).OnClick(func() { c.Services().WriteClipboard(g.reader.Buffer.String()) })
				})
				requestCodeEditor(c, g.reader)
			})
		}).Grow(1)
	})
}
