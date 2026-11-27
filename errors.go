package noderings

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Code is a gRPC status code, as the API puts it in error bodies.
type Code int

// Status codes the API uses.
const (
	CodeOK                 Code = 0
	CodeCanceled           Code = 1
	CodeUnknown            Code = 2
	CodeInvalidArgument    Code = 3
	CodeDeadlineExceeded   Code = 4
	CodeNotFound           Code = 5
	CodeAlreadyExists      Code = 6
	CodePermissionDenied   Code = 7
	CodeResourceExhausted  Code = 8
	CodeFailedPrecondition Code = 9
	CodeAborted            Code = 10
	CodeOutOfRange         Code = 11
	CodeUnimplemented      Code = 12
	CodeInternal           Code = 13
	CodeUnavailable        Code = 14
	CodeDataLoss           Code = 15
	CodeUnauthenticated    Code = 16
)

// Error is an error returned by the NodeRings API.
type Error struct {
	// StatusCode is the HTTP status code.
	StatusCode int
	// Code is the API's gRPC status code.
	Code Code
	// Message is the API's message, meant for people.
	Message string
	// Details are the structured details of the error, for example
	// google.rpc.ErrorInfo with a reason such as VM_LIMIT_REACHED.
	Details []ErrorDetail
	// Method and Path identify the request.
	Method, Path string
}

// ErrorDetail is one entry of an error's details.
type ErrorDetail struct {
	Type     string            `json:"@type"`
	Reason   string            `json:"reason,omitempty"`
	Domain   string            `json:"domain,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	// Raw is the whole detail as sent by the API.
	Raw json.RawMessage `json:"-"`
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("noderings: %s %s: %s (HTTP %d)", e.Method, e.Path, msg, e.StatusCode)
}

// Reason returns the reason of the first google.rpc.ErrorInfo detail, or "".
func (e *Error) Reason() string {
	for _, d := range e.Details {
		if strings.HasSuffix(d.Type, "google.rpc.ErrorInfo") && d.Reason != "" {
			return d.Reason
		}
	}
	return ""
}

// IsQuotaExceeded reports whether a plan quota is used up (a reason ending in
// LIMIT_REACHED), as opposed to a short-term rate limit.
func (e *Error) IsQuotaExceeded() bool {
	return strings.HasSuffix(e.Reason(), "LIMIT_REACHED")
}

func decodeError(resp *http.Response, body []byte, r *request) *Error {
	e := &Error{StatusCode: resp.StatusCode, Method: r.method, Path: r.path}
	var raw struct {
		Code    Code              `json:"code"`
		Message string            `json:"message"`
		Details []json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal(body, &raw); err == nil {
		e.Code, e.Message = raw.Code, raw.Message
		for _, d := range raw.Details {
			var detail ErrorDetail
			if json.Unmarshal(d, &detail) == nil {
				detail.Raw = d
				e.Details = append(e.Details, detail)
			}
		}
	} else {
		e.Message = strings.TrimSpace(string(body))
		if len(e.Message) > 300 {
			e.Message = e.Message[:300] + "..."
		}
	}
	if e.Code == 0 {
		e.Code = codeFromHTTP(resp.StatusCode)
	}
	return e
}

func codeFromHTTP(status int) Code {
	switch status {
	case http.StatusBadRequest:
		return CodeInvalidArgument
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusForbidden:
		return CodePermissionDenied
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeAlreadyExists
	case http.StatusTooManyRequests:
		return CodeResourceExhausted
	case http.StatusNotImplemented:
		return CodeUnimplemented
	case http.StatusServiceUnavailable:
		return CodeUnavailable
	case http.StatusGatewayTimeout:
		return CodeDeadlineExceeded
	}
	return CodeUnknown
}

// ErrorCode returns the API status code of err, or CodeUnknown when err is
// not an *Error (or CodeOK when err is nil).
func ErrorCode(err error) Code {
	if err == nil {
		return CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeUnknown
}

// IsNotFound reports whether err means the resource does not exist.
func IsNotFound(err error) bool { return ErrorCode(err) == CodeNotFound }

// IsAlreadyExists reports whether err means the resource already exists.
func IsAlreadyExists(err error) bool { return ErrorCode(err) == CodeAlreadyExists }

// IsUnauthenticated reports whether the token was missing or rejected.
func IsUnauthenticated(err error) bool { return ErrorCode(err) == CodeUnauthenticated }

// IsPermissionDenied reports whether the token lacks a permission.
func IsPermissionDenied(err error) bool { return ErrorCode(err) == CodePermissionDenied }

// IsInvalidArgument reports whether the request was rejected as invalid.
func IsInvalidArgument(err error) bool { return ErrorCode(err) == CodeInvalidArgument }

// IsRateLimited reports whether the API asked to slow down (HTTP 429) and the
// cause is not an exhausted plan quota.
func IsRateLimited(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.StatusCode == http.StatusTooManyRequests && !e.IsQuotaExceeded()
}

// IsQuotaExceeded reports whether a plan quota, such as the number of virtual
// machines, is used up.
func IsQuotaExceeded(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.IsQuotaExceeded()
}

// IsBusy reports whether the API refused an action because another operation
// on the resource is still running ("operation in progress"). Such calls
// succeed when retried a little later.
func IsBusy(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == CodeFailedPrecondition &&
		strings.Contains(strings.ToLower(e.Message), "in progress")
}
