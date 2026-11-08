// Package ntlmtest is a small NTLMv2 server for real-socket tests. It checks
// the Type 1/2/3 exchange on one connection with its own implementation of the
// MS-NLMP 4.2.4 calculations, independent of the client library the app uses.
// It is test support, not application code.
package ntlmtest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"golang.org/x/crypto/md4"
)

// Timestamp is the MS-NLMP 4.2.4 filetime the challenge carries; the client
// must echo it in its NTLMv2 blob.
const Timestamp uint64 = 131400439870000000

const (
	negotiateUnicode                 = 0x00000001
	negotiateNTLM                    = 0x00000200
	negotiateAlwaysSign              = 0x00008000
	targetTypeServer                 = 0x00020000
	negotiateExtendedSessionSecurity = 0x00080000
	negotiateTargetInfo              = 0x00800000
	negotiate128                     = 0x20000000
	negotiate56                      = 0x80000000
)

// Server answers HTTP requests with an NTLM challenge and accepts one account.
// Use it as an http.Handler and set ConnContext on the http.Server so each
// connection keeps its own challenge.
type Server struct {
	Username string
	Password string
	Domain   string
	// Workstation, when set, must match the Type 3 message's workstation.
	Workstation string
	// Delay waits before answering, for timeout tests.
	Delay time.Duration
	// CloseChallenge closes the connection after the Type 2 response:
	// "header" sends Connection: close, "silent" closes without saying so.
	CloseChallenge string

	mu          sync.Mutex
	connections int
	requests    []Request
	problems    []string
}

// Request is one exchange the server saw.
type Request struct {
	Connection  int
	Path        string
	Stage       string // challenge, type1, type2, authenticated, denied
	User        string
	Domain      string
	Workstation string
	Cookie      string
	Body        string
}

type stateKey struct{}

type connState struct {
	id int

	mu         sync.Mutex
	challenge  [8]byte
	targetInfo []byte
	stage      string
}

// ConnContext gives each connection its own challenge state.
func (s *Server) ConnContext(ctx context.Context, _ net.Conn) context.Context {
	s.mu.Lock()
	s.connections++
	id := s.connections
	s.mu.Unlock()
	return context.WithValue(ctx, stateKey{}, &connState{id: id})
}

// Connections is how many connections the server accepted.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connections
}

// Requests is what the server saw, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Problems names protocol violations the client committed.
func (s *Server) Problems() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.problems...)
}

func (s *Server) problem(format string, args ...any) {
	s.mu.Lock()
	s.problems = append(s.problems, fmt.Sprintf(format, args...))
	s.mu.Unlock()
}

func (s *Server) record(r *http.Request, st *connState, stage string, t3 *type3) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	entry := Request{Connection: st.id, Path: r.URL.Path, Stage: stage, Cookie: r.Header.Get("Cookie"), Body: string(body)}
	if t3 != nil {
		entry.User, entry.Domain, entry.Workstation = t3.User, t3.Domain, t3.Workstation
	}
	s.mu.Lock()
	s.requests = append(s.requests, entry)
	s.mu.Unlock()
}

// ServeHTTP runs the challenge-response exchange.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Delay > 0 {
		select {
		case <-time.After(s.Delay):
		case <-r.Context().Done():
			return
		}
	}
	st, _ := r.Context().Value(stateKey{}).(*connState)
	if st == nil {
		http.Error(w, "ntlmtest: request has no connection state", http.StatusInternalServerError)
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	auth := r.Header.Get("Authorization")
	if auth == "" {
		st.stage = "challenge"
		s.record(r, st, "challenge", nil)
		w.Header().Set("WWW-Authenticate", "NTLM")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if !strings.HasPrefix(auth, "NTLM ") {
		s.problem("unexpected Authorization %q", auth)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	message, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(auth, "NTLM ")))
	if err != nil || len(message) < 12 || string(message[:8]) != "NTLMSSP\x00" {
		s.problem("malformed NTLM message")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch binary.LittleEndian.Uint32(message[8:12]) {
	case 1:
		s.type1(w, r, st, message)
	case 3:
		s.type3(w, r, st, message)
	default:
		s.problem("unexpected NTLM message type %d", binary.LittleEndian.Uint32(message[8:12]))
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (s *Server) type1(w http.ResponseWriter, r *http.Request, st *connState, message []byte) {
	if st.stage != "challenge" {
		s.problem("type 1 without a preceding challenge on connection %d", st.id)
	}
	domain, workstation, err := parseType1(message)
	if err != nil {
		s.problem("type 1: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if s.Workstation != "" && !strings.EqualFold(workstation, s.Workstation) {
		s.problem("type 1 workstation %q, want %q", workstation, s.Workstation)
	}
	if domain != "" && !strings.EqualFold(domain, s.Domain) {
		s.problem("type 1 domain %q, want %q", domain, s.Domain)
	}
	if _, err := rand.Read(st.challenge[:]); err != nil {
		s.problem("random challenge: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	st.targetInfo = s.targetInfo()
	st.stage = "type2"
	s.record(r, st, "type1", nil)
	challenge := s.challengeMessage(st.challenge, st.targetInfo)
	if s.CloseChallenge == "silent" {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			s.problem("response writer cannot be hijacked")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, buf, err := hijacker.Hijack()
		if err != nil {
			s.problem("hijack: %v", err)
			return
		}
		fmt.Fprintf(buf, "HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\nWWW-Authenticate: NTLM %s\r\n\r\n", base64.StdEncoding.EncodeToString(challenge))
		_ = buf.Flush()
		_ = conn.Close()
		return
	}
	if s.CloseChallenge == "header" {
		w.Header().Set("Connection", "close")
	}
	w.Header().Set("WWW-Authenticate", "NTLM "+base64.StdEncoding.EncodeToString(challenge))
	w.WriteHeader(http.StatusUnauthorized)
}

func (s *Server) type3(w http.ResponseWriter, r *http.Request, st *connState, message []byte) {
	parsed, err := parseType3(message)
	if err != nil {
		s.problem("type 3: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if st.stage != "type2" {
		s.problem("type 3 without a challenge on connection %d", st.id)
		s.record(r, st, "denied", parsed)
		w.Header().Set("WWW-Authenticate", "NTLM")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if !s.valid(st, parsed) {
		s.record(r, st, "denied", parsed)
		w.Header().Set("WWW-Authenticate", "NTLM")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.record(r, st, "authenticated", parsed)
	w.Header().Set("X-NTLM-User", parsed.Domain+`\`+parsed.User)
	io.WriteString(w, "authenticated")
}

// valid checks the NTLMv2 proof and the blob the client echoed.
func (s *Server) valid(st *connState, t3 *type3) bool {
	if t3.User != s.Username || !strings.EqualFold(t3.Domain, s.Domain) {
		return false
	}
	if s.Workstation != "" && !strings.EqualFold(t3.Workstation, s.Workstation) {
		return false
	}
	if len(t3.NT) < 16+28+len(st.targetInfo)+4 {
		return false
	}
	proof := hmacMD5(NTOWFv2(s.Password, t3.User, t3.Domain), st.challenge[:], t3.NT[16:])
	if !hmac.Equal(proof, t3.NT[:16]) {
		return false
	}
	blob := t3.NT[16:]
	if !bytes.Equal(blob[:8], []byte{1, 1, 0, 0, 0, 0, 0, 0}) {
		return false
	}
	if binary.LittleEndian.Uint64(blob[8:16]) != Timestamp {
		return false
	}
	if !bytes.Equal(blob[24:28], make([]byte, 4)) {
		return false
	}
	if !bytes.Equal(blob[28:28+len(st.targetInfo)], st.targetInfo) {
		return false
	}
	return bytes.Equal(blob[28+len(st.targetInfo):], make([]byte, 4))
}

// targetInfo is the AV pair list the challenge carries.
func (s *Server) targetInfo() []byte {
	var info []byte
	add := func(id uint16, value []byte) {
		info = binary.LittleEndian.AppendUint16(info, id)
		info = binary.LittleEndian.AppendUint16(info, uint16(len(value)))
		info = append(info, value...)
	}
	add(2, utf16le(s.Domain))   // MsvAvNbDomainName
	add(1, utf16le("SERVER"))   // MsvAvNbComputerName
	add(7, filetime(Timestamp)) // MsvAvTimestamp
	add(0, nil)                 // MsvAvEOL
	return info
}

// challengeMessage builds a Type 2 message with a target name and target info.
func (s *Server) challengeMessage(challenge [8]byte, targetInfo []byte) []byte {
	const flags = negotiateUnicode | negotiateNTLM | negotiateAlwaysSign | targetTypeServer |
		negotiateExtendedSessionSecurity | negotiateTargetInfo | negotiate128 | negotiate56
	target := utf16le("SERVER")
	message := make([]byte, 48)
	copy(message, "NTLMSSP\x00")
	binary.LittleEndian.PutUint32(message[8:12], 2)
	binary.LittleEndian.PutUint16(message[12:14], uint16(len(target)))
	binary.LittleEndian.PutUint16(message[14:16], uint16(len(target)))
	binary.LittleEndian.PutUint32(message[16:20], uint32(48+len(targetInfo)))
	binary.LittleEndian.PutUint32(message[20:24], flags)
	copy(message[24:32], challenge[:])
	binary.LittleEndian.PutUint16(message[40:42], uint16(len(targetInfo)))
	binary.LittleEndian.PutUint16(message[42:44], uint16(len(targetInfo)))
	binary.LittleEndian.PutUint32(message[44:48], 48)
	message = append(message, targetInfo...)
	return append(message, target...)
}

// type3 is the parsed AUTHENTICATE message.
type type3 struct {
	LM, NT                    []byte
	Domain, User, Workstation string
}

func parseType1(message []byte) (domain, workstation string, err error) {
	if len(message) < 32 {
		return "", "", fmt.Errorf("short Type 1 message")
	}
	domainBytes, err := readField(message, 16)
	if err != nil {
		return "", "", err
	}
	workstationBytes, err := readField(message, 24)
	if err != nil {
		return "", "", err
	}
	return string(domainBytes), string(workstationBytes), nil
}

func parseType3(message []byte) (*type3, error) {
	if len(message) < 64 {
		return nil, fmt.Errorf("short Type 3 message")
	}
	lm, err := readField(message, 12)
	if err != nil {
		return nil, err
	}
	nt, err := readField(message, 20)
	if err != nil {
		return nil, err
	}
	domain, err := readUTF16Field(message, 28)
	if err != nil {
		return nil, err
	}
	user, err := readUTF16Field(message, 36)
	if err != nil {
		return nil, err
	}
	workstation, err := readUTF16Field(message, 44)
	if err != nil {
		return nil, err
	}
	return &type3{LM: lm, NT: nt, Domain: domain, User: user, Workstation: workstation}, nil
}

// readField reads a length/max-length/offset field.
func readField(message []byte, at int) ([]byte, error) {
	if at+8 > len(message) {
		return nil, fmt.Errorf("field at %d is outside the message", at)
	}
	length := int(binary.LittleEndian.Uint16(message[at:]))
	offset := int(binary.LittleEndian.Uint32(message[at+4:]))
	if offset < 0 || length < 0 || offset+length > len(message) {
		return nil, fmt.Errorf("field at %d spans %d bytes at %d", at, length, offset)
	}
	return message[offset : offset+length], nil
}

func readUTF16Field(message []byte, at int) (string, error) {
	raw, err := readField(message, at)
	if err != nil {
		return "", err
	}
	if len(raw)%2 != 0 {
		return "", fmt.Errorf("odd UTF-16 field at %d", at)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	return string(utf16.Decode(units)), nil
}

func utf16le(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(out[i*2:], unit)
	}
	return out
}

func filetime(value uint64) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, value)
	return out
}

// NTOWFv2 is HMAC-MD5(MD4(UTF-16LE(password)), UTF-16LE(UPPER(user)+domain)).
func NTOWFv2(password, user, domain string) []byte {
	hash := md4.New()
	hash.Write(utf16le(password))
	return hmacMD5(hash.Sum(nil), utf16le(strings.ToUpper(user)+domain))
}

// NTLMv2Response is NTProofStr followed by temp, as MS-NLMP 3.3.2 defines it.
func NTLMv2Response(key, serverChallenge, clientChallenge, timestamp, targetInfo []byte) []byte {
	temp := []byte{1, 1, 0, 0, 0, 0, 0, 0}
	temp = append(temp, timestamp...)
	temp = append(temp, clientChallenge...)
	temp = append(temp, 0, 0, 0, 0)
	temp = append(temp, targetInfo...)
	temp = append(temp, 0, 0, 0, 0)
	return append(hmacMD5(key, serverChallenge, temp), temp...)
}

func hmacMD5(key []byte, data ...[]byte) []byte {
	mac := hmac.New(md5.New, key)
	for _, part := range data {
		mac.Write(part)
	}
	return mac.Sum(nil)
}
