package machine_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/machine"
)

// ID collects hardware facts by running OS commands; it is a startup cost, not a hot path.
func BenchmarkGeneratorID(b *testing.B) {
	g := machine.New().WithCPU().WithMAC().VMFriendly()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := g.ID(); err != nil {
			b.Skip(err)
		}
	}
}
