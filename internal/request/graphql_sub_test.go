package request

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestGraphQLSubscriptionTransportWS(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{"graphql-transport-ws"}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var init map[string]any
		if err := conn.ReadJSON(&init); err != nil || init["type"] != "connection_init" {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "connection_ack"})
		var start map[string]any
		if err := conn.ReadJSON(&start); err != nil || start["type"] != "subscribe" {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "next", "id": "1", "payload": map[string]any{"data": map[string]any{"tick": 1}}})
		_ = conn.WriteJSON(map[string]any{"type": "next", "id": "1", "payload": map[string]any{"data": map[string]any{"tick": 2}}})
		_ = conn.WriteJSON(map[string]any{"type": "complete", "id": "1"})
	}))
	defer upstream.Close()
	r := Request{Method: "POST", URL: upstream.URL + "/graphql", BodyMode: "GraphQL",
		GraphQL: GraphQLBody{Query: "subscription { tick }", Transport: "graphql-transport-ws", WaitMS: 2000}}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if len(out.Replies) != 2 || !strings.Contains(out.Replies[0], `"tick":1`) || !strings.Contains(out.Replies[1], `"tick":2`) {
		t.Fatalf("replies = %q", out.Replies)
	}
	if out.Response == nil || out.Response.Status != http.StatusSwitchingProtocols || len(out.WebSocket) != 1 {
		t.Fatalf("response = %+v, exchanges = %+v", out.Response, out.WebSocket)
	}
}

func TestGraphQLSubscriptionLegacyWS(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{"graphql-ws"}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var init map[string]any
		if err := conn.ReadJSON(&init); err != nil || init["type"] != "connection_init" {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "connection_ack"})
		var start map[string]any
		if err := conn.ReadJSON(&start); err != nil || start["type"] != "start" {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "data", "id": "1", "payload": map[string]any{"data": map[string]any{"tick": 3}}})
		_ = conn.WriteJSON(map[string]any{"type": "complete", "id": "1"})
	}))
	defer upstream.Close()
	r := Request{Method: "POST", URL: upstream.URL + "/graphql", BodyMode: "GraphQL",
		GraphQL: GraphQLBody{Query: "subscription { tick }", Transport: "graphql-ws", WaitMS: 2000}}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if len(out.Replies) != 1 || !strings.Contains(out.Replies[0], `"tick":3`) {
		t.Fatalf("replies = %q", out.Replies)
	}
}

func TestGraphQLSubscriptionRejectsNonSubscription(t *testing.T) {
	r := Request{Method: "POST", URL: "https://example.test/graphql", BodyMode: "GraphQL",
		GraphQL: GraphQLBody{Query: "query { tick }", Transport: "graphql-transport-ws", WaitMS: 100}}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "not a subscription") {
		t.Fatalf("err = %v", out.Err)
	}
}

func TestGraphQLSubscriptionLegacyKeepAlive(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{"graphql-ws"}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var init map[string]any
		if err := conn.ReadJSON(&init); err != nil || init["type"] != "connection_init" {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "connection_ack"})
		_ = conn.WriteJSON(map[string]any{"type": "ka"})
		var start map[string]any
		if err := conn.ReadJSON(&start); err != nil || start["type"] != "start" {
			// subscriptions-transport-ws has no pong: an answer to ka is an
			// unknown message type and fails the connection.
			_ = conn.WriteJSON(map[string]any{"type": "error", "payload": "unknown message type"})
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "data", "id": "1", "payload": map[string]any{"data": map[string]any{"tick": 4}}})
		_ = conn.WriteJSON(map[string]any{"type": "complete", "id": "1"})
	}))
	defer upstream.Close()
	r := Request{Method: "POST", URL: upstream.URL + "/graphql", BodyMode: "GraphQL",
		GraphQL: GraphQLBody{Query: "subscription { tick }", Transport: "graphql-ws", WaitMS: 2000}}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if len(out.Replies) != 1 || !strings.Contains(out.Replies[0], `"tick":4`) {
		t.Fatalf("replies = %q", out.Replies)
	}
}

func TestGraphQLSubscriptionTruncatesReplies(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{"graphql-transport-ws"}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var init map[string]any
		if err := conn.ReadJSON(&init); err != nil || init["type"] != "connection_init" {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "connection_ack"})
		var start map[string]any
		if err := conn.ReadJSON(&start); err != nil || start["type"] != "subscribe" {
			return
		}
		for i := 0; i < maxWSReplies+50; i++ {
			if err := conn.WriteJSON(map[string]any{"type": "next", "id": "1", "payload": map[string]any{"data": map[string]any{"tick": i}}}); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	r := Request{Method: "POST", URL: upstream.URL + "/graphql", BodyMode: "GraphQL",
		GraphQL: GraphQLBody{Query: "subscription { tick }", Transport: "graphql-transport-ws", WaitMS: 2000}}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if len(out.Replies) != maxWSReplies {
		t.Fatalf("kept %d replies, want %d", len(out.Replies), maxWSReplies)
	}
	if out.Response == nil || out.Response.ResponseHeader.Get("X-Vscroxy-Truncated") != "true" {
		t.Fatalf("truncation header = %q", out.Response.ResponseHeader.Get("X-Vscroxy-Truncated"))
	}
}
