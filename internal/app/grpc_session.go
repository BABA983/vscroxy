package app

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/grpcclient"
	"github.com/baba983/vscroxy/internal/request"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type grpcEvent struct {
	seq       int
	at        time.Time
	direction string
	text      string
	size      int
}

// Only this bounded session crosses threads. UI elements and documents never
// do. Replacing a session leaves its late updates on the old object.
type grpcSession struct {
	mu                      sync.Mutex
	call                    *grpcclient.Call
	cancel                  context.CancelFunc
	done                    chan struct{}
	notify                  func()
	finished                bool
	sending                 bool
	halfClosed              bool
	state, problem, details string
	events                  []grpcEvent
	seq, evicted            int
	header, trailer         metadata.MD
	// Inputs are immutable snapshots. Later messages use the original host,
	// auth and variable scopes, not another environment selected mid-call.
	input request.Request
	scope request.Context
	vars  map[string]string
}

type grpcSnapshot struct {
	state, problem, details       string
	finished, sending, halfClosed bool
	events                        []grpcEvent
	evicted                       int
	header, trailer               metadata.MD
}

func (s *grpcSession) snapshot() grpcSnapshot {
	if s == nil {
		return grpcSnapshot{state: "Not started", finished: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return grpcSnapshot{state: s.state, problem: s.problem, details: s.details, finished: s.finished, sending: s.sending, halfClosed: s.halfClosed,
		events: slices.Clone(s.events), evicted: s.evicted, header: s.header.Copy(), trailer: s.trailer.Copy()}
}

func grpcMetadata(header http.Header) (metadata.MD, error) {
	md := metadata.MD{}
	for key, values := range header {
		key = strings.ToLower(key)
		for _, value := range values {
			if strings.HasSuffix(key, "-bin") {
				data, err := base64.StdEncoding.DecodeString(value)
				if err != nil {
					return nil, fmt.Errorf("metadata %s requires padded base64", key)
				}
				value = string(data)
			}
			md.Append(key, value)
		}
	}
	return md, nil
}

func openGRPCSession(ctx context.Context, cancel context.CancelFunc, input request.Request, scope request.Context, vars map[string]string,
	req *request.Prepared, schema *protoSchema, method protoreflect.MethodDescriptor, proxy string, tlsConfig *tls.Config, notify func()) *grpcSession {
	s := &grpcSession{cancel: cancel, done: make(chan struct{}), notify: notify, state: "Connecting", sending: true,
		input: input, scope: scope, vars: maps.Clone(vars)}
	go func() {
		defer close(s.done)
		defer cancel()
		var final error
		defer func() {
			s.mu.Lock()
			s.finished, s.sending = true, false
			s.state = "gRPC OK"
			if final != nil {
				s.state = "gRPC " + status.Code(final).String()
				s.problem = final.Error()
				st := status.Convert(final).Proto()
				data, err := (protojson.MarshalOptions{Multiline: true, Resolver: schema.types}).Marshal(st)
				s.details = string(data)
				if err != nil {
					s.details = st.String() // Unknown Any types remain visible as wire bytes.
				}
			}
			s.mu.Unlock()
			s.changed()
		}()
		conn, err := grpcclient.DialWithTLS(req.URL, proxy, tlsConfig)
		if err != nil {
			final = err
			return
		}
		defer conn.Close()
		md, err := grpcMetadata(req.Header)
		if err != nil {
			final = err
			return
		}
		call, err := grpcclient.Start(ctx, conn, method, schema.types, md)
		if err != nil {
			final = err
			return
		}
		defer func() {
			cancel() // Also unblocks Header after a local JSON/send failure.
			header, _ := call.Header()
			s.mu.Lock()
			s.header, s.trailer = header, call.Trailer()
			s.mu.Unlock()
		}()
		s.mu.Lock()
		s.call, s.state = call, "Calling"
		s.mu.Unlock()
		if err := call.Send([]byte(req.Body)); err != nil {
			if errors.Is(err, io.EOF) {
				_, err = call.Recv() // Read the peer's actual final status.
			}
			final = err
			return
		}
		s.add("Out", req.Body)
		if !method.IsStreamingClient() {
			if err := call.CloseSend(); err != nil {
				final = err
				return
			}
		}
		s.mu.Lock()
		s.sending, s.halfClosed = false, !method.IsStreamingClient()
		s.mu.Unlock()
		s.changed()
		for {
			data, err := call.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					final = err
				}
				return
			}
			header, _ := call.Header()
			s.mu.Lock()
			s.header = header
			s.mu.Unlock()
			s.add("In", string(data))
		}
	}()
	return s
}

func (s *grpcSession) changed() {
	if s.notify != nil {
		s.notify()
	}
}

func (s *grpcSession) add(direction, text string) {
	s.mu.Lock()
	s.seq++
	size := len(text)
	text = text[:min(size, 64<<10)]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	if len(text) < size {
		text = strings.Clone(text) // Do not retain the full message's backing storage.
	}
	if len(s.events) == 200 {
		s.events = slices.Delete(s.events, 0, 1)
		s.evicted++
	}
	s.events = append(s.events, grpcEvent{seq: s.seq, at: time.Now(), direction: direction, text: text, size: size})
	s.mu.Unlock()
	s.changed()
}

// write is off-thread; SendMsg may wait for flow control. The UI disables a
// second write until completion, and Cancel interrupts the blocked write.
func (s *grpcSession) write(body string, halfClose bool) {
	s.mu.Lock()
	if s.finished || s.sending || s.halfClosed || s.call == nil {
		s.mu.Unlock()
		return
	}
	s.sending, s.problem = true, ""
	call := s.call
	s.mu.Unlock()
	go func() {
		var err error
		if halfClose {
			err = call.CloseSend()
		} else {
			err = call.Send([]byte(body))
			if err == nil {
				s.add("Out", body)
			}
		}
		s.mu.Lock()
		s.sending = false
		if !s.finished {
			if err != nil {
				s.problem = err.Error()
			} else if halfClose {
				s.halfClosed = true
			}
		}
		s.mu.Unlock()
		s.changed()
	}()
}
