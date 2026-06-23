// Package budget tracks and enforces the per-run limits that bound autonomy:
// wall time, model calls, internal tool calls, files and bytes read, and output
// size. Exhaustion produces a typed error so callers return a partial result
// with explicit limitations rather than looping unbounded.
package budget

import (
	"context"
	"sync"
	"time"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// Limits are the ceilings for a run. Zero means "no explicit limit" for that
// dimension (subject to global safety caps applied by the caller).
type Limits struct {
	Timeout            time.Duration
	MaxModelCalls      int
	MaxModelToolRounds int
	MaxInternalTools   int
	MaxFilesRead       int
	MaxBytesRead       int64
	MaxContextTokens   int
	MaxOutputTokens    int
}

// Tracker is a thread-safe accountant for a single run.
type Tracker struct {
	limits        Limits
	start         time.Time
	mu            sync.Mutex
	modelCalls    int
	internalTools int
	filesRead     int
	bytesRead     int64
}

// New starts a tracker.
func New(l Limits) *Tracker {
	return &Tracker{limits: l, start: time.Now()}
}

// Elapsed returns wall time since the run started.
func (t *Tracker) Elapsed() time.Duration { return time.Since(t.start) }

// Deadline returns a context that cancels at the timeout, plus a cancel func.
func (t *Tracker) Deadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if t.limits.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, t.limits.Timeout)
}

// TimedOut reports whether the wall-time budget is exhausted.
func (t *Tracker) TimedOut() bool {
	return t.limits.Timeout > 0 && t.Elapsed() >= t.limits.Timeout
}

// ChargeModelCall reserves one model call, erroring if the budget is exhausted.
func (t *Tracker) ChargeModelCall() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.limits.MaxModelCalls > 0 && t.modelCalls >= t.limits.MaxModelCalls {
		return schema.NewError(schema.CodeBudgetExhausted, "model call budget (%d) exhausted", t.limits.MaxModelCalls)
	}
	t.modelCalls++
	return nil
}

// ChargeInternalTool reserves one internal tool call.
func (t *Tracker) ChargeInternalTool() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.limits.MaxInternalTools > 0 && t.internalTools >= t.limits.MaxInternalTools {
		return schema.NewError(schema.CodeBudgetExhausted, "internal tool budget (%d) exhausted", t.limits.MaxInternalTools)
	}
	t.internalTools++
	return nil
}

// ChargeFileRead reserves one file read of n bytes.
func (t *Tracker) ChargeFileRead(n int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.limits.MaxFilesRead > 0 && t.filesRead >= t.limits.MaxFilesRead {
		return schema.NewError(schema.CodeBudgetExhausted, "file read budget (%d) exhausted", t.limits.MaxFilesRead)
	}
	if t.limits.MaxBytesRead > 0 && t.bytesRead+n > t.limits.MaxBytesRead {
		return schema.NewError(schema.CodeBudgetExhausted, "byte read budget (%d) exhausted", t.limits.MaxBytesRead)
	}
	t.filesRead++
	t.bytesRead += n
	return nil
}

// Snapshot returns the current usage counters.
func (t *Tracker) Snapshot() Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Usage{
		ModelCalls:    t.modelCalls,
		InternalTools: t.internalTools,
		FilesRead:     t.filesRead,
		BytesRead:     t.bytesRead,
		Wall:          t.Elapsed(),
	}
}

// Usage is a point-in-time view of consumption.
type Usage struct {
	ModelCalls    int
	InternalTools int
	FilesRead     int
	BytesRead     int64
	Wall          time.Duration
}

// MaxOutputTokens exposes the configured output ceiling.
func (t *Tracker) MaxOutputTokens() int { return t.limits.MaxOutputTokens }

// MaxModelToolRounds exposes the configured exploration round ceiling.
func (t *Tracker) MaxModelToolRounds() int { return t.limits.MaxModelToolRounds }
