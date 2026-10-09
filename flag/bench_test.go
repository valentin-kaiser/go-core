package flag_test

import (
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
