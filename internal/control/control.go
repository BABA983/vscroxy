// Package control provides an authenticated, local-only desktop control endpoint.
package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxRequestBytes  = 64 << 10
	maxResponseBytes = 16 << 20
	dispatchTimeout  = 10 * time.Second
)

// Command is an action and its object-valued arguments.
type Command struct {
	Action string         `json:"action"`
	Params jsontext.Value `json:"params"`
}

// Dispatch executes a command. Implementations must honor context cancellation;
// Go cannot forcibly stop a dispatch function that ignores its context.
type Dispatch func(context.Context, Command) (any, error)

type discovery struct {
	Version int    `json:"version"`
	Address string `json:"address"`
	Token   string `json:"token"`
}

// Server owns a listening endpoint and its discovery file.
type Server struct {
	http      *http.Server
	cancel    context.CancelFunc
	done      chan struct{}
	file      *os.File
	path      string
	closeOnce sync.Once
	closeErr  error
}

// Start listens on 127.0.0.1:0 and atomically publishes a private discovery file.
// An existing path (including a dangling symlink) is never replaced. The parent
// directory must already exist and should be private to the current user.
func Start(discoveryPath string, dispatch Dispatch) (*Server, error) {
	if dispatch == nil {
		return nil, errors.New("control: dispatch is required")
	}
	path, err := filepath.Abs(discoveryPath)
	if err != nil {
		return nil, errors.New("control: invalid discovery path")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("control: cannot listen on loopback")
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		listener.Close()
		return nil, errors.New("control: cannot generate authentication token")
	}
	d := discovery{Version: 1, Address: listener.Addr().String(), Token: hex.EncodeToString(token)}
	data, err := json.Marshal(d)
	if err != nil {
		listener.Close()
		return nil, errors.New("control: cannot encode discovery")
	}
	// Publish a fully written file using a hard link: unlike Rename, Link cannot
	// replace another process's endpoint, even if both processes start together.
	// Protection is installed at creation, before any token bytes are written.
	f, err := createPrivateTemp(filepath.Dir(path))
	if err != nil {
		listener.Close()
		return nil, errors.New("control: cannot create private discovery file")
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		listener.Close()
		return nil, errors.New("control: cannot write discovery file")
	}
	if err := f.Sync(); err != nil {
		f.Close()
		listener.Close()
		return nil, errors.New("control: cannot sync discovery file")
	}
	if err := os.Link(f.Name(), path); err != nil {
		f.Close()
		listener.Close()
		return nil, errors.New("control: discovery path already exists or cannot be published")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cancel: cancel, done: make(chan struct{}), file: f, path: path}
	s.http = &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveCall(w, r, d, dispatch) }),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       15 * time.Second,
		MaxHeaderBytes:    8 << 10,
		// Never let net/http log arbitrary panic values or request information.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() {
		defer close(s.done)
		_ = s.http.Serve(listener)
	}()
	return s, nil
}

// Close cancels in-flight dispatch contexts, closes all connections, and removes
// the discovery path only if it still names this server's file. It is idempotent.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		if err := s.http.Close(); err != nil {
			s.closeErr = errors.New("control: cannot close server")
		}
		<-s.done
		own, err := s.file.Stat()
		current, pathErr := os.Lstat(s.path)
		if err == nil && pathErr == nil && os.SameFile(own, current) {
			if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.closeErr = errors.New("control: cannot remove discovery file")
			}
		} else if err != nil || (pathErr != nil && !errors.Is(pathErr, os.ErrNotExist)) {
			s.closeErr = errors.New("control: cannot inspect discovery file")
		}
		if err := s.file.Close(); err != nil {
			s.closeErr = errors.New("control: cannot close discovery file")
		}
	})
	return s.closeErr
}

func serveCall(w http.ResponseWriter, r *http.Request, d discovery, dispatch Dispatch) {
	defer func() {
		if recover() != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
		}
	}()
	w.Header().Set("Cache-Control", "no-store")
	if r.Host != d.Address || len(r.Header.Values("Origin")) != 0 {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+d.Token)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.URL.Path != "/v1/call" || r.URL.RawQuery != "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "request too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid request")
		}
		return
	}
	var command Command
	if err := json.Unmarshal(body, &command, json.RejectUnknownMembers(true)); err != nil || !validCommand(command) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dispatchTimeout)
	defer cancel()
	type outcome struct {
		value any
		err   error
	}
	finished := make(chan outcome, 1)
	go func() {
		result := outcome{}
		defer func() {
			if recover() != nil {
				result = outcome{err: errors.New("dispatch panic")}
			}
			finished <- result
		}()
		result.value, result.err = dispatch(ctx, command)
	}()
	select {
	case <-ctx.Done():
		writeError(w, http.StatusGatewayTimeout, "dispatch canceled or timed out")
	case result := <-finished:
		if result.err != nil {
			writeError(w, http.StatusInternalServerError, "dispatch failed")
			return
		}
		data, err := json.Marshal(result.value)
		if err != nil || len(data) > maxResponseBytes {
			writeError(w, http.StatusInternalServerError, "invalid dispatch result")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
}

func validCommand(c Command) bool {
	if len(c.Action) == 0 || len(c.Action) > 128 || c.Params.Kind() != '{' {
		return false
	}
	for _, ch := range c.Action {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, struct {
		Error string `json:"error"`
	}{Error: message})
}

// Client is a concurrency-safe client for one discovered endpoint.
type Client struct {
	address string
	token   string
	http    *http.Client
}

// Load reads a private regular discovery file owned by the current user.
// Symlinks, non-loopback addresses, and unsupported versions are rejected.
func Load(discoveryPath string) (*Client, error) {
	info, err := os.Lstat(discoveryPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("control: discovery must be a private regular file")
	}
	f, err := os.Open(discoveryPath)
	if err != nil {
		return nil, errors.New("control: cannot open discovery file")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !privateFile(f, opened) {
		return nil, errors.New("control: discovery file changed or is not private")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return nil, errors.New("control: cannot read discovery file")
	}
	var d discovery
	if err := json.Unmarshal(data, &d, json.RejectUnknownMembers(true)); err != nil || d.Version != 1 {
		return nil, errors.New("control: invalid discovery version or contents")
	}
	address, err := netip.ParseAddrPort(d.Address)
	if err != nil || !address.Addr().IsLoopback() || address.Addr().Zone() != "" || address.Port() == 0 || address.String() != d.Address {
		return nil, errors.New("control: discovery address must be numeric loopback with a port")
	}
	token, err := hex.DecodeString(d.Token)
	if err != nil || len(token) != 32 {
		return nil, errors.New("control: invalid discovery authentication")
	}
	return &Client{
		address: d.Address,
		token:   d.Token,
		http: &http.Client{
			Transport: &http.Transport{
				Proxy:                  nil,
				DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				ResponseHeaderTimeout:  12 * time.Second,
				MaxResponseHeaderBytes: 16 << 10,
				IdleConnTimeout:        30 * time.Second,
				DisableCompression:     true,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Timeout:       15 * time.Second,
		},
	}, nil
}

// Call invokes an action. Nil params means {}; otherwise params must encode as
// an object. A nil result discards a successful response. Backend error details
// are deliberately not exposed; failures use fixed, credential-free messages.
func (c *Client) Call(ctx context.Context, action string, params any, result any) error {
	if params == nil {
		params = map[string]any{}
	}
	arguments, err := json.Marshal(params)
	if err != nil {
		return errors.New("control: cannot encode parameters")
	}
	command := Command{Action: action, Params: arguments}
	if !validCommand(command) {
		return errors.New("control: invalid action or parameters")
	}
	body, err := json.Marshal(command)
	if err != nil || len(body) > maxRequestBytes {
		return errors.New("control: request too large or invalid")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.address+"/v1/call", bytes.NewReader(body))
	if err != nil {
		return errors.New("control: cannot create request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("control: request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("control: response unreadable or too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &failure, json.RejectUnknownMembers(true)) == nil {
			// Only known protocol messages may enter an error; never echo an
			// arbitrary response that might contain the bearer credential.
			switch failure.Error {
			case "forbidden", "unauthorized", "not found", "method not allowed", "request too large", "invalid request", "internal error", "dispatch failed", "dispatch canceled or timed out", "invalid dispatch result":
				return errors.New("control: " + failure.Error)
			}
		}
		return errors.New("control: endpoint returned an error")
	}
	if result == nil {
		if !jsontext.Value(data).IsValid() {
			return errors.New("control: invalid response")
		}
		return nil
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("control: cannot decode response")
	}
	return nil
}
