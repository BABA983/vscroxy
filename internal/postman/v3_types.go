package postman

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
)

// The types below are the subset of the Postman Collection v3 schema this
// reader understands. Unknown fields are ignored, as the schema says an
// unrecognized field is ignored rather than a parse failure.

type v3Definition struct {
	Kind        string         `json:"$kind"`
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description jsontext.Value `json:"description"`
	Variables   []v3Variable   `json:"variables"`
	Scripts     []v3Script     `json:"scripts"`
	Auth        jsontext.Value `json:"auth"`
	Uses        jsontext.Value `json:"uses"`
	Order       *float64       `json:"order"`
}

type v3Variable struct {
	Key         string         `json:"key"`
	Value       jsontext.Value `json:"value"`
	Description jsontext.Value `json:"description"`
	Disabled    bool           `json:"disabled"`
	Secret      bool           `json:"secret"`
	Source      jsontext.Value `json:"source"`
}

type v3Script struct {
	Type     string         `json:"type"`
	Code     string         `json:"code"`
	Language string         `json:"language"`
	Packages jsontext.Value `json:"packages"`
	Requests jsontext.Value `json:"requests"`
}

type v3Auth struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description jsontext.Value `json:"description"`
	Type        string         `json:"type"`
	Credentials jsontext.Value `json:"credentials"`
}

// v3KV is a header, query parameter, path variable, form part or credential.
// Description, SchemaType and ContentType are metadata the app's field model
// cannot keep; the reader counts them.
type v3KV struct {
	Key         string         `json:"key"`
	Value       jsontext.Value `json:"value"`
	Description jsontext.Value `json:"description"`
	Disabled    bool           `json:"disabled"`
	Type        string         `json:"type"`
	Src         jsontext.Value `json:"src"`
	ContentType string         `json:"contentType"`
	SchemaType  string         `json:"$type"`
}

// v3KVs accepts the array the schema documents and the object shape some
// hand-written collections use: {Content-Type: application/json}.
type v3KVs []v3KV

func (k *v3KVs) UnmarshalJSON(data []byte) error {
	value := jsontext.Value(data)
	switch value.Kind() {
	case 0, 'n':
		*k = nil
	case '[':
		var list []v3KV
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		*k = list
	case '{':
		var values map[string]jsontext.Value
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		list := make([]v3KV, 0, len(values))
		for _, key := range slices.Sorted(maps.Keys(values)) {
			list = append(list, v3KV{Key: key, Value: values[key]})
		}
		*k = list
	default:
		return fmt.Errorf("want an array or object of key-value entries")
	}
	return nil
}

type v3Body struct {
	Type       string         `json:"type"`
	Content    jsontext.Value `json:"content"`
	SchemaType string         `json:"$type"`
}

type v3Request struct {
	Kind          string         `json:"$kind"`
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Description   jsontext.Value `json:"description"`
	URL           string         `json:"url"`
	Method        string         `json:"method"`
	Headers       v3KVs          `json:"headers"`
	QueryParams   v3KVs          `json:"queryParams"`
	PathVariables v3KVs          `json:"pathVariables"`
	Body          *v3Body        `json:"body"`
	Auth          jsontext.Value `json:"auth"`
	Settings      jsontext.Value `json:"settings"`
	Scripts       []v3Script     `json:"scripts"`
	Examples      jsontext.Value `json:"examples"`
	Order         *float64       `json:"order"`

	// graphql-request
	Query         string         `json:"query"`
	Variables     jsontext.Value `json:"variables"`
	OperationName string         `json:"operationName"`
	Schema        jsontext.Value `json:"schema"`

	// grpc-request
	MethodPath       string `json:"methodPath"`
	MethodDescriptor string `json:"methodDescriptor"`
	Message          struct {
		Content jsontext.Value `json:"content"`
	} `json:"message"`
	Metadata v3KVs `json:"metadata"`

	// websocket-request and socket.io-request
	Messages jsontext.Value `json:"messages"`
	Events   []v3Event      `json:"events"`

	// mcp-request selects its shape with transport; the app skips the kind.
	Transport string `json:"transport"`
}

type v3Event struct {
	Name               string `json:"name"`
	SubscribeOnConnect bool   `json:"subscribeOnConnect"`
	Description        string `json:"description"`
}

type v3Schema struct {
	Source          string `json:"source"`
	Location        string `json:"location"`
	URL             string `json:"url"`
	APIID           string `json:"apiId"`
	VersionID       string `json:"versionId"`
	SpecificationID string `json:"specificationId"`
}

type v3Example struct {
	Kind        string             `json:"$kind"`
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description jsontext.Value     `json:"description"`
	Auth        jsontext.Value     `json:"auth"`
	Settings    jsontext.Value     `json:"settings"`
	Schema      jsontext.Value     `json:"schema"`
	Request     *v3ExampleRequest  `json:"request"`
	Response    *v3ExampleResponse `json:"response"`
	Order       *float64           `json:"order"`
}

type v3ExampleRequest struct {
	URL           string         `json:"url"`
	Method        string         `json:"method"`
	Headers       jsontext.Value `json:"headers"`
	QueryParams   v3KVs          `json:"queryParams"`
	PathVariables v3KVs          `json:"pathVariables"`
	Body          *v3Body        `json:"body"`
	Query         string         `json:"query"`
	Variables     jsontext.Value `json:"variables"`
	OperationName string         `json:"operationName"`
	OperationType string         `json:"operationType"`
}

type v3ExampleResponse struct {
	StatusCode int            `json:"statusCode"`
	StatusText string         `json:"statusText"`
	Headers    jsontext.Value `json:"headers"`
	Body       *v3Body        `json:"body"`
	Events     []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Content string `json:"content"`
	} `json:"events"`
	HTTP struct {
		Status  string         `json:"status"`
		Headers jsontext.Value `json:"headers"`
		Body    string         `json:"body"`
	} `json:"http"`
}

type v3Message struct {
	Kind            string         `json:"$kind"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Content         string         `json:"content"`
	ContentType     string         `json:"contentType"`
	EventName       string         `json:"eventName"`
	Acknowledgement bool           `json:"acknowledgement"`
	Args            []v3MessageArg `json:"args"`
}

type v3MessageArg struct {
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
}

// pmGRPC and pmSocketIO are set by the v3 reader only. They let the shared
// v2.1 request conversion produce the app's protocol documents without a
// second auth/body/header conversion.
type pmGRPC struct {
	Method string
	Body   string
}

type pmSocketIO struct {
	Event   string
	Body    string
	WantACK bool
}
