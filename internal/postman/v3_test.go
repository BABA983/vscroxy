package postman_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
)

func readV3(t *testing.T, dir string) postman.Collection {
	t.Helper()
	col, err := postman.ReadCollectionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return col
}

func v3Folder(t *testing.T, col postman.Collection, path string) request.Folder {
	t.Helper()
	for _, folder := range col.Folders {
		if folder.Path == path {
			return folder
		}
	}
	t.Fatalf("folder %q not found; have %+v", path, col.Folders)
	return request.Folder{}
}

func v3Request(t *testing.T, col postman.Collection, collection, name string) request.Request {
	t.Helper()
	for _, r := range col.Requests {
		if r.Collection == collection && r.Name == name {
			return r
		}
	}
	var have []string
	for _, r := range col.Requests {
		have = append(have, r.Collection+"/"+r.Name)
	}
	t.Fatalf("request %q in %q not found; have %q", name, collection, have)
	return request.Request{}
}

func writeV3(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadCollectionDirV3(t *testing.T) {
	col := readV3(t, filepath.Join("testdata", "v3", "shop"))
	if col.Name != "Shop API" {
		t.Fatalf("name = %q, want Shop API", col.Name)
	}
	if len(col.Requests) != 25 {
		t.Fatalf("requests = %d, want 25", len(col.Requests))
	}
	if len(col.Folders) != 2 {
		t.Fatalf("folders = %+v, want the collection and Orders", col.Folders)
	}

	root := v3Folder(t, col, "Shop API")
	if !strings.Contains(root.Description, "Storefront endpoints.") || !strings.Contains(root.Description, "Postman v3 directory") {
		t.Errorf("collection description = %q", root.Description)
	}
	if root.Auth.Type != "Bearer" || root.Auth.Token != "{{token}}" {
		t.Errorf("collection auth = %+v", root.Auth)
	}
	if !strings.Contains(root.Pre, "pm.variables.set('ts'") || !strings.Contains(root.Post, "pm.test('ok'") {
		t.Errorf("collection scripts = %q / %q", root.Pre, root.Post)
	}
	wantVars := []request.Variable{
		{Name: "baseUrl", Initial: "https://shop.example.com"},
		{Name: "pageSize", Initial: "20"},
		{Name: "legacy", Initial: "v1", Disabled: true},
	}
	if !reflect.DeepEqual(root.Variables, wantVars) {
		t.Errorf("collection variables = %+v, want %+v", root.Variables, wantVars)
	}

	orders := v3Folder(t, col, "Shop API/Orders")
	if orders.Description != "Order and return flows." || orders.Auth.Type != "None" || !strings.Contains(orders.Pre, "console.log('orders')") {
		t.Errorf("Orders folder = %+v", orders)
	}
	if !reflect.DeepEqual(orders.Variables, []request.Variable{{Name: "region", Initial: "eu"}}) {
		t.Errorf("Orders variables = %+v", orders.Variables)
	}

	list := v3Request(t, col, "Shop API/Orders", "List orders")
	if list.Method != "GET" || list.URL != "{{baseUrl}}/orders?status=open" {
		t.Errorf("List orders = %s %s", list.Method, list.URL)
	}
	if list.Auth.Type != "Bearer" || list.Auth.Token != "{{token}}" {
		t.Errorf("List orders auth = %+v", list.Auth)
	}
	if !strings.Contains(list.Pre, "console.log('list')") || !strings.Contains(list.Post, "pm.test('ok'") {
		t.Errorf("List orders scripts = %q / %q", list.Pre, list.Post)
	}
	if list.Description != "Lists orders." {
		t.Errorf("List orders description = %q", list.Description)
	}
	if list.Settings.FollowRedirects == nil || *list.Settings.FollowRedirects {
		t.Errorf("List orders followRedirects = %v", list.Settings.FollowRedirects)
	}
	if list.Settings.MaxRedirects != nil {
		t.Errorf("maxRedirects 0 should mean do not follow, got %v", *list.Settings.MaxRedirects)
	}
	if list.Settings.VerifyTLS == nil || *list.Settings.VerifyTLS {
		t.Errorf("List orders strictSSL = %v", list.Settings.VerifyTLS)
	}
	wantHeaders := []request.Field{{Name: "Accept", Value: "application/json"}, {Name: "X-Debug", Value: "1", Disabled: true}}
	if !reflect.DeepEqual(list.HeaderList, wantHeaders) || list.Headers != "Accept: application/json" {
		t.Errorf("List orders headers = %+v / %q", list.HeaderList, list.Headers)
	}

	create := v3Request(t, col, "Shop API/Orders/Nested", "Create order")
	if create.Method != "POST" || create.BodyMode != "Raw" || create.Body != `{"sku":"{{sku}}"}` {
		t.Errorf("Create order = %+v", create)
	}
	if create.Description != "Creates an order." {
		t.Errorf("Create order description = %q", create.Description)
	}
	if len(create.HeaderList) != 1 || create.HeaderList[0] != (request.Field{Name: "Content-Type", Value: "application/json"}) {
		t.Errorf("Create order headers = %+v", create.HeaderList)
	}

	get := v3Request(t, col, "Shop API/Users", "Get user")
	if get.URL != "{{baseUrl}}/users/42" {
		t.Errorf("path variable URL = %q, want the :id segment filled", get.URL)
	}

	health := v3Request(t, col, "Shop API", "Health/check")
	if health.URL != "{{baseUrl}}/health" {
		t.Errorf("Health/check = %+v", health)
	}
}

func TestReadCollectionDirV3Examples(t *testing.T) {
	col := readV3(t, filepath.Join("testdata", "v3", "shop"))
	list := v3Request(t, col, "Shop API/Orders", "List orders")
	if len(list.Examples) != 2 {
		t.Fatalf("examples = %d, want 2", len(list.Examples))
	}
	open := list.Examples[0]
	if open.ID != "ex-open" || open.Name != "Open orders" || open.Status != 200 {
		t.Errorf("open example = %+v", open)
	}
	if open.Headers != "Content-Type: application/json" || open.Body != `[{"id":1}]` {
		t.Errorf("open example response = %q / %q", open.Headers, open.Body)
	}
	if open.Request.Method != "GET" || open.Request.URL != "{{baseUrl}}/orders?status=open" || open.Request.Headers != "Accept: application/json" {
		t.Errorf("open example request = %+v", open.Request)
	}
	missing := list.Examples[1]
	if missing.ID != "ex-missing" || missing.Name != "Not found" || missing.Status != 404 || missing.Body != `{"error":"missing"}` {
		t.Errorf("missing example = %+v", missing)
	}
}

func TestReadCollectionDirV3Bodies(t *testing.T) {
	col := readV3(t, filepath.Join("testdata", "v3", "shop"))

	text := v3Request(t, col, "Shop API/Bodies", "Text body")
	if text.BodyMode != "Raw" || text.Body != "hello" || text.Headers != "Content-Type: text/plain" {
		t.Errorf("text body = %+v", text)
	}
	xml := v3Request(t, col, "Shop API/Bodies", "XML body")
	if xml.BodyMode != "Raw" || xml.Body != `<order id="1"/>` || xml.Headers != "Content-Type: application/xml" {
		t.Errorf("XML body = %+v", xml)
	}
	form := v3Request(t, col, "Shop API/Bodies", "URL encoded body")
	if form.BodyMode != "URL encoded" {
		t.Errorf("urlencoded mode = %q", form.BodyMode)
	}
	wantForm := []request.Field{
		{Name: "grant_type", Value: "client_credentials"},
		{Name: "scope", Value: "orders"},
		{Name: "off", Value: "1", Disabled: true},
	}
	if !reflect.DeepEqual(form.Form, wantForm) {
		t.Errorf("urlencoded fields = %+v, want %+v", form.Form, wantForm)
	}
	multipart := v3Request(t, col, "Shop API/Bodies", "Multipart body")
	if multipart.BodyMode != "Multipart" {
		t.Errorf("multipart mode = %q", multipart.BodyMode)
	}
	wantParts := []request.Field{
		{Name: "note", Value: "hello"},
		{Name: "file", Value: "./payload.bin", File: true},
	}
	if !reflect.DeepEqual(multipart.Form, wantParts) {
		t.Errorf("multipart fields = %+v, want %+v", multipart.Form, wantParts)
	}
	binary := v3Request(t, col, "Shop API/Bodies", "Binary body")
	if binary.BodyMode != "Binary" || binary.BodyFile != "./payload.bin" {
		t.Errorf("binary body = %+v", binary)
	}
	graphql := v3Request(t, col, "Shop API/Bodies", "GraphQL body")
	if graphql.BodyMode != "GraphQL" || graphql.GraphQL.Query != "query Q($id: ID!) { order(id: $id) { id } }" ||
		graphql.GraphQL.Variables != `{"id":"1"}` || graphql.GraphQL.OperationName != "Q" {
		t.Errorf("GraphQL body = %+v", graphql.GraphQL)
	}
}

func TestReadCollectionDirV3Auth(t *testing.T) {
	col := readV3(t, filepath.Join("testdata", "v3", "shop"))
	for _, test := range []struct {
		name  string
		check func(*testing.T, request.Auth)
	}{
		{"Basic", func(t *testing.T, a request.Auth) {
			if a.Type != "Basic" || a.Username != "user" || a.Password != "pass" {
				t.Errorf("basic = %+v", a)
			}
		}},
		{"API key", func(t *testing.T, a request.Auth) {
			if a.Type != "API key" || a.Key != "api_key" || a.Value != "{{key}}" || a.Location != "Query" {
				t.Errorf("API key = %+v", a)
			}
		}},
		{"OAuth 2.0", func(t *testing.T, a request.Auth) {
			if a.Type != request.AuthOAuth2 || a.OAuth2.Grant != request.GrantClientCredentials ||
				a.OAuth2.TokenURL != "https://id.example.com/token" || a.OAuth2.ClientID != "client" ||
				a.OAuth2.ClientSecret != "{{secret}}" || a.OAuth2.Scope != "orders" {
				t.Errorf("OAuth 2.0 = %+v", a)
			}
		}},
		{"Digest", func(t *testing.T, a request.Auth) {
			if a.Type != request.AuthDigest || a.Username != "user" || a.Password != "pass" {
				t.Errorf("digest = %+v", a)
			}
		}},
		{"AWS Signature", func(t *testing.T, a request.Auth) {
			if a.Type != request.AuthAWS || a.AWS.AccessKey != "AKIA" || a.AWS.SecretKey != "{{secret}}" ||
				a.AWS.Region != "us-east-1" || a.AWS.Service != "execute-api" {
				t.Errorf("AWS = %+v", a)
			}
		}},
		{"JWT", func(t *testing.T, a request.Auth) {
			if a.Type != request.AuthJWT || a.JWT.Algorithm != "HS256" || a.JWT.Secret != "{{secret}}" || a.JWT.Payload != `{"sub":"1"}` {
				t.Errorf("JWT = %+v", a)
			}
		}},
		{"OAuth 1.0", func(t *testing.T, a request.Auth) {
			if a.Type != request.AuthOAuth1 || a.OAuth1.ConsumerKey != "consumer" || a.OAuth1.ConsumerSecret != "{{secret}}" ||
				a.OAuth1.Token != "token" || a.OAuth1.TokenSecret != "{{secret}}" || a.OAuth1.SignatureMethod != "HMAC-SHA1" {
				t.Errorf("OAuth 1.0 = %+v", a)
			}
		}},
		{"Hawk", func(t *testing.T, a request.Auth) {
			if a.Type != request.AuthHawk || a.Hawk.AuthID != "hawk-id" || a.Hawk.AuthKey != "{{secret}}" || a.Hawk.Algorithm != "SHA256" {
				t.Errorf("Hawk = %+v", a)
			}
		}},
		{"EdgeGrid", func(t *testing.T, a request.Auth) {
			if a.Type != request.AuthEdgeGrid || a.EdgeGrid.AccessToken != "access" || a.EdgeGrid.ClientToken != "client" ||
				a.EdgeGrid.ClientSecret != "{{secret}}" || a.EdgeGrid.BaseURL != "https://akamai.example.com" {
				t.Errorf("EdgeGrid = %+v", a)
			}
		}},
		{"NTLM", func(t *testing.T, a request.Auth) {
			if a.Type != "" {
				t.Errorf("NTLM should import as no auth, got %+v", a)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := v3Request(t, col, "Shop API/Auth", test.name)
			test.check(t, r.Auth)
		})
	}
}

func TestReadCollectionDirV3Protocols(t *testing.T) {
	col := readV3(t, filepath.Join("testdata", "v3", "shop"))

	graphql := v3Request(t, col, "Shop API", "GraphQL request")
	if graphql.Method != "POST" || graphql.BodyMode != "GraphQL" || graphql.URL != "{{baseUrl}}/graphql" {
		t.Errorf("GraphQL request = %+v", graphql)
	}
	if graphql.GraphQL.Query != "query Q($id: ID!) { order(id: $id) { id } }" || graphql.GraphQL.Variables != `{"id":"1"}` ||
		graphql.GraphQL.OperationName != "Q" {
		t.Errorf("GraphQL body = %+v", graphql.GraphQL)
	}
	if !strings.Contains(graphql.GraphQL.Schema, "type Query") || graphql.GraphQL.SchemaSource != "./schema.graphql" {
		t.Errorf("GraphQL schema = %q from %q", graphql.GraphQL.Schema, graphql.GraphQL.SchemaSource)
	}
	if graphql.Auth.Type != "Basic" || !strings.Contains(graphql.Pre, "console.log('gql')") || graphql.Headers != "X-Trace: 1" {
		t.Errorf("GraphQL request metadata = %+v", graphql)
	}

	grpc := v3Request(t, col, "Shop API", "gRPC request")
	if grpc.Protocol != "gRPC" || grpc.Method != "POST" || grpc.GRPC.Method != "demo.Echo/Say" || grpc.Body != `{"name":"demo"}` || grpc.BodyMode != "Raw" {
		t.Errorf("gRPC request = %+v", grpc)
	}
	if grpc.Headers != "x-tag: one" || grpc.Auth.Type != "Bearer" || grpc.Auth.Token != "{{token}}" {
		t.Errorf("gRPC metadata = %+v / %+v", grpc.Headers, grpc.Auth)
	}
	if grpc.Settings.VerifyTLS == nil || *grpc.Settings.VerifyTLS {
		t.Errorf("gRPC strictSSL = %v", grpc.Settings.VerifyTLS)
	}

	ws := v3Request(t, col, "Shop API", "WebSocket request")
	if ws.Protocol != "" || ws.Method != "GET" || ws.URL != "wss://ws.example.com/stream" || ws.Body != `{"op":"subscribe"}` || ws.BodyMode != "Raw" {
		t.Errorf("WebSocket request = %+v", ws)
	}
	if ws.Headers != "X-Trace: 1" || ws.Auth.Type != "Bearer" || ws.Settings.VerifyTLS == nil || *ws.Settings.VerifyTLS {
		t.Errorf("WebSocket metadata = %+v", ws)
	}

	sio := v3Request(t, col, "Shop API", "Socket.IO request")
	if sio.Protocol != "Socket.IO" || sio.Method != "GET" || sio.URL != "https://api.example.com/socket.io/" {
		t.Errorf("Socket.IO request = %+v", sio)
	}
	if sio.Body != `[{"text":"hi"}]` || sio.BodyMode != "Raw" || sio.SocketIO.Event != "chat" || !sio.SocketIO.WantACK {
		t.Errorf("Socket.IO message = %+v", sio)
	}
	if sio.Settings.VerifyTLS == nil || *sio.Settings.VerifyTLS {
		t.Errorf("Socket.IO strictSSL = %v", sio.Settings.VerifyTLS)
	}

	mqtt := v3Request(t, col, "Shop API", "MQTT request")
	if mqtt.Protocol != "MQTT" || mqtt.Method != "GET" || mqtt.URL != "mqtts://broker.example.com:8883" {
		t.Errorf("MQTT request = %+v", mqtt)
	}
	if mqtt.MQTT.Version != request.MQTT5 || mqtt.MQTT.ClientID != "vscroxy-import" {
		t.Errorf("MQTT connection = %+v", mqtt.MQTT)
	}
	if mqtt.Auth.Type != "Basic" || mqtt.Auth.Username != "demo" || mqtt.Auth.Password != "secret" {
		t.Errorf("MQTT credentials = %+v", mqtt.Auth)
	}
	if mqtt.Settings.VerifyTLS == nil || *mqtt.Settings.VerifyTLS {
		t.Errorf("MQTT strictSSL = %v", mqtt.Settings.VerifyTLS)
	}
	if mqtt.MQTT.CleanStart == nil || *mqtt.MQTT.CleanStart || mqtt.MQTT.KeepAlive != 30 ||
		mqtt.MQTT.AutoReconnect == nil || *mqtt.MQTT.AutoReconnect || mqtt.MQTT.ConnectTimeoutMs != 5000 {
		t.Errorf("MQTT settings = %+v", mqtt.MQTT)
	}
	if len(mqtt.MQTT.Topics) != 2 {
		t.Fatalf("MQTT topics = %+v", mqtt.MQTT.Topics)
	}
	topic := mqtt.MQTT.Topics[0]
	if topic.Name != "sensors/+/temp" || topic.QoS != 1 || topic.Subscribe == nil || !*topic.Subscribe ||
		topic.Description != "Kitchen sensors" || !topic.NoLocal || !topic.RetainAsPublished ||
		topic.RetainHandling != 1 || topic.SubscriptionIdentifier != 7 ||
		len(topic.UserProperties) != 1 || topic.UserProperties[0].Key != "team" || topic.UserProperties[0].Value != "telemetry" {
		t.Errorf("MQTT topic = %+v", topic)
	}
	if second := mqtt.MQTT.Topics[1]; second.Name != "commands/#" || second.QoS != 2 || second.Subscribe == nil || *second.Subscribe {
		t.Errorf("MQTT second topic = %+v", second)
	}
	will := mqtt.MQTT.Will
	if !will.Enabled || will.Topic != "clients/gone" || will.Payload != `{"state":"offline"}` || will.Type != "json" ||
		will.QoS != 1 || !will.Retain || !will.Properties.PayloadFormat || will.Properties.MessageExpiry != 60 ||
		will.Properties.WillDelay != 30 || will.Properties.ResponseTopic != "replies/gone" ||
		will.Properties.CorrelationData != "corr-1" || will.Properties.ContentType != "application/json" ||
		len(will.Properties.UserProperties) != 1 || will.Properties.UserProperties[0].Value != "vscroxy" {
		t.Errorf("MQTT will = %+v", will)
	}
	props := mqtt.MQTT.Properties
	if props.SessionExpiry != 120 || props.ReceiveMaximum != 20 || props.MaximumPacketSize != 65536 ||
		!props.RequestResponseInformation || len(props.UserProperties) != 1 || props.UserProperties[0].Value != "demo" {
		t.Errorf("MQTT connect properties = %+v", props)
	}
	if len(mqtt.MQTT.Messages) != 1 {
		t.Fatalf("MQTT messages = %+v", mqtt.MQTT.Messages)
	}
	message := mqtt.MQTT.Messages[0]
	if message.Name != "Publish temperature" || message.Topic != "sensors/kitchen/temp" || message.Payload != `{"celsius":21.4}` ||
		message.Type != "json" || message.QoS != 1 || !message.Retain {
		t.Errorf("MQTT message = %+v", message)
	}
	if !message.Properties.PayloadFormat || message.Properties.MessageExpiry != 15 || message.Properties.TopicAlias != 3 ||
		message.Properties.ResponseTopic != "replies/temp" || message.Properties.CorrelationData != "abc" ||
		message.Properties.ContentType != "application/json" || len(message.Properties.UserProperties) != 1 ||
		message.Properties.UserProperties[0].Value != "c" {
		t.Errorf("MQTT message properties = %+v", message.Properties)
	}
}

func TestReadCollectionDirV3Warnings(t *testing.T) {
	col := readV3(t, filepath.Join("testdata", "v3", "shop"))
	want := []string{
		"1 request uses NTLM auth, which needs one connection for its challenge; imported as no auth",
		"Dropped 1 Socket.IO subscription event; vscroxy shows every event the server sends",
		"Dropped 1 gRPC schema or method descriptor; load the proto in the gRPC editor",
		"Dropped descriptions or types on 1 parameter",
		"Dropped protocol profile, certificate or proxy settings on 3 items",
		"Imported the first auth set on 1 collection item where several are offered; vscroxy uses one",
		"Skipped 1 gRPC script; vscroxy does not run them",
		"Skipped 1 secret variable that references a vault",
	}
	got, want := slices.Clone(col.Warnings), slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("warnings =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestReadCollectionDirV3MalformedAndUnsafe(t *testing.T) {
	t.Run("truncated request", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Broken\n",
			"Good.request.yaml":          "$kind: http-request\nname: Good\nurl: https://example.com/good\n",
			"Bad.request.yaml":           "$kind: http-request\nname: Bad\nurl: [unterminated\n",
		})
		col := readV3(t, dir)
		if len(col.Requests) != 1 || col.Requests[0].Name != "Good" {
			t.Fatalf("requests = %+v", col.Requests)
		}
		if !slices.Contains(col.Warnings, "Skipped 1 unreadable or malformed file (first: Bad.request.yaml)") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})

	t.Run("truncated definition", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: [unterminated\n",
			"One.request.yaml":           "$kind: http-request\nname: One\nurl: https://example.com/one\n",
		})
		col := readV3(t, dir)
		if col.Name != filepath.Base(dir) || len(col.Requests) != 1 {
			t.Errorf("name = %q, requests = %+v", col.Name, col.Requests)
		}
		if !slices.Contains(col.Warnings, "Skipped 1 unreadable or malformed file (first: .resources/definition.yaml)") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})

	t.Run("wrong kind", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{".resources/definition.yaml": "$kind: environment\nname: Nope\n"})
		if _, err := postman.ReadCollectionDir(dir); err == nil || !strings.Contains(err.Error(), "want collection") {
			t.Fatalf("err = %v, want a collection-kind error", err)
		}
	})

	t.Run("symlink and traversal", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Links\n",
			"Safe.request.yaml":          "$kind: http-request\nname: Safe\nurl: https://example.com/safe\nexamples: ../../outside\n",
		})
		if err := os.Symlink(filepath.Join(dir, "Safe.request.yaml"), filepath.Join(dir, "Linked.request.yaml")); err != nil {
			t.Fatal(err)
		}
		col := readV3(t, dir)
		if len(col.Requests) != 1 || col.Requests[0].Name != "Safe" {
			t.Fatalf("requests = %+v", col.Requests)
		}
		if !slices.Contains(col.Warnings, "Skipped 2 symbolic links, non-regular files or paths outside the collection") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})

	t.Run("oversized request", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{".resources/definition.yaml": "$kind: collection\nname: Big\n"})
		big := "$kind: http-request\nname: Big\nurl: https://example.com/big\n#" + strings.Repeat("x", 8<<20)
		if err := os.WriteFile(filepath.Join(dir, "Big.request.yaml"), []byte(big), 0o644); err != nil {
			t.Fatal(err)
		}
		col := readV3(t, dir)
		if !slices.Contains(col.Warnings, "Skipped example, message or request files larger than 8 MiB") {
			t.Errorf("warnings = %q", col.Warnings)
		}
		if !slices.Contains(col.Warnings, "First skipped file: Big.request.yaml") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})

	t.Run("disabled query", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Query\n",
			"One.request.yaml": "$kind: http-request\nname: One\nurl: https://example.com/one?page=1\n" +
				"queryParams:\n  - key: page\n    value: '1'\n    disabled: true\n",
		})
		col := readV3(t, dir)
		if !slices.Contains(col.Warnings, "Dropped 1 disabled query parameter") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Alien\n",
			"One.request.yaml":           "$kind: alien-request\nname: One\n",
		})
		col := readV3(t, dir)
		if len(col.Requests) != 0 || !slices.Contains(col.Warnings, "Skipped 1 request of an unknown kind (alien-request)") {
			t.Errorf("requests = %+v, warnings = %q", col.Requests, col.Warnings)
		}
	})

	t.Run("socketio binary argument", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Streams\n",
			"One.request.yaml": "$kind: socket.io-request\nname: One\nurl: https://example.com/socket.io/\n" +
				"messages: ./.resources/One.resources/messages\n",
			".resources/One.resources/messages/blob.message.yaml": "$kind: socket.io-message\nname: blob\n" +
				"eventName: bytes\nargs:\n  - content: AAEC\n    contentType: base64\n",
		})
		col := readV3(t, dir)
		one := v3Request(t, col, "Streams", "One")
		if one.SocketIO.Event != "bytes" || one.Body != `["AAEC"]` {
			t.Errorf("Socket.IO message = %+v", one)
		}
		if !slices.Contains(col.Warnings, "Imported 1 Socket.IO binary argument as text") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})

	t.Run("secret value stays local", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{
			".resources/definition.yaml": "$kind: collection\nname: Secrets\n" +
				"variables:\n  - key: token\n    value: local-secret\n    secret: true\n",
			"One.request.yaml": "$kind: http-request\nname: One\nurl: https://example.com/one\n",
		})
		col := readV3(t, dir)
		root := v3Folder(t, col, "Secrets")
		if len(root.Variables) != 1 {
			t.Fatalf("variables = %+v", root.Variables)
		}
		v := root.Variables[0]
		if v.Name != "token" || v.Initial != "" || v.Current != "local-secret" || !v.Local || !v.Credential {
			t.Errorf("secret variable = %+v", v)
		}
		if !slices.Contains(col.Warnings, "Kept 1 secret variable value in this computer's Current values") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})

	t.Run("colliding folder names", func(t *testing.T) {
		dir := t.TempDir()
		writeV3(t, dir, map[string]string{
			".resources/definition.yaml":   "$kind: collection\nname: Root\n",
			"A/.resources/definition.yaml": "$kind: collection\nname: Same\n",
			"B/.resources/definition.yaml": "$kind: collection\nname: Same\n",
			"A/One.request.yaml":           "$kind: http-request\nname: One\nurl: https://example.com/one\n",
			"B/Two.request.yaml":           "$kind: http-request\nname: Two\nurl: https://example.com/two\n",
		})
		col := readV3(t, dir)
		if len(col.Requests) != 2 || col.Requests[0].Collection != "Root/Same" || col.Requests[1].Collection != "Root/Same (2)" {
			t.Errorf("requests = %+v", col.Requests)
		}
		if !slices.Contains(col.Warnings, "Renamed 1 folder whose display name collides with another") {
			t.Errorf("warnings = %q", col.Warnings)
		}
	})
}

func TestReadCollectionDirV3ExampleHeadersObject(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Examples\n",
		"One.request.yaml": "$kind: http-request\nname: One\nurl: https://example.com/one\n" +
			"examples: ./.resources/One.resources/examples\n",
		".resources/One.resources/examples/Open.example.yaml": "$kind: http-example\nname: Open\n" +
			"response:\n  statusCode: 200\n  statusText: OK\n  headers:\n    Content-Type: application/json\n" +
			"  body:\n    type: text\n    content: ok\n",
	})
	col := readV3(t, dir)
	one := v3Request(t, col, "Examples", "One")
	if len(one.Examples) != 1 || one.Examples[0].Headers != "Content-Type: application/json" {
		t.Errorf("examples = %+v", one.Examples)
	}
}

func TestReadCollectionDirV3BinaryStreamMessage(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: Streams\n",
		"One.request.yaml": "$kind: websocket-request\nname: One\nurl: wss://example.com/stream\n" +
			"messages: ./.resources/One.resources/messages\n",
		".resources/One.resources/messages/blob.message.yaml": "$kind: websocket-message\nname: blob\n" +
			"content: AAEC\ncontentType: base64\n",
	})
	col := readV3(t, dir)
	one := v3Request(t, col, "Streams", "One")
	if one.Body != "" {
		t.Errorf("binary message became a text body: %q", one.Body)
	}
	if !slices.Contains(col.Warnings, "Skipped 1 binary stream message; vscroxy sends text") {
		t.Errorf("warnings = %q", col.Warnings)
	}
}

func TestReadCollectionDirV3FileCountLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("writes more than 10000 files")
	}
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{".resources/definition.yaml": "$kind: collection\nname: Many\n"})
	body := []byte("$kind: http-request\nname: One\nurl: https://example.com/one\n")
	for i := range 10_001 {
		name := filepath.Join(dir, "r"+strconv.Itoa(i)+".request.yaml")
		if err := os.WriteFile(name, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	col := readV3(t, dir)
	if !slices.Contains(col.Warnings, "Stopped reading at the 10000-entry limit") {
		t.Errorf("warnings = %q", col.Warnings)
	}
}

func TestLooksLikeCollectionDir(t *testing.T) {
	if !postman.LooksLikeCollectionDir(filepath.Join("testdata", "v3", "shop")) {
		t.Error("the v3 fixture was not recognized")
	}
	if postman.LooksLikeCollectionDir(t.TempDir()) {
		t.Error("an empty directory was recognized as a collection")
	}
	if postman.LooksLikeCollectionDir(filepath.Join("testdata", "v3", "shop", "schema.graphql")) {
		t.Error("a file was recognized as a collection folder")
	}
}

func TestCollectionV3RoundTrip(t *testing.T) {
	col := readV3(t, filepath.Join("testdata", "v3", "shop"))
	data, warnings, err := postman.WriteCollection(col.Name, col.Requests, col.Folders)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Skipped 1 gRPC request; Postman Collection v2.1 only supports HTTP requests",
		"Skipped 1 Socket.IO request; Postman Collection v2.1 only supports HTTP requests",
		"Dropped 1 GraphQL schema; Postman collections cannot hold it",
	} {
		if !slices.Contains(warnings, want) {
			t.Errorf("export warnings %q do not contain %q", warnings, want)
		}
	}
	again, err := postman.ReadCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Requests) != 22 {
		t.Fatalf("round-tripped requests = %d, want 22 (gRPC and Socket.IO are v2.1 losses)", len(again.Requests))
	}
	byKey := map[string]request.Request{}
	for _, r := range again.Requests {
		byKey[r.Collection+"\x00"+r.Name] = r
	}
	for _, original := range col.Requests {
		if original.Protocol != "" {
			continue
		}
		key := original.Collection + "\x00" + original.Name
		got, ok := byKey[key]
		if !ok {
			t.Errorf("%s/%s disappeared in the round trip", original.Collection, original.Name)
			continue
		}
		if got.Method != original.Method || got.URL != original.URL || got.BodyMode != original.BodyMode ||
			got.Body != original.Body || got.BodyFile != original.BodyFile || got.Headers != original.Headers ||
			got.Pre != original.Pre || got.Post != original.Post || got.Description != original.Description {
			t.Errorf("%s/%s changed:\n got %+v\nwant %+v", original.Collection, original.Name, got, original)
		}
		if !reflect.DeepEqual(got.HeaderList, original.HeaderList) {
			t.Errorf("%s/%s header rows = %+v, want %+v", original.Collection, original.Name, got.HeaderList, original.HeaderList)
		}
		if !reflect.DeepEqual(normalizeRoundTripAuth(got.Auth), normalizeRoundTripAuth(original.Auth)) {
			t.Errorf("%s/%s auth = %+v, want %+v", original.Collection, original.Name, got.Auth, original.Auth)
		}
		if !reflect.DeepEqual(got.Settings, original.Settings) {
			t.Errorf("%s/%s settings = %+v, want %+v", original.Collection, original.Name, got.Settings, original.Settings)
		}
		if got.GraphQL.Query != original.GraphQL.Query || got.GraphQL.Variables != original.GraphQL.Variables ||
			got.GraphQL.OperationName != original.GraphQL.OperationName {
			t.Errorf("%s/%s GraphQL = %+v, want %+v", original.Collection, original.Name, got.GraphQL, original.GraphQL)
		}
		if original.GraphQL.Schema != "" && got.GraphQL.Schema != "" {
			t.Errorf("%s/%s kept a GraphQL schema in v2.1", original.Collection, original.Name)
		}
		if len(got.Examples) != len(original.Examples) {
			t.Errorf("%s/%s examples = %d, want %d", original.Collection, original.Name, len(got.Examples), len(original.Examples))
		}
	}
}

// normalizeRoundTripAuth applies the defaults a v2.1 export writes out, so a
// comparison checks the configured auth rather than whether a default was
// spelled explicitly.
func normalizeRoundTripAuth(a request.Auth) request.Auth {
	if a.Type == request.AuthJWT {
		if a.JWT.Algorithm == "" {
			a.JWT.Algorithm = "HS256"
		}
		if a.JWT.Payload == "" {
			a.JWT.Payload = "{}"
		}
		if a.JWT.Header == "" {
			a.JWT.Header = "{}"
		}
		if a.JWT.Prefix == "" {
			a.JWT.Prefix = "Bearer"
		}
		if a.JWT.QueryParam == "" {
			a.JWT.QueryParam = "token"
		}
	}
	if a.Type == request.AuthOAuth1 && a.OAuth1.Version == "" {
		a.OAuth1.Version = "1.0"
	}
	return a
}
