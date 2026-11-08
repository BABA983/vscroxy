package app

import (
	"errors"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/i18n"
)

func TestNTLMTextUsesTheCatalog(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	for _, test := range []struct {
		kind string
		want string
	}{
		{request.NTLMClosedConnection, "NTLM 认证失败：服务器在 NTLM 质询后关闭了连接。"},
		{request.NTLMNoChallenge, "NTLM 认证失败：服务器没有发送 NTLM 质询。"},
		{request.NTLMUsernameRequired, "NTLM 认证失败：请输入用户名。"},
		{request.NTLMProxyRequired, "NTLM 认证失败：请先启动本地代理。"},
		{request.NTLMUnsupported, "NTLM 认证失败：the server asked for NTLMv1"},
	} {
		err := &request.NTLMError{Kind: test.kind, Detail: "the server asked for NTLMv1"}
		if got := runSummary(request.Execution{Err: err}); got != test.want {
			t.Errorf("%s: %q, want %q", test.kind, got, test.want)
		}
	}
	failure := &request.NTLMError{Kind: request.NTLMClosedConnection, Detail: "detail"}
	if got := executionOutcome(request.Execution{Err: failure, Stage: "NTLM authentication failed"}); got != "NTLM 认证失败" {
		t.Errorf("outcome = %q", got)
	}
	if got := executionOutcome(request.Execution{Err: errors.New("network error"), Stage: "Network error"}); got != "Network error" {
		t.Errorf("other outcome = %q", got)
	}
}
