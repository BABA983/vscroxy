package imports

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func collectionNamed(t *testing.T, result Result, name string) Collection {
	t.Helper()
	for _, collection := range result.Collections {
		if collection.Name == name {
			return collection
		}
	}
	t.Fatalf("no collection %q in %+v", name, result.Collections)
	return Collection{}
}

func requestNamed(t *testing.T, collection Collection, name string) request.Request {
	t.Helper()
	for _, r := range collection.Requests {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no request %q in %+v", name, collection.Requests)
	return request.Request{}
}

func TestReadInsomniaV4(t *testing.T) {
	result, err := ReadInsomnia(fixture(t, "insomnia-v4.json"))
	if err != nil {
		t.Fatal(err)
	}
	collection := collectionNamed(t, result, "Shop API")
	if len(collection.Requests) != 4 {
		t.Fatalf("%d requests: %+v", len(collection.Requests), collection.Requests)
	}
	list := requestNamed(t, collection, "List orders")
	if list.Collection != "Shop API/Orders" || list.Method != "GET" || list.URL != "{{base_url}}/orders?page=1" ||
		list.Auth.Type != "Bearer" || list.Auth.Token != "{{token}}" {
		t.Errorf("List orders %+v", list)
	}
	if got := request.ParseHeaderBlock(list.Headers).Get("Accept"); got != "application/json" {
		t.Errorf("Accept %q", got)
	}
	create := requestNamed(t, collection, "Create order")
	if create.Collection != "Shop API/Orders/2026" || create.URL != "{{base_url}}/orders/{{order_id}}" ||
		create.BodyMode != "Raw" || create.Body != `{"item":"widget"}` || create.Auth.Type != request.AuthInherit {
		t.Errorf("Create order %+v", create)
	}
	if create.Settings.Follows() {
		t.Errorf("follow redirects off: %+v", create.Settings)
	}
	upload := requestNamed(t, collection, "Upload receipt")
	if upload.BodyMode != "Multipart" || len(upload.Form) != 2 || !upload.Form[1].File || upload.Form[1].Value != "/tmp/receipt.png" {
		t.Errorf("Upload receipt %+v", upload)
	}
	graph := requestNamed(t, collection, "Order graph")
	if graph.BodyMode != "GraphQL" || graph.GraphQL.Query != "query { order { id } }" || graph.GraphQL.Variables != `{"id":7}` {
		t.Errorf("Order graph %+v", graph)
	}
	var orders *request.Folder
	for i := range collection.Folders {
		if collection.Folders[i].Path == "Shop API/Orders" {
			orders = &collection.Folders[i]
		}
	}
	if orders == nil || orders.Auth.Type != "Basic" || orders.Auth.Username != "ada" || len(orders.Variables) != 1 || orders.Variables[0].Name != "team" {
		t.Fatalf("Orders folder %+v", collection.Folders)
	}
	if len(result.Environments) != 2 {
		t.Fatalf("environments %+v", result.Environments)
	}
	staging := result.Environments[1]
	if staging.Name != "Staging" || staging.Values["base_url"] != "https://staging.example.com" || staging.Values["token"] != "staging-token" {
		t.Errorf("Staging %+v", staging)
	}
	for _, want := range []string{"with a description", "with a script", "gRPC/WebSocket"} {
		if !strings.Contains(strings.Join(collection.Warnings, "\n"), want) {
			t.Errorf("warnings %q missing %q", collection.Warnings, want)
		}
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "cookie jars") {
		t.Errorf("result warnings %q missing cookie jars", result.Warnings)
	}
}

func TestReadInsomniaV5(t *testing.T) {
	result, err := ReadInsomnia(fixture(t, "insomnia-v5.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	collection := collectionNamed(t, result, "Flights")
	if len(collection.Requests) != 3 {
		t.Fatalf("%d requests: %+v", len(collection.Requests), collection.Requests)
	}
	list := requestNamed(t, collection, "List flights")
	if list.Collection != "Flights/Operations" || list.URL != "{{base_url}}/flights?origin=YYZ" || list.Auth.Token != "{{token}}" {
		t.Errorf("List flights %+v", list)
	}
	create := requestNamed(t, collection, "Create flight")
	if create.URL != "{{base_url}}/flights/{{flight_id}}" || create.Body != `{"number":"IA918"}` || create.Settings.Follows() {
		t.Errorf("Create flight %+v", create)
	}
	upload := requestNamed(t, collection, "Upload")
	if upload.BodyMode != "Multipart" || !upload.Form[1].File || upload.Form[1].Value != "/tmp/a.png" {
		t.Errorf("Upload %+v", upload)
	}
	if len(result.Environments) != 2 || result.Environments[1].Values["token"] != "staging-token" {
		t.Errorf("environments %+v", result.Environments)
	}
	for _, want := range []string{"with a description", "with a script"} {
		if !strings.Contains(strings.Join(collection.Warnings, "\n"), want) {
			t.Errorf("warnings %q missing %q", collection.Warnings, want)
		}
	}
}

func TestReadInsomniaRejectsOtherDocuments(t *testing.T) {
	for _, data := range []string{`{"openapi":"3.0.0"}`, `{"_type":"export","__export_format":3,"resources":[]}`, `not yaml: [`} {
		if _, err := ReadInsomnia([]byte(data)); err == nil {
			t.Errorf("accepted %q", data)
		}
	}
}

func TestReadHoppscotch(t *testing.T) {
	result, err := ReadHoppscotch(fixture(t, "hoppscotch.json"))
	if err != nil {
		t.Fatal(err)
	}
	collection := collectionNamed(t, result, "Echo")
	if len(collection.Requests) != 4 {
		t.Fatalf("%d requests: %+v", len(collection.Requests), collection.Requests)
	}
	list := requestNamed(t, collection, "List users")
	if list.Collection != "Echo/Users" || list.URL != "https://echo.example.com/users?page=1" ||
		list.Auth.Type != "Basic" || list.Auth.Username != "ada" {
		t.Errorf("List users %+v", list)
	}
	if !strings.Contains(list.Pre, `pm.variables.set("local", "v");`) {
		t.Errorf("request variables %q", list.Pre)
	}
	create := requestNamed(t, collection, "Create user")
	if create.BodyMode != "Raw" || create.Body != `{"name":"Ada"}` || create.Auth.Type != "API key" ||
		create.Auth.Key != "X-API-Key" || create.Auth.Location != "Header" {
		t.Errorf("Create user %+v", create)
	}
	upload := requestNamed(t, collection, "Upload")
	if upload.BodyMode != "Multipart" || !upload.Form[1].File {
		t.Errorf("Upload %+v", upload)
	}
	form := requestNamed(t, collection, "Form")
	if form.BodyMode != "URL encoded" || len(form.Form) != 2 || !form.Form[1].Disabled || form.Form[1].Name != "hidden" {
		t.Errorf("Form %+v", form)
	}
	for _, want := range []string{"collection headers were dropped", "request descriptions were dropped", "multipart file fields"} {
		if !strings.Contains(strings.Join(collection.Warnings, "\n"), want) {
			t.Errorf("warnings %q missing %q", collection.Warnings, want)
		}
	}
	if got := collection.Folders[0].Variables; len(got) != 1 || got[0].Name != "baseUrl" {
		t.Errorf("collection variables %+v", got)
	}
}

func TestReadHoppscotchEnvironment(t *testing.T) {
	envs, err := ReadHoppscotchEnvironment(fixture(t, "hoppscotch-env.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Environment{
		{Name: "Dev", Values: map[string]string{"baseURL": "https://dev.example.com", "token": "dev-token"}, Warnings: []string{"confidential variables were imported as ordinary local variables"}},
		{Name: "Prod", Values: map[string]string{"baseURL": "https://api.example.com"}},
	}
	if !reflect.DeepEqual(envs, want) {
		t.Fatalf("environments\n got %+v\nwant %+v", envs, want)
	}
}

func TestReadHoppscotchRejectsMalformed(t *testing.T) {
	for _, data := range []string{`{}`, `[]`, `[{"v":12,"name":"x","folders":[],"requests":[]}]`} {
		if _, err := ReadHoppscotch([]byte(data)); err == nil {
			t.Errorf("accepted %q", data)
		}
	}
}

func TestReadSwagger2(t *testing.T) {
	doc, warnings, err := ReadSwagger2(fixture(t, "swagger2.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.Title != "Pet Store" || len(doc.Servers) != 2 || doc.Servers[0].URL != "https://petstore.example.com/v1" {
		t.Errorf("doc %+v servers %+v", doc.Info, doc.Servers)
	}
	if doc.Paths.Value("/pets") == nil || doc.Paths.Value("/pets/{petId}/photo") == nil {
		t.Errorf("paths %+v", doc.Paths)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"request body", "form data", "file upload", "declare security", "consumes", "several schemes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q missing %q", warnings, want)
		}
	}
}

func TestReadSwagger2RejectsInvalid(t *testing.T) {
	for _, data := range []string{`{"swagger":"1.2","info":{"title":"x","version":"1"}}`, `{"swagger":"2.0"}`, `not yaml: [`} {
		if _, _, err := ReadSwagger2([]byte(data)); err == nil {
			t.Errorf("accepted %q", data)
		}
	}
}

func TestReadBrunoDir(t *testing.T) {
	result, err := ReadBrunoDir(filepath.Join("testdata", "bruno"))
	if err != nil {
		t.Fatal(err)
	}
	collection := collectionNamed(t, result, "Bruno Shop")
	if len(collection.Requests) != 6 {
		t.Fatalf("%d requests: %+v", len(collection.Requests), collection.Requests)
	}
	list := requestNamed(t, collection, "List users")
	if list.Collection != "Bruno Shop/Users" || list.URL != "{{baseUrl}}/users?page=1" || list.Auth.Type != request.AuthInherit {
		t.Errorf("List users %+v", list)
	}
	headers := request.ParseHeaderBlock(list.Headers)
	if headers.Get("X-Collection") != "yes" || headers.Get("X-Folder") != "users" || headers.Get("Accept") != "application/json" {
		t.Errorf("headers %q", list.Headers)
	}
	create := requestNamed(t, collection, "Create user")
	if create.BodyMode != "Raw" || create.Body != "{\n  \"name\": \"Ada\"\n}" || create.Checks.Status != "201" ||
		request.ParseHeaderBlock(create.Headers).Get("Content-Type") != "application/json" {
		t.Errorf("Create user %+v checks %+v", create, create.Checks)
	}
	if !strings.Contains(create.Pre, `pm.variables.set("local", "yes");`) {
		t.Errorf("request variables %q", create.Pre)
	}
	upload := requestNamed(t, collection, "Upload")
	absUpload, err := filepath.Abs(filepath.Join("testdata", "bruno", "files", "a.png"))
	if err != nil {
		t.Fatal(err)
	}
	if upload.BodyMode != "Multipart" || !upload.Form[1].File || upload.Form[1].Value != absUpload {
		t.Errorf("Upload %+v, want file %q", upload, absUpload)
	}
	binary := requestNamed(t, collection, "Binary")
	if binary.BodyMode != "Binary" || binary.BodyFile != "/tmp/b.bin" || request.ParseHeaderBlock(binary.Headers).Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Binary %+v", binary)
	}
	graph := requestNamed(t, collection, "Graph")
	if graph.BodyMode != "GraphQL" || graph.GraphQL.Query != "query { viewer { id } }" || graph.GraphQL.Variables != "{\n  \"id\": 1\n}" {
		t.Errorf("Graph %+v", graph)
	}
	ping := requestNamed(t, collection, "Ping")
	if ping.Collection != "Bruno Shop/Nested/Deep" {
		t.Errorf("Ping %+v", ping)
	}
	var users *request.Folder
	for i := range collection.Folders {
		if collection.Folders[i].Path == "Bruno Shop/Users" {
			users = &collection.Folders[i]
		}
	}
	if users == nil || users.Auth.Type != "Basic" || users.Auth.Username != "ada" || len(users.Variables) != 1 || users.Variables[0].Name != "team" {
		t.Fatalf("Users folder %+v", collection.Folders)
	}
	if len(result.Environments) != 1 || result.Environments[0].Name != "Dev" || result.Environments[0].Values["token"] != "dev-token" {
		t.Errorf("environments %+v", result.Environments)
	}
	if len(result.Environments[0].Warnings) != 1 || !strings.Contains(result.Environments[0].Warnings[0], "secret variable") {
		t.Errorf("environment warnings %+v", result.Environments[0].Warnings)
	}
	joined := strings.Join(collection.Warnings, "\n")
	for _, want := range []string{"collection and folder headers", "request descriptions were dropped", "scripts and tests use Bruno's API"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q missing %q", collection.Warnings, want)
		}
	}
}

func TestReadBrunoDirRejectsNonCollection(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadBrunoDir(dir); err == nil {
		t.Error("accepted a directory without bruno.json")
	}
	if err := os.WriteFile(filepath.Join(dir, "bruno.json"), []byte(`{"type":"folder"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBrunoDir(dir); err == nil {
		t.Error("accepted bruno.json that is not a collection")
	}
}

func TestAuthMappings(t *testing.T) {
	var warnings Tally
	oauth := authFromInsomnia(map[string]any{
		"type": "oauth2", "grantType": "client_credentials", "accessTokenUrl": "https://id.example.com/token",
		"clientId": "client", "clientSecret": "secret", "scope": "read", "credentialsInBody": true,
	}, &warnings)
	if oauth.Type != request.AuthOAuth2 || oauth.OAuth2.Grant != request.GrantClientCredentials || oauth.OAuth2.ClientAuth != "Body" || oauth.OAuth2.TokenURL != "https://id.example.com/token" {
		t.Errorf("Insomnia OAuth2 %+v", oauth)
	}
	aws := authFromInsomnia(map[string]any{"type": "iam", "accessKeyId": "AK", "secretAccessKey": "SK", "region": "eu-west-1", "service": "s3"}, &warnings)
	if aws.Type != request.AuthAWS || aws.AWS.AccessKey != "AK" || aws.AWS.Region != "eu-west-1" {
		t.Errorf("Insomnia IAM %+v", aws)
	}
	if got := authFromInsomnia(map[string]any{"type": "ntlm"}, &warnings); got.Type != "None" {
		t.Errorf("unsupported Insomnia auth %+v", got)
	}

	jwt := hoppAuth(map[string]any{
		"authType": "jwt", "authActive": true, "algorithm": "HS256", "secret": "s", "payload": `{"sub":"ada"}`,
		"addTo": "QUERY_PARAMS", "paramName": "token", "headerPrefix": "Bearer ",
	}, nil)
	if jwt.Type != request.AuthJWT || jwt.JWT.AddTo != "Query" || jwt.JWT.QueryParam != "token" || jwt.JWT.Algorithm != "HS256" {
		t.Errorf("Hoppscotch JWT %+v", jwt)
	}
	hoppOAuth := hoppAuth(map[string]any{
		"authType": "oauth-2", "authActive": true,
		"grantTypeInfo": map[string]any{"grantType": "AUTHORIZATION_CODE", "isPKCE": true, "authEndpoint": "https://id/auth", "tokenEndpoint": "https://id/token", "clientID": "id", "scopes": "read"},
	}, nil)
	if hoppOAuth.Type != request.AuthOAuth2 || hoppOAuth.OAuth2.Grant != request.GrantAuthorizationCodePKCE || hoppOAuth.OAuth2.AuthURL != "https://id/auth" {
		t.Errorf("Hoppscotch OAuth2 %+v", hoppOAuth)
	}

	var bruWarnings []string
	bruOAuth := bruAuth("oauth2", bruBlocks{"auth:oauth2": {{tag: "auth:oauth2", pairs: []bruPair{
		{key: "grant_type", value: "authorization_code"}, {key: "pkce", value: "true"},
		{key: "authorization_url", value: "https://id/auth"}, {key: "access_token_url", value: "https://id/token"},
		{key: "client_id", value: "id"}, {key: "callback_url", value: "http://127.0.0.1:7777/callback"},
	}}}}, &bruWarnings)
	if bruOAuth.Type != request.AuthOAuth2 || bruOAuth.OAuth2.Grant != request.GrantAuthorizationCodePKCE || bruOAuth.OAuth2.RedirectURL != "http://127.0.0.1:7777/callback" {
		t.Errorf("Bruno OAuth2 %+v", bruOAuth)
	}
	bruAWS := bruAuth("awsv4", bruBlocks{"auth:awsv4": {{tag: "auth:awsv4", pairs: []bruPair{
		{key: "accessKeyId", value: "AK"}, {key: "secretAccessKey", value: "SK"}, {key: "service", value: "execute-api"}, {key: "region", value: "us-east-1"},
	}}}}, &bruWarnings)
	if bruAWS.Type != request.AuthAWS || bruAWS.AWS.Service != "execute-api" {
		t.Errorf("Bruno AWS %+v", bruAWS)
	}
	if len(bruWarnings) != 0 {
		t.Errorf("Bruno auth warnings %q", bruWarnings)
	}
}

func TestReadHoppscotchWorkspaceArray(t *testing.T) {
	one := fixture(t, "hoppscotch.json")
	result, err := ReadHoppscotch(append(append([]byte("["), one...), ']'))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Collections) != 1 || result.Collections[0].Name != "Echo" || len(result.Collections[0].Requests) != 4 {
		t.Fatalf("result %+v", result)
	}
}

func TestHoppscotchGraphQLRequest(t *testing.T) {
	data := []byte(`{"v":12,"name":"GQL","requests":[{"v":"17","name":"Viewer","url":"https://api.example.com/graphql","query":"query { viewer { id } }","variables":{"id":1},"auth":{"authType":"bearer","authActive":true,"token":"t"}}]}`)
	result, err := ReadHoppscotch(data)
	if err != nil {
		t.Fatal(err)
	}
	r := result.Collections[0].Requests[0]
	if r.Method != "POST" || r.BodyMode != "GraphQL" || r.GraphQL.Query != "query { viewer { id } }" || r.GraphQL.Variables != `{"id":1}` || r.Auth.Token != "t" {
		t.Fatalf("request %+v", r)
	}
}

func TestReadInsomniaV5EnvironmentFile(t *testing.T) {
	result, err := ReadInsomnia([]byte(`type: environment.insomnia.rest/5.0
schema_version: "5.1"
name: Dev
environments:
  name: Dev
  data:
    base_url: https://dev.example.com
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Environments) != 1 || result.Environments[0].Name != "Dev" || result.Environments[0].Values["base_url"] != "https://dev.example.com" {
		t.Fatalf("result %+v", result)
	}
}

func TestReadSwagger2UnquotedVersion(t *testing.T) {
	doc, _, err := ReadSwagger2([]byte(`swagger: 2.0
info: {title: Tiny, version: "1"}
paths:
  /ping:
    get:
      responses: {"200": {description: ok}}
`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.Title != "Tiny" {
		t.Fatalf("doc %+v", doc.Info)
	}
}

func TestParseBruRejectsMalformedInput(t *testing.T) {
	for _, data := range []string{
		"get {\n  url: https://example.com\n",
		"meta {\n  name: x\n",
		"  indented: value\n",
	} {
		if _, _, err := parseBru([]byte(data)); err == nil {
			t.Errorf("accepted malformed .bru %q", data)
		}
	}
}

func TestInsomniaAuthDetails(t *testing.T) {
	var warnings Tally
	query := authFromInsomnia(map[string]any{"type": "apikey", "key": "k", "value": "v", "addTo": "queryParams"}, &warnings)
	if query.Type != "API key" || query.Location != "Query" {
		t.Errorf("query API key %+v", query)
	}
	cookie := authFromInsomnia(map[string]any{"type": "apikey", "key": "session", "value": "abc", "addTo": "cookie"}, &warnings)
	if cookie.Type != "API key" || cookie.Key != "Cookie" || cookie.Value != "session=abc" || cookie.Location != "Header" {
		t.Errorf("cookie API key %+v", cookie)
	}
	prefix := authFromInsomnia(map[string]any{"type": "bearer", "token": "abc", "prefix": "Token"}, &warnings)
	if prefix.Type != "API key" || prefix.Value != "Token abc" {
		t.Errorf("bearer prefix %+v", prefix)
	}
	noPrefix := authFromInsomnia(map[string]any{"type": "bearer", "token": "abc", "prefix": "NO_PREFIX"}, &warnings)
	if noPrefix.Type != "API key" || noPrefix.Value != "abc" {
		t.Errorf("NO_PREFIX bearer %+v", noPrefix)
	}
	if len(warnings.Lines()) != 3 {
		t.Errorf("warnings %q", warnings.Lines())
	}
}

func TestInsomniaNormalizesBodiesAndWarnsAboutTags(t *testing.T) {
	data := []byte(`{"_type":"export","__export_format":4,"resources":[
		{"_id":"wrk","_type":"workspace","name":"Templates"},
		{"_id":"req","_type":"request","parentId":"wrk","name":"Tagged","method":"POST","url":"{{ _.base_url }}/x",
		 "body":{"mimeType":"application/json","text":"{\"token\":\"{{ _.token }}\",\"when\":\"{% response 'body' %}\"}"}}
	]}`)
	result, err := ReadInsomnia(data)
	if err != nil {
		t.Fatal(err)
	}
	r := result.Collections[0].Requests[0]
	if r.Body != `{"token":"{{token}}","when":"{% response 'body' %}"}` {
		t.Errorf("body %q", r.Body)
	}
	if !strings.Contains(strings.Join(result.Collections[0].Warnings, "\n"), "template tags") {
		t.Errorf("warnings %q", result.Collections[0].Warnings)
	}
	var out request.Request
	var skips Tally
	insomniaBodyRequest(&out, &insomniaBody{MimeType: "application/graphql", Text: `{"query":"query { x(id: \"{{ _.id }}\") }"}`}, &skips)
	if out.GraphQL.Query != `query { x(id: "{{id}}") }` {
		t.Errorf("GraphQL query %q", out.GraphQL.Query)
	}
}

func TestInsomniaTagScannerChecksImportedStringsDirectly(t *testing.T) {
	tests := []struct {
		name       string
		request    request.Request
		folder     request.Folder
		env        Environment
		requestTag bool
		envTag     bool
	}{
		{name: "lone percent", request: request.Request{URL: "https://x.test/%20", Body: "100% done"}},
		{name: "URL", request: request.Request{URL: "https://x.test/{% url 'base' %}"}, requestTag: true},
		{name: "body", request: request.Request{Body: `{"when":"{% response 'body' %}"}`}, requestTag: true},
		{name: "header block", request: request.Request{Headers: "X-When: {% now %}"}, requestTag: true},
		{name: "header row", request: request.Request{HeaderList: []request.Field{{Name: "X-When", Value: "{% now %}"}}}, requestTag: true},
		{name: "form row", request: request.Request{Form: []request.Field{{Name: "when", Value: "{% now %}"}}}, requestTag: true},
		{name: "auth token", request: request.Request{Auth: request.Auth{Token: "{% response 'body' %}"}}, requestTag: true},
		{name: "auth oauth2", request: request.Request{Auth: request.Auth{OAuth2: request.OAuth2{ClientSecret: "{% now %}"}}}, requestTag: true},
		{name: "folder variable", folder: request.Folder{Variables: []request.Field{{Name: "when", Value: "{% now %}"}}}, requestTag: true},
		{name: "environment name", env: Environment{Name: "{% now %}"}, envTag: true},
		{name: "environment value", env: Environment{Name: "Dev", Values: map[string]string{"when": "{% now %}"}}, envTag: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := []request.Request{test.request}
			folders := []request.Folder{test.folder}
			if got := hasInsomniaTags(requests, folders); got != test.requestTag {
				t.Errorf("hasInsomniaTags = %v, want %v", got, test.requestTag)
			}
			envs := []Environment{test.env}
			if got := environmentsTagged(envs); got != test.envTag {
				t.Errorf("environmentsTagged = %v, want %v", got, test.envTag)
			}
		})
	}
}

func TestInsomniaV5CookieSettingsWarn(t *testing.T) {
	result, err := ReadInsomnia([]byte(`type: collection.insomnia.rest/5.0
schema_version: "5.1"
name: Cookies
collection:
  - url: https://x.test
    name: Get
    meta: {id: req_1}
    method: GET
    settings:
      followRedirects: global
      cookies:
        send: false
        store: false
`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(result.Collections[0].Warnings, "\n"), "request settings") {
		t.Errorf("warnings %q", result.Collections[0].Warnings)
	}
}

func TestHoppscotchUnsupportedRequestAuthWarns(t *testing.T) {
	data := []byte(`{"v":12,"name":"Auth","requests":[
		{"v":"17","name":"Hawk","method":"GET","endpoint":"https://x.test","auth":{"authType":"hawk","authActive":true}},
		{"v":"17","name":"Disabled","method":"GET","endpoint":"https://x.test","auth":{"authType":"basic","authActive":false,"username":"a","password":"b"}},
		{"v":"17","name":"JWT","method":"GET","endpoint":"https://x.test","auth":{"authType":"jwt","authActive":true,"algorithm":"EdDSA","secret":"s"}}
	]}`)
	result, err := ReadHoppscotch(data)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.Collections[0].Warnings, "\n")
	for _, want := range []string{"hawk auth", "disabled auth", "EdDSA"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q missing %q", result.Collections[0].Warnings, want)
		}
	}
}

func TestBruQuotedKeyMayContainAColon(t *testing.T) {
	blocks, _, err := parseBru([]byte("headers {\n  \"a:b\": 1\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	pairs := indexBru(blocks).pairs("headers")
	if len(pairs) != 1 || pairs[0].key != "a:b" || pairs[0].value != "1" {
		t.Fatalf("pairs %+v", pairs)
	}
}

func TestBrunoAssertOnlyMapsExactComparisons(t *testing.T) {
	var out request.Request
	var warnings []string
	brunoChecks(&out, []bruPair{
		{key: "res.status", value: "neq 200"},
		{key: "res.headers.Content-Type", value: "contains json"},
		{key: "res.status", value: "eq 201"},
		{key: "res.headers.X-Test", value: "ok"},
	}, &warnings)
	if out.Checks.Status != "201" || out.Checks.Headers != "X-Test: ok" {
		t.Fatalf("checks %+v", out.Checks)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"operator neq", "operator contains"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q missing %q", warnings, want)
		}
	}
}

func TestBruParseErrorsDoNotIncludeLineContents(t *testing.T) {
	_, _, err := parseBru([]byte("auth:bearer {\n  token: super-secret\n  not-a-pair\n}\n"))
	if err == nil {
		t.Fatal("accepted a malformed block")
	}
	if strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), "not-a-pair") {
		t.Fatalf("error leaked line contents: %v", err)
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("error has no line number: %v", err)
	}
}

func TestReadBrunoDirSkipsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bruno.json"), []byte(`{"version":"1","name":"Links","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.bru")
	if err := os.WriteFile(target, []byte("meta {\n  name: Target\n}\n\nget {\n  url: https://x.test\n  body: none\n  auth: none\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.bru")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	result, err := ReadBrunoDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Collections) != 1 || len(result.Collections[0].Requests) != 0 {
		t.Fatalf("requests %+v", result.Collections)
	}
	if !strings.Contains(strings.Join(result.Collections[0].Warnings, "\n"), "non-regular") {
		t.Errorf("warnings %q", result.Collections[0].Warnings)
	}
}

func TestReadBrunoFileLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.bru")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxBrunoFile+1); err != nil {
		t.Fatal(err)
	}
	if _, err := readBrunoFile(path); err == nil {
		t.Fatal("read a file over 16 MiB")
	}
}
