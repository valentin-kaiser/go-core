package apperror_test

import (
	"errors"
	"testing"

	"github.com/valentin-kaiser/go-core/apperror"
)

func BenchmarkWhere(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = apperror.Where(2)
	}
}

func BenchmarkErrorsIsChain8(b *testing.B) {
	base := errors.New("base")
	var err error = base
	for i := 0; i < 8; i++ {
		err = apperror.Wrap(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = errors.Is(err, base)
	}
}

// Error() on a fresh chain each time, to rule out caching in the earlier benchmark.
func BenchmarkChainErrorStringFresh8(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var err error = errors.New("base")
		for j := 0; j < 8; j++ {
			err = apperror.Wrap(err)
		}
		_ = err.Error()
	}
}

func BenchmarkTraceError(b *testing.B) {
	e := apperror.NewError("x")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = apperror.TraceError(e)
	}
}
