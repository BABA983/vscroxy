package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

const (
	// maxPACSource bounds a script read from a file or URL.
	maxPACSource = 2 << 20
	// maxPACResult bounds what one FindProxyForURL call may return.
	maxPACResult = 64 << 10
	// maxPACCache bounds the per-host result cache. Reaching it clears the
	// cache rather than growing without limit.
	maxPACCache = 4096
	// defaultPACTimeout bounds one evaluation, including its DNS lookups.
	defaultPACTimeout = 2 * time.Second
	// defaultPACMemory is the heap growth one evaluation may cause before it
	// is interrupted. Goja has no per-runtime heap quota; this watchdog is
	// the safety bound for an accidental allocation loop.
	defaultPACMemory = 64 << 20
)

var (
	errPACTimeout = errors.New("PAC evaluation timed out")
	errPACMemory  = errors.New("PAC evaluation exceeded its memory limit")
)

// PACOptions adjust one PAC evaluator. The zero value uses the defaults; tests
// set the clock and resolver to fixed values.
type PACOptions struct {
	Timeout     time.Duration
	MemoryLimit int64
	Now         func() time.Time
	Lookup      func(context.Context, string) ([]netip.Addr, error)
	MyIP        func() netip.Addr
}

// PAC is a compiled proxy auto-configuration script and its per-host result
// cache. Each evaluation gets a fresh goja runtime, so a script cannot keep
// state between hosts or reach another request's data.
type PAC struct {
	name        string
	program     *goja.Program
	timeout     time.Duration
	memoryLimit int64
	now         func() time.Time
	lookup      func(context.Context, string) ([]netip.Addr, error)
	myIP        func() netip.Addr

	mu    sync.Mutex
	cache map[string]pacResult

	evaluations atomic.Int64
}

type pacResult struct {
	candidates []Candidate
	display    string
}

// newPAC compiles source. A syntax error is returned here, not on the first
// request, so a bad PAC file is reported when it is loaded.
func newPAC(source, name string, opts PACOptions) (*PAC, error) {
	program, err := goja.Compile(name, source, false)
	if err != nil {
		return nil, err
	}
	p := &PAC{
		name:        name,
		program:     program,
		timeout:     opts.Timeout,
		memoryLimit: opts.MemoryLimit,
		now:         opts.Now,
		lookup:      opts.Lookup,
		myIP:        opts.MyIP,
		cache:       make(map[string]pacResult),
	}
	if p.timeout <= 0 {
		p.timeout = defaultPACTimeout
	}
	if p.memoryLimit <= 0 {
		p.memoryLimit = defaultPACMemory
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.lookup == nil {
		p.lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	if p.myIP == nil {
		p.myIP = localIPv4
	}
	return p, nil
}

// loadPACSource reads a PAC script from an http(s) URL, a file:// URL or a
// local path. Fetches are direct (never through the upstream being configured)
// and bounded in time and size.
func loadPACSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("a file path or URL is required")
	}
	if u, err := url.Parse(source); err == nil {
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			client := &http.Client{
				Timeout: 10 * time.Second,
				Transport: &http.Transport{
					Proxy:             nil,
					ForceAttemptHTTP2: true,
				},
			}
			req, err := http.NewRequest(http.MethodGet, source, nil)
			if err != nil {
				return "", err
			}
			resp, err := client.Do(req)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return "", fmt.Errorf("GET returned %s", resp.Status)
			}
			return readPAC(resp.Body)
		case "file":
			if u.Host != "" && u.Host != "localhost" {
				return "", fmt.Errorf("file URL host %q is not local", u.Host)
			}
			return readPACFile(u.Path)
		}
	}
	return readPACFile(source)
}

func readPACFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return readPAC(f)
}

func readPAC(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxPACSource+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxPACSource {
		return "", fmt.Errorf("the script is larger than %d bytes", maxPACSource)
	}
	return string(data), nil
}

// findProxy evaluates the script for one host and caches the parsed result by
// host. PAC failures are errors; they never become a silent DIRECT.
func (p *PAC) findProxy(ctx context.Context, rawURL, host string) ([]Candidate, string, error) {
	host = normalizeHost(host)
	p.mu.Lock()
	if cached, ok := p.cache[host]; ok {
		p.mu.Unlock()
		return cloneCandidates(cached.candidates), cached.display, nil
	}
	p.mu.Unlock()

	result, err := p.evaluate(ctx, rawURL, host)
	if err != nil {
		return nil, "", err
	}
	if len(result) > maxPACResult {
		return nil, "", fmt.Errorf("FindProxyForURL returned more than %d bytes", maxPACResult)
	}
	candidates, display, err := parsePACResult(result)
	if err != nil {
		return nil, "", err
	}
	p.mu.Lock()
	if len(p.cache) >= maxPACCache {
		p.cache = make(map[string]pacResult)
	}
	p.cache[host] = pacResult{candidates: cloneCandidates(candidates), display: display}
	p.mu.Unlock()
	return candidates, display, nil
}

func (p *PAC) evaluate(ctx context.Context, rawURL, host string) (string, error) {
	p.evaluations.Add(1)
	vm := goja.New()
	vm.SetMaxCallStackSize(1000)
	p.install(vm, ctx)

	var interrupted atomic.Bool
	interrupt := func(reason error) {
		if interrupted.CompareAndSwap(false, true) {
			vm.Interrupt(reason)
		}
	}
	timer := time.AfterFunc(p.timeout, func() { interrupt(errPACTimeout) })
	defer timer.Stop()
	stopContext := context.AfterFunc(ctx, func() {
		if ctx.Err() != nil {
			interrupt(ctx.Err())
		}
	})
	defer stopContext()

	baseline := heapAlloc()
	done := make(chan struct{})
	var watchdog sync.WaitGroup
	watchdog.Add(1)
	go func() {
		defer watchdog.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if heapAlloc()-baseline > uint64(p.memoryLimit) {
					interrupt(errPACMemory)
					return
				}
			}
		}
	}()

	if _, err := vm.RunProgram(p.program); err != nil {
		close(done)
		watchdog.Wait()
		return "", p.evalError(err, interrupted.Load())
	}
	value := vm.Get("FindProxyForURL")
	fn, ok := goja.AssertFunction(value)
	if !ok {
		close(done)
		watchdog.Wait()
		return "", errors.New("the script does not define FindProxyForURL")
	}
	out, err := fn(goja.Undefined(), vm.ToValue(rawURL), vm.ToValue(host))
	close(done)
	watchdog.Wait()
	if err != nil {
		return "", p.evalError(err, interrupted.Load())
	}
	if goja.IsUndefined(out) || goja.IsNull(out) {
		return "DIRECT", nil
	}
	if out.ExportType().Kind() != reflect.String {
		return "", fmt.Errorf("FindProxyForURL returned %s, not a string", out.ExportType())
	}
	return out.String(), nil
}

func (p *PAC) evalError(err error, interrupted bool) error {
	var stopped *goja.InterruptedError
	if errors.As(err, &stopped) {
		switch stopped.Value() {
		case errPACTimeout:
			return fmt.Errorf("FindProxyForURL did not finish within %s", p.timeout)
		case errPACMemory:
			return fmt.Errorf("FindProxyForURL exceeded its memory limit: allocated more than %d MiB", p.memoryLimit>>20)
		}
		if stopped.Value() != nil {
			return fmt.Errorf("FindProxyForURL was interrupted: %v", stopped.Value())
		}
	}
	if interrupted {
		return fmt.Errorf("FindProxyForURL was interrupted: %w", err)
	}
	return fmt.Errorf("FindProxyForURL: %w", err)
}

func (p *PAC) install(vm *goja.Runtime, ctx context.Context) {
	arg := func(call goja.FunctionCall, i int) string {
		return strings.TrimSpace(call.Argument(i).String())
	}
	vm.Set("alert", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	vm.Set("isPlainHostName", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(!strings.Contains(arg(call, 0), "."))
	})
	vm.Set("dnsDomainIs", func(call goja.FunctionCall) goja.Value {
		host, domain := strings.ToLower(arg(call, 0)), strings.ToLower(arg(call, 1))
		return vm.ToValue(len(host) >= len(domain) && strings.HasSuffix(host, domain))
	})
	vm.Set("localHostOrDomainIs", func(call goja.FunctionCall) goja.Value {
		host, hostdom := strings.ToLower(arg(call, 0)), strings.ToLower(arg(call, 1))
		return vm.ToValue(host == hostdom || strings.HasPrefix(hostdom, host+"."))
	})
	vm.Set("dnsDomainLevels", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(strings.Count(arg(call, 0), "."))
	})
	vm.Set("shExpMatch", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(globMatch(arg(call, 0), arg(call, 1)))
	})
	vm.Set("dnsResolve", func(call goja.FunctionCall) goja.Value {
		if ip, ok := p.resolveIPv4(ctx, arg(call, 0)); ok {
			return vm.ToValue(ip.String())
		}
		return goja.Null()
	})
	vm.Set("isResolvable", func(call goja.FunctionCall) goja.Value {
		_, ok := p.resolveIPv4(ctx, arg(call, 0))
		return vm.ToValue(ok)
	})
	vm.Set("isInNet", func(call goja.FunctionCall) goja.Value {
		host := arg(call, 0)
		ip, ok := parseIPv4(host)
		if !ok {
			ip, ok = p.resolveIPv4(ctx, host)
		}
		pattern, pok := parseIPv4(arg(call, 1))
		mask, mok := parseIPv4(arg(call, 2))
		if !ok || !pok || !mok {
			return vm.ToValue(false)
		}
		return vm.ToValue(ipAnd(ip, mask) == ipAnd(pattern, mask))
	})
	vm.Set("convert_addr", func(call goja.FunctionCall) goja.Value {
		ip, ok := parseIPv4(arg(call, 0))
		if !ok {
			return goja.Null()
		}
		b := ip.As4()
		return vm.ToValue(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
	})
	vm.Set("myIpAddress", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(p.myIP().String())
	})
	vm.Set("weekdayRange", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(weekdayRange(p.now(), call.Arguments))
	})
	vm.Set("dateRange", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(dateRange(p.now(), call.Arguments))
	})
	vm.Set("timeRange", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(timeRange(p.now(), call.Arguments))
	})
}

func (p *PAC) resolveIPv4(ctx context.Context, host string) (netip.Addr, bool) {
	host = strings.TrimSpace(host)
	if ip, ok := parseIPv4(host); ok {
		return ip, true
	}
	if host == "" {
		return netip.Addr{}, false
	}
	lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	addrs, err := p.lookup(lookupCtx, host)
	if err != nil {
		return netip.Addr{}, false
	}
	for _, ip := range addrs {
		ip = ip.Unmap()
		if ip.Is4() {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

func localIPv4() netip.Addr {
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				var ip net.IP
				switch a := addr.(type) {
				case *net.IPNet:
					ip = a.IP
				case *net.IPAddr:
					ip = a.IP
				}
				if v4 := ip.To4(); v4 != nil && !v4.IsLoopback() {
					if out, ok := netip.AddrFromSlice(v4); ok {
						return out
					}
				}
			}
		}
	}
	return netip.MustParseAddr("127.0.0.1")
}

func parseIPv4(raw string) (netip.Addr, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 4 {
		return netip.Addr{}, false
	}
	var bytes [4]byte
	for i, part := range parts {
		if part == "" || len(part) > 3 {
			return netip.Addr{}, false
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > 255 {
			return netip.Addr{}, false
		}
		bytes[i] = byte(n)
	}
	return netip.AddrFrom4(bytes), true
}

func ipAnd(a, mask netip.Addr) netip.Addr {
	ab, mb := a.As4(), mask.As4()
	return netip.AddrFrom4([4]byte{ab[0] & mb[0], ab[1] & mb[1], ab[2] & mb[2], ab[3] & mb[3]})
}

func heapAlloc() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func cloneCandidates(in []Candidate) []Candidate {
	out := make([]Candidate, len(in))
	for i, c := range in {
		out[i] = c
		if c.Proxy != nil && c.Proxy.URL != nil {
			u := *c.Proxy.URL
			out[i].Proxy = &Proxy{URL: &u}
		}
	}
	return out
}

// parsePACResult reads the semicolon-separated list a PAC script returned.
// Every entry is validated; an unknown or malformed entry fails the whole
// result instead of silently dropping it.
func parsePACResult(raw string) ([]Candidate, string, error) {
	var candidates []Candidate
	var display []string
	for part := range strings.SplitSeq(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		kind := strings.ToUpper(fields[0])
		switch kind {
		case "DIRECT":
			if len(fields) != 1 {
				return nil, "", fmt.Errorf("DIRECT cannot have an address: %q", part)
			}
			candidates = append(candidates, Candidate{Reason: "DIRECT"})
			display = append(display, "DIRECT")
		case "PROXY", "HTTP", "HTTPS", "SOCKS", "SOCKS5":
			if len(fields) != 2 {
				return nil, "", fmt.Errorf("%s needs one host:port: %q", kind, part)
			}
			scheme := "http"
			label := "PROXY"
			switch kind {
			case "HTTPS":
				scheme, label = "https", "HTTPS"
			case "SOCKS", "SOCKS5":
				scheme, label = "socks5", "SOCKS5"
			}
			host, port, err := net.SplitHostPort(fields[1])
			if err != nil || host == "" {
				return nil, "", fmt.Errorf("%s address %q needs host:port", kind, fields[1])
			}
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return nil, "", fmt.Errorf("%s port %q must be 1 to 65535", kind, port)
			}
			addr := strings.ToLower(net.JoinHostPort(host, port))
			candidates = append(candidates, Candidate{Proxy: &Proxy{URL: &url.URL{Scheme: scheme, Host: addr}}, Reason: label + " " + addr})
			display = append(display, label+" "+addr)
		case "SOCKS4":
			return nil, "", errors.New("SOCKS4 is not supported; use SOCKS5")
		default:
			return nil, "", fmt.Errorf("unknown PAC directive %q", kind)
		}
	}
	if len(candidates) == 0 {
		return nil, "", errors.New("FindProxyForURL returned no proxy or DIRECT")
	}
	return candidates, strings.Join(display, "; "), nil
}

// withCredentials attaches the configured username and password to each
// candidate's endpoint. It clones so cached PAC results stay credential-free.
func (c *Config) withCredentials(in []Candidate) []Candidate {
	out := cloneCandidates(in)
	for i := range out {
		px := out[i].Proxy
		if px == nil || px.URL == nil {
			continue
		}
		key := px.URL.Scheme + "://" + px.URL.Host
		if cred, ok := c.credentials[key]; ok {
			u := *px.URL
			u.User = url.UserPassword(cred.username, cred.password)
			out[i].Proxy = &Proxy{URL: &u}
		}
	}
	return out
}

// globMatch implements the shell expression PAC's shExpMatch documents: *
// matches any run of characters, ? one character, and [] a character class
// with optional ranges and ! or ^ negation. Other characters are literal.
func globMatch(s, pattern string) bool {
	sr, pr := []rune(s), []rune(pattern)
	type state struct{ i, j int }
	memo := make(map[state]bool)
	seen := make(map[state]bool)
	var match func(i, j int) bool
	match = func(i, j int) bool {
		key := state{i, j}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		ok := false
		switch {
		case j == len(pr):
			ok = i == len(sr)
		case pr[j] == '*':
			ok = match(i, j+1) || i < len(sr) && match(i+1, j)
		case pr[j] == '?':
			ok = i < len(sr) && match(i+1, j+1)
		case pr[j] == '\\' && j+1 < len(pr):
			ok = i < len(sr) && sr[i] == pr[j+1] && match(i+1, j+2)
		case pr[j] == '[':
			if class, next, valid := parseClass(pr, j); valid {
				ok = i < len(sr) && class.matches(sr[i]) && match(i+1, next)
			} else {
				ok = i < len(sr) && sr[i] == '[' && match(i+1, j+1)
			}
		default:
			ok = i < len(sr) && sr[i] == pr[j] && match(i+1, j+1)
		}
		memo[key] = ok
		return ok
	}
	return match(0, 0)
}

type charClass struct {
	negate bool
	items  []rune
}

func (c charClass) matches(r rune) bool {
	hit := false
	for i := 0; i < len(c.items); i++ {
		if i+2 < len(c.items) && c.items[i+1] == '-' {
			if c.items[i] <= r && r <= c.items[i+2] {
				hit = true
			}
			i += 2
			continue
		}
		if c.items[i] == r {
			hit = true
		}
	}
	if c.negate {
		return !hit
	}
	return hit
}

// parseClass reads a [...] class starting at pattern[start] and returns the
// index after the closing bracket. An unclosed class is not a class.
func parseClass(pattern []rune, start int) (charClass, int, bool) {
	var class charClass
	i := start + 1
	if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
		class.negate = true
		i++
	}
	if i < len(pattern) && pattern[i] == ']' {
		class.items = append(class.items, ']')
		i++
	}
	for ; i < len(pattern); i++ {
		switch pattern[i] {
		case ']':
			if len(class.items) == 0 {
				return class, 0, false
			}
			return class, i + 1, true
		case '\\':
			if i+1 < len(pattern) {
				i++
			}
			class.items = append(class.items, pattern[i])
		default:
			class.items = append(class.items, pattern[i])
		}
	}
	return class, 0, false
}

func isGMT(v goja.Value) bool {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return false
	}
	if v.ExportType().Kind() != reflect.String {
		return false
	}
	return strings.EqualFold(v.String(), "GMT")
}

func intValue(v goja.Value) (int, bool) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0, false
	}
	switch v.ExportType().Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
	default:
		return 0, false
	}
	f := v.ToFloat()
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 0 || f > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

var monthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

func monthValue(v goja.Value) (int, bool) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) || v.ExportType().Kind() != reflect.String {
		return 0, false
	}
	n, ok := monthNames[strings.ToUpper(strings.TrimSpace(v.String()))]
	return n, ok
}

var weekdayNames = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

func weekdayValue(v goja.Value) (int, bool) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) || v.ExportType().Kind() != reflect.String {
		return 0, false
	}
	n, ok := weekdayNames[strings.ToUpper(strings.TrimSpace(v.String()))]
	return n, ok
}

// inWrapped reports whether v is inside a..b, wrapping at the category's end
// when a > b. Firefox 49 and later support these reversed ranges.
func inWrapped(v, a, b int) bool {
	if a <= b {
		return a <= v && v <= b
	}
	return v >= a || v <= b
}

func validDate(day, month, year int) bool {
	return day >= 1 && day <= 31 && month >= 1 && month <= 12 && year >= 1
}

func dateRange(now time.Time, args []goja.Value) bool {
	if n := len(args); n > 0 && isGMT(args[n-1]) {
		args = args[:n-1]
		now = now.UTC()
	}
	year, month, day := now.Year(), int(now.Month()), now.Day()
	switch len(args) {
	case 1:
		if m, ok := monthValue(args[0]); ok {
			return month == m
		}
		n, ok := intValue(args[0])
		if !ok {
			return false
		}
		if n <= 31 {
			return validDate(n, 1, 1) && day == n
		}
		return year == n
	case 2:
		if m1, ok := monthValue(args[0]); ok {
			if m2, ok := monthValue(args[1]); ok {
				return inWrapped(month, m1, m2)
			}
			if y, ok := intValue(args[1]); ok {
				return validDate(1, m1, y) && month == m1 && year == y
			}
			return false
		}
		d1, ok := intValue(args[0])
		if !ok {
			return false
		}
		if m2, ok := monthValue(args[1]); ok {
			return validDate(d1, m2, 1) && day == d1 && month == m2
		}
		d2, ok := intValue(args[1])
		if !ok {
			return false
		}
		if d1 > 31 || d2 > 31 {
			return d1 >= 1 && d2 >= 1 && inWrapped(year, d1, d2)
		}
		return validDate(d1, 1, 1) && validDate(d2, 1, 1) && inWrapped(day, d1, d2)
	case 4:
		if d1, ok := intValue(args[0]); ok {
			if m1, ok := monthValue(args[1]); ok {
				d2, ok := intValue(args[2])
				if !ok {
					return false
				}
				m2, ok := monthValue(args[3])
				if !ok || !validDate(d1, m1, 1) || !validDate(d2, m2, 1) {
					return false
				}
				return inWrapped(month*100+day, m1*100+d1, m2*100+d2)
			}
		}
		if m1, ok := monthValue(args[0]); ok {
			y1, ok := intValue(args[1])
			if !ok {
				return false
			}
			m2, ok := monthValue(args[2])
			if !ok {
				return false
			}
			y2, ok := intValue(args[3])
			if !ok || !validDate(1, m1, y1) || !validDate(1, m2, y2) {
				return false
			}
			return inWrapped(year*12+month, y1*12+m1, y2*12+m2)
		}
		return false
	case 6:
		d1, ok1 := intValue(args[0])
		m1, ok2 := monthValue(args[1])
		y1, ok3 := intValue(args[2])
		d2, ok4 := intValue(args[3])
		m2, ok5 := monthValue(args[4])
		y2, ok6 := intValue(args[5])
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !validDate(d1, m1, y1) || !validDate(d2, m2, y2) {
			return false
		}
		return inWrapped(year*10000+month*100+day, y1*10000+m1*100+d1, y2*10000+m2*100+d2)
	default:
		return false
	}
}

func weekdayRange(now time.Time, args []goja.Value) bool {
	if n := len(args); n > 0 && isGMT(args[n-1]) {
		args = args[:n-1]
		now = now.UTC()
	}
	today := int(now.Weekday())
	switch len(args) {
	case 1:
		day, ok := weekdayValue(args[0])
		return ok && day == today
	case 2:
		first, ok1 := weekdayValue(args[0])
		last, ok2 := weekdayValue(args[1])
		return ok1 && ok2 && inWrapped(today, first, last)
	default:
		return false
	}
}

func timeRange(now time.Time, args []goja.Value) bool {
	if n := len(args); n > 0 && isGMT(args[n-1]) {
		args = args[:n-1]
		now = now.UTC()
	}
	hour, minute, second := now.Hour(), now.Minute(), now.Second()
	valid := func(h, m, s int) bool {
		return h >= 0 && h <= 23 && m >= 0 && m <= 59 && s >= 0 && s <= 59
	}
	switch len(args) {
	case 1:
		h, ok := intValue(args[0])
		return ok && valid(h, 0, 0) && hour == h
	case 2:
		h1, ok1 := intValue(args[0])
		h2, ok2 := intValue(args[1])
		return ok1 && ok2 && valid(h1, 0, 0) && valid(h2, 0, 0) && inWrapped(hour, h1, h2)
	case 4:
		h1, ok1 := intValue(args[0])
		m1, ok2 := intValue(args[1])
		h2, ok3 := intValue(args[2])
		m2, ok4 := intValue(args[3])
		return ok1 && ok2 && ok3 && ok4 && valid(h1, m1, 0) && valid(h2, m2, 0) &&
			inWrapped(hour*60+minute, h1*60+m1, h2*60+m2)
	case 6:
		h1, ok1 := intValue(args[0])
		m1, ok2 := intValue(args[1])
		s1, ok3 := intValue(args[2])
		h2, ok4 := intValue(args[3])
		m2, ok5 := intValue(args[4])
		s2, ok6 := intValue(args[5])
		return ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && valid(h1, m1, s1) && valid(h2, m2, s2) &&
			inWrapped(hour*3600+minute*60+second, h1*3600+m1*60+s1, h2*3600+m2*60+s2)
	default:
		return false
	}
}
