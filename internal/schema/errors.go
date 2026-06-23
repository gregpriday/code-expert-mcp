package schema

import "fmt"

// Typed error codes. These are stable identifiers returned to MCP hosts and
// mapped to CLI exit codes. They never carry secrets or raw source content.
const (
	CodeInvalidArgument          = "CE_INVALID_ARGUMENT"
	CodeConfigInvalid            = "CE_CONFIG_INVALID"
	CodeConfigUnsupportedVersion = "CE_CONFIG_UNSUPPORTED_VERSION"
	CodeRootNotFound             = "CE_ROOT_NOT_FOUND"
	CodeRootOutsideAllowed       = "CE_ROOT_OUTSIDE_ALLOWED"
	CodeRootSymlinkEscape        = "CE_ROOT_SYMLINK_ESCAPE"
	CodeNotGitRepository         = "CE_NOT_GIT_REPOSITORY"
	CodeGitNotFound              = "CE_GIT_NOT_FOUND"
	CodeGitRefInvalid            = "CE_GIT_REF_INVALID"
	CodeSnapshotChanged          = "CE_SNAPSHOT_CHANGED"
	CodeSnapshotTooLarge         = "CE_SNAPSHOT_TOO_LARGE"
	CodeProviderAuth             = "CE_PROVIDER_AUTH"
	CodeProviderRateLimit        = "CE_PROVIDER_RATE_LIMIT"
	CodeProviderTimeout          = "CE_PROVIDER_TIMEOUT"
	CodeProviderUnsupported      = "CE_PROVIDER_UNSUPPORTED"
	CodeProviderSchema           = "CE_PROVIDER_SCHEMA"
	CodeBudgetExhausted          = "CE_BUDGET_EXHAUSTED"
	CodeCheckNotAllowed          = "CE_CHECK_NOT_ALLOWED"
	CodeCheckFailed              = "CE_CHECK_FAILED"
	CodeOutputInvalid            = "CE_OUTPUT_INVALID"
	CodeCancelled                = "CE_CANCELLED"
	CodeInternal                 = "CE_INTERNAL"
)

// ToolError is the structured error payload returned to MCP hosts and rendered
// by the CLI. It implements error so it can be returned from any stage.
type ToolError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Retryable bool              `json:"retryable"`
	Stage     string            `json:"stage,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
	RunID     string            `json:"run_id,omitempty"`
}

func (e *ToolError) Error() string {
	if e == nil {
		return "<nil tool error>"
	}
	if e.Stage != "" {
		return fmt.Sprintf("%s [%s]: %s", e.Code, e.Stage, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewError builds a ToolError with a code and message.
func NewError(code, format string, args ...any) *ToolError {
	return &ToolError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithStage returns a copy of the error annotated with the failing stage.
func (e *ToolError) WithStage(stage string) *ToolError {
	if e == nil {
		return nil
	}
	cp := *e
	cp.Stage = stage
	return &cp
}

// WithDetail attaches a single key/value detail. Values must not contain
// secrets or raw source content.
func (e *ToolError) WithDetail(key, value string) *ToolError {
	if e == nil {
		return nil
	}
	cp := *e
	if cp.Details == nil {
		cp.Details = map[string]string{}
	} else {
		nd := make(map[string]string, len(cp.Details)+1)
		for k, v := range cp.Details {
			nd[k] = v
		}
		cp.Details = nd
	}
	cp.Details[key] = value
	return &cp
}

// AsToolError unwraps any error to a *ToolError, wrapping unknown errors as
// CE_INTERNAL so callers always have a stable code.
func AsToolError(err error) *ToolError {
	if err == nil {
		return nil
	}
	if te, ok := err.(*ToolError); ok {
		return te
	}
	return &ToolError{Code: CodeInternal, Message: err.Error()}
}
