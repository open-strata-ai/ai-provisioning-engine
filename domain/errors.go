package domain

// ErrorCode is a stable machine-readable provisioning error code.
type ErrorCode string

const (
	ErrInvalidRequest   ErrorCode = "invalid_request"
	ErrPreflight        ErrorCode = "preflight_failed"
	ErrConflict         ErrorCode = "conflict"
	ErrNotFound         ErrorCode = "not_found"
	ErrRevisionNotFound ErrorCode = "revision_not_found"
	ErrUnreachable      ErrorCode = "target_unreachable"
	ErrUpstream         ErrorCode = "upstream_error"
)

// ProvisionError is a domain error carrying a code and human-readable message.
type ProvisionError struct {
	Code    ErrorCode
	Message string
}

func (e *ProvisionError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// NewError builds a ProvisionError.
func NewError(code ErrorCode, msg string) *ProvisionError {
	return &ProvisionError{Code: code, Message: msg}
}

// HTTPStatus maps an error code to an HTTP status (interfaces/http, SPECS §7).
func (e *ProvisionError) HTTPStatus() int {
	switch e.Code {
	case ErrInvalidRequest, ErrPreflight, ErrConflict:
		return 400
	case ErrNotFound, ErrRevisionNotFound:
		return 404
	case ErrUnreachable:
		return 503
	default:
		return 500
	}
}

var _ error = (*ProvisionError)(nil)
