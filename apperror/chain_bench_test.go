package apperror_test

import (
	"errors"
	"testing"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Wrap-at-every-return is a common pattern, so the cost that matters is
// a chain of wraps, not a single one.

func wrapChain(depth int, base error) error {
	err := base
	for range depth {
		err = apperror.Wrap(err)
	}
	return err
}

func BenchmarkWrapChain8(b *testing.B) {
	base := errors.New("base")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		err := wrapChain(8, base)
		if err == nil {
			b.Fatal("wrapChain() = nil, want an error")
		}
	}
}

func BenchmarkNewErrorAddErrorWrap(b *testing.B) {
	base := errors.New("base")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		err := apperror.Wrap(apperror.NewError("insert failed").AddError(base))
		if err == nil {
			b.Fatal("Wrap() = nil, want an error")
		}
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
