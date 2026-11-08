package app

import (
	"errors"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
)

// ntlmErrorText is an NTLM exchange failure in the app's language. Other
// errors keep their own text.
func ntlmErrorText(err error) string {
	var failure *request.NTLMError
	if !errors.As(err, &failure) {
		return err.Error()
	}
	switch failure.Kind {
	case request.NTLMClosedConnection:
		return locales.NtlmClosedConnection()
	case request.NTLMNoChallenge:
		return locales.NtlmNoChallenge()
	case request.NTLMUsernameRequired:
		return locales.NtlmEnterUsername()
	case request.NTLMProxyRequired:
		return locales.NtlmStartProxy()
	default:
		return locales.NtlmFailureDetail(failure.Detail)
	}
}

// executionOutcome is r.Outcome() in the app's language. Only NTLM's stage has
// a message; the other outcomes keep their identifiers.
func executionOutcome(r request.Execution) string {
	var failure *request.NTLMError
	if errors.As(r.Err, &failure) {
		return locales.NtlmAuthenticationFailed()
	}
	return r.Outcome()
}
