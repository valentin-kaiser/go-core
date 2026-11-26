package version_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/version"
)

func BenchmarkParseVersionSemver(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := version.ParseVersion("v1.22.3-rc.1+build.5")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseVersionCalVer(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := version.ParseVersion("2026.10.09")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompareVersions(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := version.CompareVersions("v1.22.3", "v1.22.10")
		if err != nil {
			b.Fatal(err)
		}
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
		_, err := version.GetVersionComponents("v1.22.3")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGet(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = version.Get()
	}
}
