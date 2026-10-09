package flag_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/valentin-kaiser/go-core/flag"
)

func BenchmarkEnvVarName(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = flag.EnvVarName("server.read-timeout")
	}
}

func BenchmarkEnvVarNameParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = flag.EnvVarName("server.read-timeout")
		}
	})
}

func TestEnvVarNameFollowsRegistrationAndPrefix(t *testing.T) {
	oldPrefix := flag.Prefix
	t.Cleanup(func() { flag.Prefix = oldPrefix })
	flag.Prefix = ""

	if got := flag.EnvVarName("cache.test-name"); got != "CACHE_TEST_NAME" {
		t.Fatalf("got %q", got)
	}
	flag.Prefix = "APP"
	if got := flag.EnvVarName("cache.test-name"); got != "APP_CACHE_TEST_NAME" {
		t.Fatalf("after prefix change: got %q", got)
	}
	flag.RegisterEnvVar("cache.test-name", "other.name")
	if got := flag.EnvVarName("cache.test-name"); got != "APP_OTHER_NAME" {
		t.Fatalf("after RegisterEnvVar: got %q", got)
	}
}

// A reader that misses the cache while RegisterEnvVar runs must not store the old mapping after
// the registration has cleared the cache.
func TestEnvVarNameConcurrentRegistrationIsNotLost(t *testing.T) {
	oldPrefix := flag.Prefix
	t.Cleanup(func() { flag.Prefix = oldPrefix })
	flag.Prefix = ""

	for i := 0; i < 500; i++ {
		name := fmt.Sprintf("race.name-%d", i)
		want := fmt.Sprintf("REGISTERED_%d", i)

		var wg sync.WaitGroup
		for r := 0; r < 4; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = flag.EnvVarName(name)
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			flag.RegisterEnvVar(name, fmt.Sprintf("registered.%d", i))
		}()
		wg.Wait()

		if got := flag.EnvVarName(name); got != want {
			t.Fatalf("iteration %d: got %q, want %q", i, got, want)
		}
	}
}
