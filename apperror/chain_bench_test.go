package apperror_test

import (
	"errors"
	"testing"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Wrap-at-every-return is the pattern across bleep, so the cost that matters is
// a chain of wraps, not a single one.

func wrapChain(depth int, base error) error {
	err := base
	for i := 0; i < depth; i++ {
		err = apperror.Wrap(err)
	}
	return err
}

func BenchmarkWrapChain8(b *testing.B) {
	base := errors.New("base")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = wrapChain(8, base)
	}
}

func BenchmarkNewErrorAddErrorWrap(b *testing.B) {
	base := errors.New("base")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = apperror.Wrap(apperror.NewError("insert failed").AddError(base))
	}
}

func BenchmarkNewErrorParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = apperror.NewError("benchmark error")
		}
	})
}

func BenchmarkChainErrorString8(b *testing.B) {
	err := wrapChain(8, errors.New("base"))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = err.Error()
	}
}
