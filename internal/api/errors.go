package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrNotFound reports that the requested resource does not exist. Match it with
// errors.Is; the concrete error is always *Error.
var ErrNotFound = errors.New("not found")

// ErrUnauthorized reports that the request needs credentials the client did not
// supply, or supplied credentials the server rejected.
var ErrUnauthorized = errors.New("unauthorized")

// Error is a non-2xx response from MarvelCDB.
type Error struct {
	Method     string
	URL        string
	StatusCode int
	// Message is the "message" from MarvelCDB's {"error":{...}} envelope, or the
	// OAuth2 error_description, when either is present.
	Message string
	// Body is the response body, truncated for display.
	Body string
	// UpstreamBug marks a status MarvelCDB uses to mean "no such record" even
	// though it is nominally a server fault. See classify.
	UpstreamBug bool
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s", e.Method, e.URL, http.StatusText(e.StatusCode))
	if e.StatusCode != 0 && http.StatusText(e.StatusCode) == "" {
		fmt.Fprintf(&b, "status %d", e.StatusCode)
	}
	if e.UpstreamBug {
		b.WriteString(" (MarvelCDB answers unknown ids with 500; treating as not found)")
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	} else if e.Body != "" && !strings.HasPrefix(e.Body, "<") {
		fmt.Fprintf(&b, ": %s", e.Body)
	}
	return b.String()
}

// Is lets callers test for ErrNotFound and ErrUnauthorized.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound || e.UpstreamBug
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
	default:
		return false
	}
}

// IsNotFound reports whether err means "no such resource".
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsUnauthorized reports whether err means "credentials missing or rejected".
func IsUnauthorized(err error) bool { return errors.Is(err, ErrUnauthorized) }

const maxErrorBody = 512

// newError builds an *Error from a response body. treatServerErrorAsMissing
// should be set for the single-record endpoints (card, faq, deck, decklist),
// where MarvelCDB returns a 500 error envelope instead of a 404 for ids it does
// not have. Collection endpoints keep 500 as a genuine failure.
func newError(method, url string, status int, body []byte, treatServerErrorAsMissing bool) *Error {
	e := &Error{
		Method:     method,
		URL:        url,
		StatusCode: status,
		Body:       truncate(strings.TrimSpace(string(body)), maxErrorBody),
	}

	var envelope struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		switch {
		case envelope.Error.Message != "":
			e.Message = envelope.Error.Message
		case envelope.ErrorDescription != "":
			e.Message = envelope.ErrorDescription
		}
		if treatServerErrorAsMissing && status == http.StatusInternalServerError && envelope.Error.Code == status {
			e.UpstreamBug = true
		}
	}

	// OAuth2 errors arrive as {"error":"invalid_grant","error_description":"..."}.
	var oauthErr struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &oauthErr); err == nil && oauthErr.Error != "" {
		e.Message = strings.TrimSpace(oauthErr.Error + ": " + oauthErr.ErrorDescription)
	}

	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
