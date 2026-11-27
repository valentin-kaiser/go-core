package ptr_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/ptr"
)

var sinkInt *int

func BenchmarkPoint(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = new(i)
	}
}

func BenchmarkDeref(b *testing.B) {
	v := 1
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if ptr.Deref(&v) != v {
			b.Fatal("Deref changed the value")
		}
	}
}
