package request

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/grpctest"
)

func TestGRPCExecuteRunsScriptsChecksAndReturnsTheMessage(t *testing.T) {
	endpoint, schema := grpctest.Echo(t)
	srv := testProxy(t)
	r := Request{
		Protocol: "gRPC", URL: endpoint, Body: `"hello"`,
		GRPC:   GRPCConfig{Schema: schema, Method: "/grpctest.Echo/Unary"},
		Pre:    `pm.request.headers.upsert("x-token", "{{token}}")`,
		Post:   `pm.test("message", function () { pm.expect(pm.response.json()).to.eql("reply:hello") })`,
		Checks: Checks{Status: "200", Headers: "Grpc-Status: 0", JSON: ` = "reply:hello"`},
	}
	got := r.Execute(t.Context(), srv, Context{}, map[string]string{"token": grpctest.Token}, nil)
	if got.Err != nil {
		t.Fatalf("execute: %v (%+v)", got.Err, got.Tests)
	}
	if got.Response == nil || got.Response.ResponseBody != `"reply:hello"` {
		t.Fatalf("response: %+v", got.Response)
	}
	if got.Response.ResponseHeader.Get("Grpc-Status") != "0" || got.Response.ResponseHeader.Get("X-Reply") != "header-1" {
		t.Fatalf("headers: %v", got.Response.ResponseHeader)
	}
	if got.Response.ResponseHeader.Get("Grpc-Trailer-X-Finish") != "trailer-1" {
		t.Fatalf("trailers not kept: %v", got.Response.ResponseHeader)
	}
	if len(got.Tests) != 4 {
		t.Fatalf("tests: %+v", got.Tests)
	}
	for _, test := range got.Tests {
		if !test.Pass {
			t.Fatalf("test %q failed: %s", test.Name, test.Error)
		}
	}
	if got.Request == nil || got.Request.Header.Get("x-token") != grpctest.Token {
		t.Fatalf("pre-request script did not reach the call: %+v", got.Request)
	}
}

func TestGRPCExecuteKeepsANonOKStatusAndRunsChecks(t *testing.T) {
	endpoint, schema := grpctest.Echo(t)
	srv := testProxy(t)
	r := Request{
		Protocol: "gRPC", URL: endpoint, Body: `"hello"`,
		GRPC:    GRPCConfig{Schema: schema, Method: "/grpctest.Echo/Fail"},
		Headers: "x-token: " + grpctest.Token,
		Checks:  Checks{Headers: "Grpc-Status: 7"},
	}
	got := r.Execute(t.Context(), srv, Context{}, nil, nil)
	if got.Err == nil || !strings.Contains(got.Err.Error(), "PermissionDenied") {
		t.Fatalf("err: %v", got.Err)
	}
	if got.Response == nil || got.Response.ResponseHeader.Get("Grpc-Status") != "7" {
		t.Fatalf("response: %+v", got.Response)
	}
	if got.Response.ResponseHeader.Get("Grpc-Message") != "not permitted" {
		t.Fatalf("message: %v", got.Response.ResponseHeader)
	}
	if len(got.Tests) != 1 || !got.Tests[0].Pass {
		t.Fatalf("a non-OK status should still run checks: %+v", got.Tests)
	}
}

func TestGRPCExecuteRejectsStreamingMethodsAndMissingSchema(t *testing.T) {
	endpoint, schema := grpctest.Echo(t)
	srv := testProxy(t)
	for _, tc := range []struct {
		name string
		cfg  GRPCConfig
		want string
	}{
		{"stream", GRPCConfig{Schema: schema, Method: "/grpctest.Echo/Stream"}, "streaming method"},
		{"no schema", GRPCConfig{Method: "/grpctest.Echo/Unary"}, "import a schema"},
		{"no method", GRPCConfig{Schema: schema}, "select a gRPC service/method"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Request{Protocol: "gRPC", URL: endpoint, Body: `"hello"`, GRPC: tc.cfg, Headers: "x-token: " + grpctest.Token}.Execute(t.Context(), srv, Context{}, nil, nil)
			if got.Err == nil || !strings.Contains(got.Err.Error(), tc.want) {
				t.Fatalf("err: %v", got.Err)
			}
		})
	}
}

func TestGRPCExecuteTimesOutWithTheRequestDeadline(t *testing.T) {
	endpoint, schema := grpctest.Echo(t)
	srv := testProxy(t)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	got := Request{Protocol: "gRPC", URL: endpoint, Body: `"hello"`, GRPC: GRPCConfig{Schema: schema, Method: "/grpctest.Echo/Wait"}, Headers: "x-token: " + grpctest.Token}.Execute(ctx, srv, Context{}, nil, nil)
	if got.Err == nil || !strings.Contains(got.Err.Error(), "DeadlineExceeded") {
		t.Fatalf("err: %v", got.Err)
	}
}
