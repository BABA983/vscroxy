package request

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
)

// socketSend sends an HTTP(S) exchange directly to a Unix domain socket or a
// Windows named pipe. The capture proxy cannot dial a local socket, so this
// path builds its own client and keeps the authored redirect, TLS and cookie
// rules; the exchange is not recorded in the capture table.
func socketSend(ctx context.Context, f capture.Flow, policy capture.TransferPolicy, jar http.CookieJar, certificates certs.Settings, ca []byte, socket string) (capture.Flow, error) {
	req, raw, err := capture.NewReplayRequest(ctx, f)
	if err != nil {
		return capture.Flow{}, err
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialSocket(ctx, socket)
	}
	// The socket is the network; ALPN still decides an HTTPS connection's
	// protocol, so "2" can require h2 and "1.1" keeps it out.
	base.ForceAttemptHTTP2 = policy.HTTPVersion != "1.1"
	client := &http.Client{
		Jar: jar,
		Transport: certificates.TransportWithOptions(base, ca, policy.TLS == capture.TLSInsecure,
			certs.TLSOptions{Versions: policy.TLSVersions, CipherSuites: policy.TLSCipherSuites}),
	}
	client.CheckRedirect = policy.Redirects.Checker(req)
	defer client.CloseIdleConnections()
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return capture.Flow{}, err
	}
	defer resp.Body.Close()
	if policy.HTTPVersion == "2" && resp.ProtoMajor != 2 {
		return capture.Flow{}, fmt.Errorf("HTTP/2 was required, but the server answered %s", resp.Proto)
	}
	preview, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return capture.Flow{}, err
	}
	rest, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return capture.Flow{}, err
	}
	rawURL := f.URL
	if rawURL == "" {
		rawURL = f.Scheme + "://" + f.Host + f.Path
	}
	requestBody := raw
	if len(requestBody) > 8<<20 {
		requestBody = requestBody[:8<<20]
	}
	return capture.Flow{
		Method: req.Method, URL: rawURL, Status: resp.StatusCode,
		RequestHeader: req.Header.Clone(), RequestBody: strings.Clone(requestBody),
		RequestBodySize: len(raw), RequestBodyWireSize: len(raw),
		ResponseHeader: resp.Header.Clone(), ResponseBody: string(preview),
		ResponseBodySize: len(preview) + int(rest), Bytes: len(preview) + int(rest),
		ResponseTrailer: resp.Trailer.Clone(), ResponseProto: resp.Proto,
		Millis: int(time.Since(started).Milliseconds()), At: time.Now(),
	}, nil
}
