package provider

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// RetryPolicy bounds transient-failure retries across a run. It uses exponential
// backoff with jitter and honors Retry-After.
type RetryPolicy struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// DefaultRetryPolicy returns a policy with sane bounds.
func DefaultRetryPolicy(maxRetries int) RetryPolicy {
	if maxRetries < 0 {
		maxRetries = 0
	}
	return RetryPolicy{MaxRetries: maxRetries, BaseDelay: 500 * time.Millisecond, MaxDelay: 20 * time.Second}
}

// RetryableError marks a provider error as retryable and carries an optional
// Retry-After hint.
type RetryableError struct {
	Err        error
	RetryAfter time.Duration
	Code       string
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// ClassifyHTTPStatus returns a typed, possibly-retryable error for an HTTP
// status, or nil if the status is success.
func ClassifyHTTPStatus(status int, body string, header http.Header) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return schema.NewError(schema.CodeProviderAuth, "provider authentication failed (HTTP %d)", status)
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return schema.NewError(schema.CodeProviderSchema, "provider rejected request (HTTP %d): %s", status, Truncate(body, 300))
	case status == http.StatusRequestTimeout:
		return &RetryableError{Err: schema.NewError(schema.CodeProviderTimeout, "provider timeout (HTTP 408)"), Code: schema.CodeProviderTimeout}
	case status == http.StatusTooManyRequests:
		return &RetryableError{
			Err:        schema.NewError(schema.CodeProviderRateLimit, "provider rate limited (HTTP 429)"),
			RetryAfter: parseRetryAfter(header),
			Code:       schema.CodeProviderRateLimit,
		}
	case status == http.StatusInternalServerError, status == http.StatusBadGateway,
		status == http.StatusServiceUnavailable, status == http.StatusGatewayTimeout:
		return &RetryableError{
			Err:        schema.NewError(schema.CodeProviderTimeout, "provider server error (HTTP %d)", status),
			RetryAfter: parseRetryAfter(header),
			Code:       schema.CodeProviderTimeout,
		}
	default:
		return schema.NewError(schema.CodeProviderUnsupported, "unexpected provider status HTTP %d: %s", status, Truncate(body, 300))
	}
}

// Do runs fn with retries according to the policy. fn should return a
// *RetryableError (or wrap one) for transient failures. Do has a value receiver
// so it is safe to share a RetryPolicy across goroutines.
func (p RetryPolicy) Do(ctx context.Context, fn func() error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return schema.NewError(schema.CodeCancelled, "cancelled: %v", err)
		}
		err := fn()
		if err == nil {
			return nil
		}
		var re *RetryableError
		if !errors.As(err, &re) || attempt >= p.MaxRetries {
			return err
		}
		delay := p.backoff(attempt, re.RetryAfter)
		select {
		case <-ctx.Done():
			return schema.NewError(schema.CodeCancelled, "cancelled during backoff: %v", ctx.Err())
		case <-time.After(delay):
		}
	}
}

func (p RetryPolicy) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > p.MaxDelay {
			return p.MaxDelay
		}
		return retryAfter
	}
	base := p.BaseDelay
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	d := time.Duration(float64(base) * math.Pow(2, float64(attempt)))
	// Full jitter avoids thundering-herd retries.
	if d > 0 {
		d = time.Duration(rand.Int63n(int64(d)))/2 + d/2
	}
	if d > p.MaxDelay {
		d = p.MaxDelay
	}
	return d
}

func parseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// Truncate shortens a string to n runes with an ellipsis, for safe error
// messages that must not echo unbounded provider bodies.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
