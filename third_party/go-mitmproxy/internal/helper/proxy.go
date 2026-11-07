package helper

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// ErrProxyAuth means the upstream proxy rejected the configured credentials.
// Callers must not fall back to another proxy or DIRECT after it.
var ErrProxyAuth = errors.New("upstream proxy authentication failed")

// ErrProxyResponse means the proxy answered with an HTTP response instead of
// establishing the tunnel. Like an authentication failure, it is final: a
// proxy's 403 or 502 is a policy decision, not a transport failure.
var ErrProxyResponse = errors.New("upstream proxy returned an HTTP response")

// GetProxyConn connect proxy
// ref: http/transport.go dialConn func
func GetProxyConn(ctx context.Context, proxyUrl *url.URL, address string, proxyTLS *tls.Config) (net.Conn, error) {
	var conn net.Conn
	if proxyUrl.Scheme == "socks5" {
		//检测socks5认证信息
		proxyAuth := &proxy.Auth{}
		if proxyUrl.User != nil {
			user := proxyUrl.User.Username()
			pass, _ := proxyUrl.User.Password()
			proxyAuth.User = user
			proxyAuth.Password = pass
		}
		dialer, err := proxy.SOCKS5("tcp", proxyUrl.Host, proxyAuth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		dc := dialer.(interface {
			DialContext(ctx context.Context, network, addr string) (net.Conn, error)
		})
		conn, err = dc.DialContext(ctx, "tcp", address)
		if err != nil {
			if isSOCKSAuthError(err) {
				return nil, fmt.Errorf("%w: %v", ErrProxyAuth, err)
			}
			return nil, err
		}
		return conn, err
	} else {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyUrl.Host)
		if err != nil {
			return nil, err
		}
		// 如果代理URL是HTTPS，则进行TLS握手
		if proxyUrl.Scheme == "https" {
			// An https upstream is always verified: its credentials are sent
			// only after its certificate proves the endpoint. RootCAs comes
			// from the app's certificate settings and keeps the system pool.
			tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
			if proxyTLS != nil {
				tlsConfig = proxyTLS.Clone()
			}
			if tlsConfig.ServerName == "" {
				tlsConfig.ServerName = proxyUrl.Hostname()
			}
			// 包装原始连接为TLS连接
			tlsConn := tls.Client(conn, tlsConfig)
			// 执行TLS握手
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				conn.Close() // 握手失败，关闭连接
				return nil, err
			}
			conn = tlsConn // 使用TLS连接替换原始连接
		}
		connectReq := &http.Request{
			Method: "CONNECT",
			URL:    &url.URL{Opaque: address},
			Host:   address,
			Header: http.Header{},
		}
		if proxyUrl.User != nil {
			// User.String() percent-encodes reserved characters in the
			// password; Basic authentication needs the raw pair.
			username := proxyUrl.User.Username()
			password, _ := proxyUrl.User.Password()
			connectReq.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
		}
		connectCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
		defer cancel()
		didReadResponse := make(chan struct{}) // closed after CONNECT write+read is done or fails
		var resp *http.Response
		// Write the CONNECT request & read the response.
		go func() {
			defer close(didReadResponse)
			err = connectReq.Write(conn)
			if err != nil {
				return
			}
			// Okay to use and discard buffered reader here, because
			// TLS server will not speak until spoken to.
			br := bufio.NewReader(conn)
			resp, err = http.ReadResponse(br, connectReq)
		}()
		select {
		case <-connectCtx.Done():
			conn.Close()
			<-didReadResponse
			return nil, connectCtx.Err()
		case <-didReadResponse:
			// resp or err now set
		}
		if err != nil {
			conn.Close()
			return nil, err
		}
		if resp.StatusCode != 200 {
			conn.Close()
			if resp.Status == "" {
				return nil, errors.New("unknown status code")
			}
			if resp.StatusCode == http.StatusProxyAuthRequired {
				return nil, fmt.Errorf("%w: %w: %s", ErrProxyResponse, ErrProxyAuth, resp.Status)
			}
			return nil, fmt.Errorf("%w: %s", ErrProxyResponse, resp.Status)
		}
		return conn, nil
	}
}

// isSOCKSAuthError recognizes x/net/proxy's authentication failures without
// exposing the username or password in the error.
func isSOCKSAuthError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "authentication") ||
		strings.Contains(text, "no acceptable authentication")
}
