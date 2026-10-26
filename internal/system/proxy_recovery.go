package system

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ProxySession keeps a pipe open to an independent recovery process. That
// process owns the OS write as well as rollback: killing the GUI midway through
// enabling cannot leave a write running after recovery has already finished.
// Its methods are serialized by the caller, just like the OS proxy operations.
type ProxySession struct {
	cmd     *exec.Cmd
	input   io.WriteCloser
	output  *jsontext.Decoder
	encoder *jsontext.Encoder
	path    string
	closed  bool
}

type proxyJournal struct {
	Version int
	Target  string
	Before  proxySnapshot
}

type proxyReply struct {
	Error string
	Owned bool
}

// BeginProxy starts the same executable in --proxy-guard mode. The main entry
// point must dispatch that mode to GuardProxy before starting the UI. The
// guardian persists the previous configuration before changing any OS setting.
// A non-nil session returned with an error still needs Restore: a helper may
// have failed after writing only part of the configuration.
func BeginProxy(path, target string) (*ProxySession, error) {
	if path == "" {
		return nil, errors.New("proxy recovery storage is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, "--proxy-guard", path, target)
	separateProxyGuard(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	logFile, err := os.OpenFile(path+".log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	defer logFile.Close()
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	s := &ProxySession{cmd: cmd, input: input, encoder: jsontext.NewEncoder(input), output: jsontext.NewDecoder(output), path: path}
	result, err := s.reply()
	if err != nil {
		return s, err
	}
	if !result.Owned {
		input.Close()
		cmd.Wait()
		return nil, errors.New(result.Error)
	}
	if result.Error != "" {
		return s, errors.New(result.Error)
	}
	return s, nil
}

func (s *ProxySession) reply() (proxyReply, error) {
	var result proxyReply
	if err := json.UnmarshalDecode(s.output, &result); err != nil {
		return result, fmt.Errorf("proxy recovery helper disconnected: %w", err)
	}
	return result, nil
}

// Restore waits for restoration, not merely for delivery of the request. If
// the guardian itself died, the durable journal is the fallback. Failed
// restoration keeps the guardian and journal available for a retry.
func (s *ProxySession) Restore() error {
	if s.closed {
		return RecoverProxy(s.path)
	}
	err := json.MarshalEncode(s.encoder, "restore")
	if err == nil {
		var result proxyReply
		result, err = s.reply()
		if err == nil && result.Error != "" {
			return errors.New(result.Error)
		}
	}
	s.input.Close()
	s.cmd.Wait()
	s.closed = true
	return RecoverProxy(s.path)
}

// GuardProxy is the non-UI entry point. EOF on input means the owner exited,
// including SIGKILL or a native crash; it does not depend on a GUI quit hook.
// A file lock prevents a second instance or startup recovery from racing it.
func GuardProxy(path, target string, input io.Reader, output io.Writer) error {
	proxyGuardSignals()
	encoder := jsontext.NewEncoder(output)
	owned := false
	reply := func(err error) error {
		r := proxyReply{Owned: owned}
		if err != nil {
			r.Error = err.Error()
		}
		return json.MarshalEncode(encoder, r)
	}
	lock, err := lockProxyJournal(path)
	if err != nil {
		reply(err)
		return err
	}
	defer lock.Close()
	if err := recoverProxy(path); err != nil {
		reply(err)
		return err
	}
	if host, _, err := net.SplitHostPort(target); err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		err := errors.New("recovery requires a loopback capture address")
		reply(err)
		return err
	}
	before, err := snapshotProxy(target)
	if err != nil {
		reply(err)
		return err
	}
	if err := saveProxyJournal(path, proxyJournal{Version: 1, Target: target, Before: before}); err != nil {
		reply(err)
		return err
	}
	owned = true
	// Both explicit restoration and EOF take this same path. Even a broken
	// reply pipe during enable must reach rollback.
	defer func() {
		for attempt := range 4 {
			if err := recoverProxy(path); err == nil {
				return
			} else {
				log.Printf("proxy recovery attempt %d: %v", attempt+1, err)
			}
			if attempt < 3 {
				time.Sleep(time.Duration(1<<attempt) * time.Second)
			}
		}
	}()
	if err := reply(before.applyProxy(target)); err != nil {
		return err
	}
	decoder := jsontext.NewDecoder(input)
	for {
		var command string
		if err := json.UnmarshalDecode(decoder, &command); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if command != "restore" {
			return errors.New("unknown proxy recovery command")
		}
		err := recoverProxy(path)
		if writeErr := reply(err); writeErr != nil {
			return writeErr
		}
		if err == nil {
			return nil
		}
	}
}

// RecoverProxy retries a journal left by a lost guardian. An active guardian
// holds the lock, so this never reverts another live instance's proxy.
func RecoverProxy(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lock, err := lockProxyJournal(path)
	if err != nil {
		return err
	}
	defer lock.Close()
	return recoverProxy(path)
}

func recoverProxy(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20))
	file.Close()
	if err != nil {
		return err
	}
	var journal proxyJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return fmt.Errorf("read proxy recovery journal: %w", err)
	}
	if journal.Version != 1 || journal.Target == "" {
		return errors.New("unsupported proxy recovery journal; manual recovery required")
	}
	if err := restoreProxy(journal.Before, journal.Target); err != nil {
		return err
	}
	return os.Remove(path)
}

func saveProxyJournal(path string, journal proxyJournal) error {
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".proxy-recovery-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
