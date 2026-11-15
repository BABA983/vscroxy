package request

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/gorilla/websocket"
)

// GraphQLSubscriptionTransports are the WebSocket subprotocols a GraphQL
// subscription can use.
var GraphQLSubscriptionTransports = []string{"graphql-ws", "graphql-transport-ws"}

// graphqlSubscribe sends one subscription and collects its payloads until the
// server completes it or the reply window closes.
func graphqlSubscribe(ctx context.Context, proxy string, req *Prepared, body GraphQLBody, vars map[string]string, wait time.Duration, ca []byte, certificates certs.Settings, jar http.CookieJar) ([]WebSocketExchange, []string, bool, error) {
	transport := strings.TrimSpace(body.Transport)
	if transport != "graphql-ws" && transport != "graphql-transport-ws" {
		return nil, nil, false, fmt.Errorf("GraphQL subscription transport %q is not graphql-ws or graphql-transport-ws", transport)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, nil, false, err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return nil, nil, false, fmt.Errorf("GraphQL subscriptions need an HTTP(S) or WS(S) URL")
	}
	query := ExpandVars(body.Query, vars)
	if strings.TrimSpace(query) == "" {
		return nil, nil, false, errors.New("enter a GraphQL subscription")
	}
	operationName, err := graphql.SelectOperation(query, body.OperationName)
	if err != nil {
		return nil, nil, false, err
	}
	ops, err := graphql.Operations(query)
	if err != nil {
		return nil, nil, false, err
	}
	selected := ""
	for _, op := range ops {
		if operationName == "" || op.Name == operationName {
			selected = op.Type
			break
		}
	}
	if selected != "subscription" {
		return nil, nil, false, errors.New("the selected GraphQL operation is not a subscription")
	}
	variables := strings.TrimSpace(ExpandVars(body.Variables, vars))
	if variables == "" {
		variables = "{}"
	}
	var decodedVars any
	if err := json.Unmarshal([]byte(variables), &decodedVars); err != nil {
		return nil, nil, false, fmt.Errorf("GraphQL variables must be a JSON object: %w", err)
	}
	header := req.Header.Clone()
	header.Set("Sec-WebSocket-Protocol", transport)
	conn, err := DialWebSocket(ctx, proxy, &Prepared{URL: u.String(), Header: header}, ca, certificates, jar)
	if err != nil {
		return nil, nil, false, err
	}
	defer conn.Close()
	conn.SetReadLimit(8 << 20)
	write := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return conn.WriteMessage(websocket.TextMessage, data)
	}
	read := func(deadline time.Time) (map[string]any, error) {
		conn.SetReadDeadline(deadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		var message map[string]any
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, fmt.Errorf("GraphQL server sent invalid JSON: %w", err)
		}
		return message, nil
	}
	ack := time.Now().Add(10 * time.Second)
	if err := write(map[string]any{"type": "connection_init", "payload": map[string]any{}}); err != nil {
		return nil, nil, false, err
	}
	for {
		message, err := read(ack)
		if err != nil {
			return nil, nil, false, fmt.Errorf("GraphQL connection init: %w", err)
		}
		switch message["type"] {
		case "connection_ack":
		case "ka":
			// subscriptions-transport-ws sends keep-alives and answers an
			// unknown type with an error; only graphql-transport-ws pings
			// expect a pong.
			continue
		case "ping":
			if transport == "graphql-transport-ws" {
				_ = write(map[string]any{"type": "pong"})
			}
			continue
		case "connection_error", "error":
			return nil, nil, false, fmt.Errorf("GraphQL connection rejected: %v", message["payload"])
		default:
			continue
		}
		break
	}
	payload := map[string]any{"query": query, "variables": decodedVars}
	if operationName != "" {
		payload["operationName"] = operationName
	}
	startType := "subscribe"
	stopType := "complete"
	if transport == "graphql-ws" {
		startType, stopType = "start", "stop"
	}
	if err := write(map[string]any{"id": "1", "type": startType, "payload": payload}); err != nil {
		return nil, nil, false, err
	}
	exchange := WebSocketExchange{Message: WebSocketMessage{Payload: string(mustJSON(map[string]any{"type": startType, "payload": payload}))}}
	var replies []string
	size := 0
	deadline := time.Now().Add(wait)
	for {
		message, err := read(deadline)
		if err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				break
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				break
			}
			return []WebSocketExchange{exchange}, replies, false, err
		}
		switch message["type"] {
		case "next", "data":
			data, err := json.Marshal(message["payload"])
			if err != nil {
				return []WebSocketExchange{exchange}, replies, false, err
			}
			replies = append(replies, string(data))
			exchange.Replies = append(exchange.Replies, string(data))
			size += len(data)
			if len(replies) >= maxWSReplies || size >= maxWSReplyBytes {
				_ = write(map[string]any{"id": "1", "type": stopType})
				return []WebSocketExchange{exchange}, replies, true, nil
			}
		case "complete":
			_ = write(map[string]any{"id": "1", "type": stopType})
			return []WebSocketExchange{exchange}, replies, false, nil
		case "error":
			return []WebSocketExchange{exchange}, replies, false, fmt.Errorf("GraphQL subscription failed: %v", message["payload"])
		case "ka":
			// Legacy keep-alive, no answer.
		case "ping":
			if transport == "graphql-transport-ws" {
				_ = write(map[string]any{"type": "pong"})
			}
		}
	}
	_ = write(map[string]any{"id": "1", "type": stopType})
	return []WebSocketExchange{exchange}, replies, false, nil
}

func mustJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

// Wait is the subscription's reply window.
func (g GraphQLBody) Wait() time.Duration {
	wait := time.Duration(g.WaitMS) * time.Millisecond
	switch {
	case wait <= 0:
		return 5 * time.Second
	case wait > MaxWebSocketWait:
		return MaxWebSocketWait
	}
	return wait
}
