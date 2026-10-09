package version_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/version"
)

// ParseVersion no longer calls ParseSemver per segment; the result has to stay the same.
func TestParseVersionSemverSegments(t *testing.T) {
	tags := []string{"v1.2.3", "1.2.3", "v10.20.30", "v0.0.0", "v1.22.3-rc.1+build.5", "v1.2.3-beta", "v1.0.0-alpha.1"}
	for _, tag := range tags {
		if !version.IsSemver(tag) {
			continue
		}
		pv, err := version.ParseVersion(tag)
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		if pv.Major != version.ParseSemver(tag, 0) || pv.Minor != version.ParseSemver(tag, 1) || pv.Patch != version.ParseSemver(tag, 2) {
			t.Errorf("%s: got %d.%d.%d, ParseSemver says %d.%d.%d", tag, pv.Major, pv.Minor, pv.Patch,
				version.ParseSemver(tag, 0), version.ParseSemver(tag, 1), version.ParseSemver(tag, 2))
		}
	}
}

// Get caches the parsed tag, but has to follow a changed GitTag and hand out independent copies.
func TestGetFollowsGitTag(t *testing.T) {
	old := version.GitTag
	t.Cleanup(func() { version.GitTag = old })

	version.GitTag = "v1.2.3"
	r := version.Get()
	if r.ParsedVersion == nil || r.ParsedVersion.Major != 1 || r.ParsedVersion.Minor != 2 || r.ParsedVersion.Patch != 3 {
		t.Fatalf("v1.2.3: %+v", r.ParsedVersion)
	}
	r.ParsedVersion.Major = 99
	if again := version.Get(); again.ParsedVersion.Major != 1 {
		t.Fatalf("a changed result leaked into later calls: %d", again.ParsedVersion.Major)
	}

	version.GitTag = "v4.5.6"
	if r := version.Get(); r.ParsedVersion == nil || r.ParsedVersion.Major != 4 || r.ParsedVersion.Minor != 5 {
		t.Fatalf("after GitTag change: %+v", r.ParsedVersion)
	}

	version.GitTag = "not-a-version"
	if r := version.Get(); r.ParsedVersion != nil || r.VersionFormat != version.FormatUnknown {
		t.Fatalf("unparsable tag: %+v format=%v", r.ParsedVersion, r.VersionFormat)
	}
}

// The pre-check in DetectFormat must not change which format a tag gets.
func TestDetectFormatTable(t *testing.T) {
	cases := map[string]version.Format{
		"v1.2.3":       version.FormatSemVer,
		"v10.20.30":    version.FormatSemVer,
		"v1.2.3-rc.1":  version.FormatSemVer,
		"2026.10.09":   version.FormatCalVerYYYYMMDD,
		"v2026.10.9":   version.FormatCalVerYYYYMMDD,
		"26.10.5":      version.FormatCalVerYYMMMICRO,
		"2026.41":      version.FormatCalVerYYYYWW,
		"2026.10.09.3": version.FormatCalVerYYYYMMDDMICRO,
		"nonsense":     version.FormatUnknown,
		"":             version.FormatUnknown,
		"2026":         version.FormatUnknown,
		"20261.10.09":  version.FormatUnknown,
		"2026.13.40":   version.FormatUnknown,
	}
	for tag, want := range cases {
		if got := version.DetectFormat(tag); got != want {
			t.Errorf("DetectFormat(%q) = %v, want %v", tag, got, want)
		}
	}
}
