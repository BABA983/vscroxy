package capture

// The SOCKS5 entry: RFC 1928 CONNECT with no authentication or RFC 1929
// username/password, for clients that cannot use an HTTP proxy. A connection
// is negotiated here and then handed to the same engine as an HTTP CONNECT:
// TLS goes through the existing per-host and per-app decryption policy, and a
// plaintext HTTP request is parsed and captured as usual. No request content
// is logged.

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	socksVersion  = 0x05
	socksAuthNone = 0x00
	socksAuthUser = 0x02
	socksNoMethod = 0xff
	socksConnect  = 0x01

	socksIPv4   = 0x01
	socksDomain = 0x03
	socksIPv6   = 0x04

	// socksHandshakeTimeout bounds a client that connects and then stalls in
	// the greeting or the request.
	socksHandshakeTimeout = 10 * time.Second
)

// SOCKSConfig is the SOCKS5 entry. Addr is a loopback address; an empty
// address or port 0 asks the OS for one. Username turns on RFC 1929
// authentication; both fields must fit the protocol's one-byte lengths.
type SOCKSConfig struct {
	Enabled  bool
	Addr     string
	Username string
	Password string
}

// SetSOCKS stores the SOCKS5 configuration. It must be called before Start;
// the listener is bound by Start, so a fixed port that is taken leaves the
// HTTP proxy running and reports the error instead of falling back.
func (s *Server) SetSOCKS(c SOCKSConfig) error {
	if c.Addr == "" {
		c.Addr = "127.0.0.1:0"
	}
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return fmt.Errorf("SOCKS5 address %q: %w", c.Addr, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Unmap().IsLoopback() {
		return fmt.Errorf("SOCKS5 address %q must be a loopback IP", c.Addr)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 0 || p > 65535 {
		return fmt.Errorf("SOCKS5 port %q must be 0 to 65535", port)
	}
	if len(c.Username) > 255 || len(c.Password) > 255 {
		return errors.New("SOCKS5 username and password must each be at most 255 bytes")
	}
	if c.Username == "" && c.Password != "" {
		return errors.New("SOCKS5 password needs a username")
	}
	if c.Username != "" && c.Password == "" {
		return errors.New("SOCKS5 authentication needs a password")
	}
	s.socksMu.Lock()
	defer s.socksMu.Unlock()
	if s.running.Load() {
		return errors.New("SOCKS5 settings apply after a restart")
	}
	s.socksCfg = c
	return nil
}

// SOCKSAddr is the bound SOCKS5 address, or empty when it is off or failed.
func (s *Server) SOCKSAddr() string {
	s.socksMu.Lock()
	defer s.socksMu.Unlock()
	return s.socksAddr
}

// SOCKSError says why the SOCKS5 listener is not running, if it should be.
func (s *Server) SOCKSError() error {
	s.socksMu.Lock()
	defer s.socksMu.Unlock()
	return s.socksErr
}

func (s *Server) startSOCKS() {
	s.socksMu.Lock()
	cfg := s.socksCfg
	s.socksMu.Unlock()
	if !cfg.Enabled {
		return
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		s.socksMu.Lock()
		s.socksErr = fmt.Errorf("SOCKS5 on %s: %w", cfg.Addr, err)
		s.socksMu.Unlock()
		return
	}
	s.socksMu.Lock()
	s.socksLn, s.socksAddr, s.socksErr = ln, ln.Addr().String(), nil
	s.socksConns = make(map[net.Conn]struct{})
	s.socksMu.Unlock()

	s.socksWG.Add(1)
	go func() {
		defer s.socksWG.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.socksMu.Lock()
			if s.socksLn != ln {
				s.socksMu.Unlock()
				_ = conn.Close()
				return
			}
			s.socksConns[conn] = struct{}{}
			s.socksMu.Unlock()
			s.socksWG.Add(1)
			go func() {
				defer s.socksWG.Done()
				s.serveSOCKS(conn, cfg)
				s.socksMu.Lock()
				delete(s.socksConns, conn)
				s.socksMu.Unlock()
			}()
		}
	}()
}

func (s *Server) serveSOCKS(conn net.Conn, cfg SOCKSConfig) {
	_ = conn.SetDeadline(time.Now().Add(socksHandshakeTimeout))
	target, err := acceptSOCKS(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if err := s.proxy.ServeSOCKSConn(conn, target, s.closed); err != nil {
		_ = conn.Close()
	}
}

func (s *Server) stopSOCKS() {
	s.socksMu.Lock()
	ln := s.socksLn
	conns := make([]net.Conn, 0, len(s.socksConns))
	for conn := range s.socksConns {
		conns = append(conns, conn)
	}
	s.socksLn, s.socksAddr, s.socksConns = nil, "", nil
	s.socksMu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
	s.socksWG.Wait()
}

// acceptSOCKS runs the greeting, optional authentication and CONNECT request.
// It replies to every request it understands, and returns the destination as
// host:port. Reads are exact, so no byte of the tunnel is consumed early.
func acceptSOCKS(c net.Conn, cfg SOCKSConfig) (string, error) {
	var greeting [2]byte
	if _, err := io.ReadFull(c, greeting[:]); err != nil {
		return "", err
	}
	if greeting[0] != socksVersion {
		return "", fmt.Errorf("SOCKS version %d is not supported", greeting[0])
	}
	methods := make([]byte, int(greeting[1]))
	if len(methods) == 0 {
		return "", errors.New("SOCKS5 greeting offers no authentication method")
	}
	if _, err := io.ReadFull(c, methods); err != nil {
		return "", err
	}
	method := byte(socksAuthNone)
	if cfg.Username != "" {
		method = socksAuthUser
	}
	if !slices.Contains(methods, method) {
		_, _ = c.Write([]byte{socksVersion, socksNoMethod})
		return "", errors.New("SOCKS5 client offered no acceptable authentication method")
	}
	if _, err := c.Write([]byte{socksVersion, method}); err != nil {
		return "", err
	}
	if method == socksAuthUser {
		if err := acceptSOCKSAuth(c, cfg); err != nil {
			return "", err
		}
	}

	var req [4]byte
	if _, err := io.ReadFull(c, req[:]); err != nil {
		return "", err
	}
	if req[0] != socksVersion {
		return "", fmt.Errorf("SOCKS request version %d is not supported", req[0])
	}
	if req[2] != 0 {
		_ = writeSOCKSReply(c, 0x01)
		return "", errors.New("SOCKS5 request reserved byte is not zero")
	}
	if req[1] != socksConnect {
		_ = writeSOCKSReply(c, 0x07)
		return "", fmt.Errorf("SOCKS5 command %d is not supported", req[1])
	}

	var host string
	switch req[3] {
	case socksIPv4:
		var ip [4]byte
		if _, err := io.ReadFull(c, ip[:]); err != nil {
			return "", err
		}
		host = net.IP(ip[:]).String()
	case socksIPv6:
		var ip [16]byte
		if _, err := io.ReadFull(c, ip[:]); err != nil {
			return "", err
		}
		host = net.IP(ip[:]).String()
	case socksDomain:
		var length [1]byte
		if _, err := io.ReadFull(c, length[:]); err != nil {
			return "", err
		}
		if length[0] == 0 {
			_ = writeSOCKSReply(c, 0x08)
			return "", errors.New("SOCKS5 domain name is empty")
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(c, name); err != nil {
			return "", err
		}
		host = string(name)
		if !validSOCKSName(host) {
			_ = writeSOCKSReply(c, 0x08)
			return "", errors.New("SOCKS5 domain name is not usable")
		}
	default:
		_ = writeSOCKSReply(c, 0x08)
		return "", fmt.Errorf("SOCKS5 address type %d is not supported", req[3])
	}

	var portBytes [2]byte
	if _, err := io.ReadFull(c, portBytes[:]); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBytes[:])
	if port == 0 {
		_ = writeSOCKSReply(c, 0x01)
		return "", errors.New("SOCKS5 destination port is zero")
	}
	if err := writeSOCKSReply(c, 0x00); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func acceptSOCKSAuth(c net.Conn, cfg SOCKSConfig) error {
	var head [2]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return err
	}
	if head[0] != 0x01 {
		_, _ = c.Write([]byte{0x01, 0x01})
		return fmt.Errorf("SOCKS5 authentication version %d is not supported", head[0])
	}
	user := make([]byte, int(head[1]))
	if _, err := io.ReadFull(c, user); err != nil {
		return err
	}
	var length [1]byte
	if _, err := io.ReadFull(c, length[:]); err != nil {
		return err
	}
	pass := make([]byte, int(length[0]))
	if _, err := io.ReadFull(c, pass); err != nil {
		return err
	}
	ok := subtle.ConstantTimeCompare(user, []byte(cfg.Username)) == 1 &&
		subtle.ConstantTimeCompare(pass, []byte(cfg.Password)) == 1
	status := byte(0x01)
	if ok {
		status = 0x00
	}
	if _, err := c.Write([]byte{0x01, status}); err != nil {
		return err
	}
	if !ok {
		return errors.New("SOCKS5 authentication failed")
	}
	return nil
}

func writeSOCKSReply(c net.Conn, code byte) error {
	// The bound address is not meaningful for a lazy proxy: the upstream is
	// dialled with the first request. IPv4 0.0.0.0:0 is the conventional
	// placeholder.
	_, err := c.Write([]byte{socksVersion, code, 0x00, socksIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

func validSOCKSName(name string) bool {
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "\x00/\\:[]") {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}
