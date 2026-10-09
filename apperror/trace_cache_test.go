package apperror_test

import (
	"strings"
	"testing"

	"github.com/valentin-kaiser/go-core/apperror"
)

func traceHere() string { return apperror.Trace(2) }

// The trace points at the caller, and a repeated call from the same place gives the same text
func TestTraceNamesTheCaller(t *testing.T) {
	var traces []string
	for i := 0; i < 2; i++ {
		traces = append(traces, traceHere()) // the same call site twice
	}
	if !strings.Contains(traces[0], "trace_cache_test.go:") {
		t.Fatalf("trace %q does not name this file", traces[0])
	}
	if traces[0] != traces[1] {
		t.Fatalf("the same call site gave %q and %q", traces[0], traces[1])
	}

	if other := traceHere(); other == traces[0] {
		t.Fatalf("two call sites share the trace %q", other)
	}
}

func TestTraceAnonymous(t *testing.T) {
	apperror.Anonymous(true)
	t.Cleanup(func() { apperror.Anonymous(false) })

	got := traceHere()
	if !strings.Contains(got, "apperror_test.TestTraceAnonymous:") || strings.Contains(got, ".go:") {
		t.Fatalf("anonymous trace %q should hold the function name and no file", got)
	}

	apperror.Anonymous(false)
	if got := traceHere(); !strings.Contains(got, "trace_cache_test.go:") {
		t.Fatalf("after switching anonymous off: %q", got)
	}
}

func TestNewErrorTraceHoldsCreationSite(t *testing.T) {
	err := apperror.NewError("x")
	if len(err.Trace) != 1 || !strings.Contains(err.Trace[0], "trace_cache_test.go:") {
		t.Fatalf("trace %v", err.Trace)
	}
	wrapped, _ := apperror.Wrap(err).(apperror.Error)
	if len(wrapped.Trace) != 2 {
		t.Fatalf("wrapped trace %v", wrapped.Trace)
	}
}
