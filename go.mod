module github.com/baba983/vscroxy

go 1.27.3

// Preserve WebSocket metadata and native gRPC streaming/trailers (see third_party/go-mitmproxy/VSCROXY.md).
replace github.com/lqqyt2423/go-mitmproxy => ./third_party/go-mitmproxy

require (
	github.com/Azure/go-ntlmssp v0.1.1
	github.com/HdrHistogram/hdrhistogram-go v1.4.0
	github.com/andybalholm/brotli v1.2.1
	github.com/bufbuild/protocompile v0.14.1
	github.com/dop251/goja v0.0.0-20261019185110-cf7b1419c9c0
	github.com/ebitengine/purego v0.11.2
	github.com/eclipse/paho.golang v0.23.0
	github.com/eclipse/paho.mqtt.golang v1.5.1
	github.com/egoist/mygo v0.9.0
	github.com/evanw/esbuild v0.28.2
	github.com/getkin/kin-openapi v0.151.0
	github.com/google/jsonschema-go v0.4.3
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/itchyny/gojq v0.12.20
	github.com/klauspost/compress v1.18.6
	github.com/lqqyt2423/go-mitmproxy v1.9.4
	github.com/mochi-mqtt/server/v2 v2.7.9
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/oasdiff/yaml v0.1.1
	github.com/oasdiff/yaml3 v0.0.14
	github.com/ohler55/ojg v1.28.9
	github.com/oneo-me/onevra/source v0.2026.16
	github.com/sirupsen/logrus v1.9.4
	github.com/vektah/gqlparser/v2 v2.5.64
	github.com/vmihailenco/msgpack/v5 v5.4.1
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78
	golang.org/x/crypto v0.58.0
	golang.org/x/net v0.61.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
	mvdan.cc/sh/v3 v3.14.1
	software.sslmate.com/src/go-pkcs12 v0.7.4
)

require (
	github.com/ProtonMail/go-crypto v1.4.1 // indirect
	github.com/agnivade/levenshtein v1.2.2 // indirect
	github.com/cloudflare/circl v1.6.2 // indirect
	github.com/dlclark/regexp2/v2 v2.8.1 // indirect
	github.com/go-openapi/jsonpointer v0.22.5 // indirect
	github.com/go-openapi/swag/jsonname v0.25.5 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	github.com/itchyny/timefmt-go v0.1.9 // indirect
	github.com/rs/xid v1.4.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/ulikunitz/xz v0.5.15 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.digitalxero.dev/rpm v0.3.4 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

tool github.com/egoist/mygo/cmd/mygo
