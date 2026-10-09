package version_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/version"
)

func BenchmarkParseVersionSemver(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = version.ParseVersion("v1.22.3-rc.1+build.5")
	}
}

func BenchmarkParseVersionCalVer(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = version.ParseVersion("2026.10.09")
	}
}

func BenchmarkCompareVersions(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = version.CompareVersions("v1.22.3", "v1.22.10")
	}
}

func BenchmarkDetectFormat(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = version.DetectFormat("v1.22.3")
	}
}

func BenchmarkGetVersionComponents(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = version.GetVersionComponents("v1.22.3")
	}
}

func BenchmarkGet(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = version.Get()
	}
}
