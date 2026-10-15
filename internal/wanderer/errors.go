package wanderer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Error codes Wanderer returns for a stream it refuses to open. They come from
// its events controller and its check_sse_disabled plug.
const (
	codeSSEDisabledForMap    = "SSE_DISABLED_FOR_MAP"
	codeSSEGloballyDisabled  = "SSE_GLOBALLY_DISABLED"
	codeSubscriptionRequired = "SUBSCRIPTION_REQUIRED"
)

// sseDisabledBody is the plain-text body a server with SSE switched off
// answers with, which it sends without an error code.
const sseDisabledBody = "Server-Sent Events are disabled on this server"

// maxErrorBody bounds how much of an error response is read for the log.
const maxErrorBody = 8 << 10

// messageLimit bounds the server message kept for the log line.
const messageLimit = 300

// FatalError reports a response that no retry can fix: the token, the map ID,
// or the SSE setting of the map or the server has to be changed. Hint says
// which.
type FatalError struct {
	// StatusCode is the HTTP status the stream endpoint answered with.
	StatusCode int
	// Code is the error code from the response body, when it carried one.
	Code string
	// Message is the server's error message, when it carried one.
	Message string
	// Hint is what to change to make the subscription work.
	Hint string
}

func (e *FatalError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "wanderer: the map stream was refused with %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	if e.Code != "" {
		fmt.Fprintf(&b, " (%s)", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.Hint != "" {
		fmt.Fprintf(&b, "; %s", e.Hint)
	}
	return b.String()
}

// IsFatal reports whether err is, or wraps, a *FatalError. Run only returns
// such an error for a misconfiguration, so main can turn it into a non-zero
// exit instead of a retry.
func IsFatal(err error) bool {
	var fatal *FatalError
	return errors.As(err, &fatal)
}

// statusError classifies a non-200 response from the stream endpoint. It
// returns a *FatalError for a misconfiguration and a plain error, which Run
// retries, for everything else.
func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	code, message := parseServerError(body)

	fatal := false
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusNotFound:
		fatal = true
	case http.StatusServiceUnavailable:
		// Only a 503 that says SSE is off is fatal. Wanderer returns the same
		// status for ordinary overload, which is worth retrying.
		fatal = code == codeSSEGloballyDisabled || strings.Contains(string(body), sseDisabledBody)
	}
	if fatal {
		return &FatalError{
			StatusCode: resp.StatusCode,
			Code:       code,
			Message:    message,
			Hint:       fatalHint(resp.StatusCode, code),
		}
	}

	err := fmt.Errorf("wanderer: the map stream answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	if code != "" {
		err = fmt.Errorf("%w (%s)", err, code)
	}
	if message != "" {
		err = fmt.Errorf("%w: %s", err, message)
	}
	return err
}

// fatalHint is the actionable part of a fatal error: what to change.
func fatalHint(status int, code string) string {
	switch {
	case code == codeSSEDisabledForMap:
		return "enable Server-Sent Events for this map in its Wanderer map settings"
	case code == codeSSEGloballyDisabled, status == http.StatusServiceUnavailable:
		return "this Wanderer instance has Server-Sent Events disabled; point the relay at an instance that offers them"
	case code == codeSubscriptionRequired, status == http.StatusPaymentRequired:
		return "this Wanderer instance requires an active subscription for the events API"
	case status == http.StatusUnauthorized:
		return "check the configured token: it has to be the map's public API key from the map's settings"
	case status == http.StatusForbidden:
		return "the token is not allowed to stream this map; check that it belongs to the map and that SSE is enabled for it"
	case status == http.StatusNotFound:
		return "check the configured map ID or slug"
	}
	return ""
}

// parseServerError pulls an error code and a message out of an error response.
// Wanderer answers with plain text in one case and JSON in the others, and the
// JSON has carried the code at the top level and nested under "error", so both
// are accepted and anything unrecognised falls back to the body itself.
func parseServerError(body []byte) (code, message string) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "", ""
	}
	if trimmed[0] != '{' {
		return "", oneLine(trimmed)
	}

	var wire struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &wire); err != nil {
		return "", oneLine(trimmed)
	}
	code, message = wire.Code, firstNonEmpty(wire.Message, wire.Detail)

	if len(wire.Error) > 0 {
		var text string
		if err := json.Unmarshal(wire.Error, &text); err == nil {
			message = firstNonEmpty(message, text)
		} else {
			var nested struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Detail  string `json:"detail"`
			}
			if err := json.Unmarshal(wire.Error, &nested); err == nil {
				code = firstNonEmpty(code, nested.Code)
				message = firstNonEmpty(message, nested.Message, nested.Detail)
			}
		}
	}
	return code, firstNonEmpty(message, oneLine(trimmed))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// oneLine makes a response body fit on a log line.
func oneLine(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > messageLimit {
		text = text[:messageLimit] + "…"
	}
	return text
}
