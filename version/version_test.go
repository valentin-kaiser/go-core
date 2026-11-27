package version_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/version"
)

func TestGet(t *testing.T) {
	version := version.Get()
	if version == nil {
		t.Error("GetVersion() returned nil")
		return
	}

	if version.GitTag == "" {
		t.Error("GitTag should not be empty")
	}

	if version.GitCommit == "" {
		t.Error("GitCommit should not be empty")
	}

	if version.GitShort == "" {
		t.Error("GitShort should not be empty")
	}

	if version.BuildDate == "" {
		t.Error("BuildDate should not be empty")
	}

	if version.GoVersion == "" {
		t.Error("GoVersion should not be empty")
	}

	if version.Platform == "" {
		t.Error("Platform should not be empty")
	}

	if version.Modules == nil {
		t.Error("Modules should not be nil")
	}
}

func TestMajor(t *testing.T) {
	// Test with default tag
	originalTag := version.GitTag
	defer func() { version.GitTag = originalTag }()

	version.GitTag = "v1.2.3"
	major := version.Major()
	if major != 1 {
		t.Errorf("Expected major version 1, got %d", major)
	}

	// Test with invalid tag
	version.GitTag = "invalid"
	major = version.Major()
	if major != 0 {
		t.Errorf("Expected major version 0 for invalid tag, got %d", major)
	}
}

func TestMinor(t *testing.T) {
	originalTag := version.GitTag
	defer func() { version.GitTag = originalTag }()

	version.GitTag = "v1.2.3"
	minor := version.Minor()
	if minor != 2 {
		t.Errorf("Expected minor version 2, got %d", minor)
	}

	// Test with invalid tag
	version.GitTag = "invalid"
	minor = version.Minor()
	if minor != 0 {
		t.Errorf("Expected minor version 0 for invalid tag, got %d", minor)
	}
}

func TestPatch(t *testing.T) {
	originalTag := version.GitTag
	defer func() { version.GitTag = originalTag }()

	version.GitTag = "v1.2.3"
	patch := version.Patch()
	if patch != 3 {
		t.Errorf("Expected patch version 3, got %d", patch)
	}

	// Test with invalid tag
	version.GitTag = "invalid"
	patch = version.Patch()
	if patch != 0 {
		t.Errorf("Expected patch version 0 for invalid tag, got %d", patch)
	}
}

func TestString(t *testing.T) {
	originalTag := version.GitTag
	defer func() { version.GitTag = originalTag }()

	version.GitTag = "v1.2.3"
	str := version.String()
	if str != "1.2.3" {
		t.Errorf("Expected version string '1.2.3', got '%s'", str)
	}

	// Test with pre-release tag
	version.GitTag = "v1.2.3-alpha"
	str = version.String()
	if str != "1.2.3" {
		t.Errorf("Expected version string '1.2.3' for pre-release, got '%s'", str)
	}
}

func TestIsSemver(t *testing.T) {
	testCases := []struct {
		tag      string
		expected bool
	}{
		{tag: "v1.2.3", expected: true},
		{tag: "v0.0.1", expected: true},
		{tag: "v10.20.30", expected: true},
		{tag: "1.2.3", expected: false},
		{tag: "v1.2", expected: false},
		{tag: "v1.2.3.4", expected: false},    // This is not valid semantic versioning
		{tag: "v1.2.3-alpha", expected: true}, // Pre-release is valid
		{tag: "v1.2.3+build", expected: true}, // Build metadata is valid
		{tag: "invalid", expected: false},
		{tag: "", expected: false},
	}

	for _, tc := range testCases {
		result := version.IsSemver(tc.tag)
		if result != tc.expected {
			t.Errorf("IsGitTag(%q) = %v, expected %v", tc.tag, result, tc.expected)
		}
	}
}

func TestParseSemver(t *testing.T) {
	testCases := []struct {
		tag      string
		segment  int
		expected int
	}{
		{tag: "v1.2.3", segment: 0, expected: 1},
		{tag: "v1.2.3", segment: 1, expected: 2},
		{tag: "v1.2.3", segment: 2, expected: 3},
		{tag: "v1.2.3", segment: 3, expected: 0},  // out of range
		{tag: "invalid", segment: 0, expected: 0}, // invalid tag
		{tag: "v1.2", segment: 2, expected: 0},    // insufficient segments
	}

	for _, tc := range testCases {
		result := version.ParseSemver(tc.tag, tc.segment)
		if result != tc.expected {
			t.Errorf("ParseTagSegment(%q, %d) = %d, expected %d", tc.tag, tc.segment, result, tc.expected)
		}
	}
}

func TestExtractSemanticVersion(t *testing.T) {
	testCases := []struct {
		tag      string
		expected string
	}{
		{tag: "v1.2.3", expected: "1.2.3"},
		{tag: "v1.2.3-alpha", expected: "1.2.3"},
		{tag: "invalid", expected: ""},
		{tag: "", expected: ""},
	}

	for _, tc := range testCases {
		result := version.ExtractSemanticVersion(tc.tag)
		if result != tc.expected {
			t.Errorf("ParseTagVersion(%q) = %q, expected %q", tc.tag, result, tc.expected)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	v1 := &version.Release{
		GitTag:    "v1.2.3",
		GitCommit: "abc123",
		GitShort:  "abc",
	}

	v2 := &version.Release{
		GitTag:    "v1.2.3",
		GitCommit: "abc123",
		GitShort:  "abc",
	}

	v3 := &version.Release{
		GitTag:    "v1.2.4",
		GitCommit: "def456",
		GitShort:  "def",
	}

	// Test identical versions
	if !v1.Compare(v2) {
		t.Error("Identical versions should be equal")
	}

	// Test different versions
	if v1.Compare(v3) {
		t.Error("Different versions should not be equal")
	}
}

func TestVersionCompareTag(t *testing.T) {
	v1 := &version.Release{GitTag: "v1.2.3"}
	v2 := &version.Release{GitTag: "v1.2.3"}
	v3 := &version.Release{GitTag: "v1.2.4"}

	if !v1.CompareTag(v2) {
		t.Error("Identical tags should be equal")
	}

	if v1.CompareTag(v3) {
		t.Error("Different tags should not be equal")
	}
}

func TestVersionCompareCommit(t *testing.T) {
	v1 := &version.Release{GitCommit: "abc123", GitShort: "abc"}
	v2 := &version.Release{GitCommit: "abc123", GitShort: "abc"}
	v3 := &version.Release{GitCommit: "def456", GitShort: "def"}

	if !v1.CompareCommit(v2) {
		t.Error("Identical commits should be equal")
	}

	if v1.CompareCommit(v3) {
		t.Error("Different commits should not be equal")
	}
}

const (
	validBuildDate = "2024-01-01"
	validGoVersion = "go1.21"
	validPlatform  = "linux/amd64"
	validCommit    = "abc123"
)

func TestVersionValidate(t *testing.T) {
	v := &version.Release{}

	// Test valid version
	validVersion := &version.Release{
		GitTag:    "v1.2.3",
		GitCommit: validCommit,
		GitShort:  "abc",
		BuildDate: validBuildDate,
		GoVersion: validGoVersion,
		Platform:  validPlatform,
	}

	err := v.Validate(validVersion)
	if err != nil {
		t.Errorf("Valid version should pass validation: %v", err)
	}

	// Test invalid versions
	testCases := []struct {
		name    string
		version *version.Release
	}{
		{name: "empty tag", version: &version.Release{GitTag: "", GitCommit: "abc", GitShort: "abc", BuildDate: validBuildDate, GoVersion: validGoVersion, Platform: validPlatform}},
		{name: "empty commit", version: &version.Release{GitTag: "v1.2.3", GitCommit: "", GitShort: "abc", BuildDate: validBuildDate, GoVersion: validGoVersion, Platform: validPlatform}},
		{name: "empty short", version: &version.Release{GitTag: "v1.2.3", GitCommit: validCommit, GitShort: "", BuildDate: validBuildDate, GoVersion: validGoVersion, Platform: validPlatform}},
		{name: "empty build date", version: &version.Release{GitTag: "v1.2.3", GitCommit: validCommit, GitShort: "abc", BuildDate: "", GoVersion: validGoVersion, Platform: validPlatform}},
		{name: "empty go version", version: &version.Release{GitTag: "v1.2.3", GitCommit: validCommit, GitShort: "abc", BuildDate: validBuildDate, GoVersion: "", Platform: validPlatform}},
		{name: "empty platform", version: &version.Release{GitTag: "v1.2.3", GitCommit: validCommit, GitShort: "abc", BuildDate: validBuildDate, GoVersion: validGoVersion, Platform: ""}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := v.Validate(tc.version)
			if err == nil {
				t.Errorf("Expected validation to fail for %s", tc.name)
			}
		})
	}
}

func TestDetectVersionFormat(t *testing.T) {
	testCases := []struct {
		tag      string
		expected version.Format
	}{
		{tag: "v1.2.3", expected: version.FormatSemVer},
		{tag: "v2024.10.02", expected: version.FormatCalVerYYYYMMDD},
		{tag: "v24.10.123", expected: version.FormatCalVerYYMMMICRO},
		{tag: "v2024.42", expected: version.FormatCalVerYYYYWW},
		{tag: "v2024.10.02.456", expected: version.FormatCalVerYYYYMMDDMICRO},
		{tag: "invalid", expected: version.FormatUnknown},
		{tag: "", expected: version.FormatUnknown},
	}

	for _, tc := range testCases {
		result := version.DetectFormat(tc.tag)
		if result != tc.expected {
			t.Errorf("DetectVersionFormat(%q) = %v, expected %v", tc.tag, result, tc.expected)
		}
	}
}

func TestVersionFormatString(t *testing.T) {
	testCases := []struct {
		format   version.Format
		expected string
	}{
		{format: version.FormatSemVer, expected: "semantic"},
		{format: version.FormatCalVerYYYYMMDD, expected: "calver-yyyy.mm.dd"},
		{format: version.FormatCalVerYYMMMICRO, expected: "calver-yy.mm.micro"},
		{format: version.FormatCalVerYYYYWW, expected: "calver-yyyy.ww"},
		{format: version.FormatCalVerYYYYMMDDMICRO, expected: "calver-yyyy.mm.dd.micro"},
		{format: version.FormatUnknown, expected: "unknown"},
	}

	for _, tc := range testCases {
		result := tc.format.String()
		if result != tc.expected {
			t.Errorf("VersionFormat.String() = %q, expected %q", result, tc.expected)
		}
	}
}

func TestParseVersion(t *testing.T) {
	testCases := []struct {
		tag           string
		expectError   bool
		expectedYear  int
		expectedMonth int
		expectedDay   int
		expectedMajor int
		expectedMinor int
		expectedPatch int
	}{
		{tag: "v1.2.3", expectError: false, expectedYear: 0, expectedMonth: 0, expectedDay: 0, expectedMajor: 1, expectedMinor: 2, expectedPatch: 3},
		{tag: "v2024.10.02", expectError: false, expectedYear: 2024, expectedMonth: 10, expectedDay: 2, expectedMajor: 0, expectedMinor: 0, expectedPatch: 0},
		{tag: "v24.10.123", expectError: false, expectedYear: 24, expectedMonth: 10, expectedDay: 0, expectedMajor: 0, expectedMinor: 0, expectedPatch: 0},
		{tag: "v2024.42", expectError: false, expectedYear: 2024, expectedMonth: 0, expectedDay: 0, expectedMajor: 0, expectedMinor: 0, expectedPatch: 0},
		{tag: "v2024.10.02.456", expectError: false, expectedYear: 2024, expectedMonth: 10, expectedDay: 2, expectedMajor: 0, expectedMinor: 0, expectedPatch: 0},
		{tag: "invalid", expectError: true, expectedYear: 0, expectedMonth: 0, expectedDay: 0, expectedMajor: 0, expectedMinor: 0, expectedPatch: 0},
	}

	for _, tc := range testCases {
		result, err := version.ParseVersion(tc.tag)
		if tc.expectError {
			if err == nil {
				t.Errorf("ParseVersion(%q) expected error but got none", tc.tag)
			}
			continue
		}

		if err != nil {
			t.Errorf("ParseVersion(%q) unexpected error: %v", tc.tag, err)
			continue
		}

		if result.Year != tc.expectedYear {
			t.Errorf("ParseVersion(%q).Year = %d, expected %d", tc.tag, result.Year, tc.expectedYear)
		}
		if result.Month != tc.expectedMonth {
			t.Errorf("ParseVersion(%q).Month = %d, expected %d", tc.tag, result.Month, tc.expectedMonth)
		}
		if result.Day != tc.expectedDay {
			t.Errorf("ParseVersion(%q).Day = %d, expected %d", tc.tag, result.Day, tc.expectedDay)
		}
		if result.Major != tc.expectedMajor {
			t.Errorf("ParseVersion(%q).Major = %d, expected %d", tc.tag, result.Major, tc.expectedMajor)
		}
		if result.Minor != tc.expectedMinor {
			t.Errorf("ParseVersion(%q).Minor = %d, expected %d", tc.tag, result.Minor, tc.expectedMinor)
		}
		if result.Patch != tc.expectedPatch {
			t.Errorf("ParseVersion(%q).Patch = %d, expected %d", tc.tag, result.Patch, tc.expectedPatch)
		}
	}
}

func TestIsValidVersion(t *testing.T) {
	testCases := []struct {
		tag      string
		expected bool
	}{
		{tag: "v1.2.3", expected: true},
		{tag: "v2024.10.02", expected: true},
		{tag: "v24.10.123", expected: true},
		{tag: "v2024.42", expected: true},
		{tag: "v2024.10.02.456", expected: true},
		{tag: "invalid", expected: false},
		{tag: "", expected: false},
		{tag: "1.2.3", expected: false}, // missing 'v' prefix for semver
	}

	for _, tc := range testCases {
		result := version.IsValidVersion(tc.tag)
		if result != tc.expected {
			t.Errorf("IsValidVersion(%q) = %v, expected %v", tc.tag, result, tc.expected)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	testCases := []struct {
		tag1     string
		tag2     string
		expected int
		hasError bool
	}{
		// SemVer comparisons
		{tag1: "v1.2.3", tag2: "v1.2.3", expected: 0, hasError: false},
		{tag1: "v1.2.3", tag2: "v1.2.4", expected: -1, hasError: false},
		{tag1: "v1.2.4", tag2: "v1.2.3", expected: 1, hasError: false},
		{tag1: "v1.3.0", tag2: "v1.2.9", expected: 1, hasError: false},
		{tag1: "v2.0.0", tag2: "v1.9.9", expected: 1, hasError: false},

		// CalVer YYYY.MM.DD comparisons
		{tag1: "v2024.10.02", tag2: "v2024.10.02", expected: 0, hasError: false},
		{tag1: "v2024.10.01", tag2: "v2024.10.02", expected: -1, hasError: false},
		{tag1: "v2024.10.02", tag2: "v2024.10.01", expected: 1, hasError: false},
		{tag1: "v2024.11.01", tag2: "v2024.10.31", expected: 1, hasError: false},
		{tag1: "v2025.01.01", tag2: "v2024.12.31", expected: 1, hasError: false},

		// Different formats should error
		{tag1: "v1.2.3", tag2: "v2024.10.02", expected: 0, hasError: true},
		{tag1: "v2024.10.02", tag2: "v24.10.123", expected: 0, hasError: true},

		// Invalid versions should error
		{tag1: "invalid", tag2: "v1.2.3", expected: 0, hasError: true},
		{tag1: "v1.2.3", tag2: "invalid", expected: 0, hasError: true},
	}

	for _, tc := range testCases {
		result, err := version.CompareVersions(tc.tag1, tc.tag2)
		if tc.hasError {
			if err == nil {
				t.Errorf("CompareVersions(%q, %q) expected error but got none", tc.tag1, tc.tag2)
			}
			continue
		}

		if err != nil {
			t.Errorf("CompareVersions(%q, %q) unexpected error: %v", tc.tag1, tc.tag2, err)
			continue
		}

		if result != tc.expected {
			t.Errorf("CompareVersions(%q, %q) = %d, expected %d", tc.tag1, tc.tag2, result, tc.expected)
		}
	}
}

func TestGetVersionComponents(t *testing.T) {
	testCases := []struct {
		tag         string
		expectError bool
	}{
		{tag: "v1.2.3", expectError: false},
		{tag: "v2024.10.02", expectError: false},
		{tag: "v24.10.123", expectError: false},
		{tag: "v2024.42", expectError: false},
		{tag: "v2024.10.02.456", expectError: false},
		{tag: "invalid", expectError: true},
	}

	for _, tc := range testCases {
		result, err := version.GetVersionComponents(tc.tag)
		if tc.expectError {
			if err == nil {
				t.Errorf("GetVersionComponents(%q) expected error but got none", tc.tag)
			}
			continue
		}

		if err != nil {
			t.Errorf("GetVersionComponents(%q) unexpected error: %v", tc.tag, err)
			continue
		}

		if result == nil {
			t.Errorf("GetVersionComponents(%q) returned nil result", tc.tag)
			continue
		}

		// Check that format and original are always present
		if _, ok := result["format"]; !ok {
			t.Errorf("GetVersionComponents(%q) missing 'format' field", tc.tag)
		}
		if _, ok := result["original"]; !ok {
			t.Errorf("GetVersionComponents(%q) missing 'original' field", tc.tag)
		}
	}
}

func TestReleaseVersionMethods(t *testing.T) {
	// Test with semantic version
	semverRelease := &version.Release{
		GitTag:        "v1.2.3",
		VersionFormat: version.FormatSemVer,
	}

	if !semverRelease.IsSemVer() {
		t.Error("Expected semverRelease.IsSemVer() to be true")
	}
	if semverRelease.IsCalVer() {
		t.Error("Expected semverRelease.IsCalVer() to be false")
	}

	// Test with CalVer version
	calverRelease := &version.Release{
		GitTag:        "v2024.10.02",
		VersionFormat: version.FormatCalVerYYYYMMDD,
	}

	if calverRelease.IsSemVer() {
		t.Error("Expected calverRelease.IsSemVer() to be false")
	}
	if !calverRelease.IsCalVer() {
		t.Error("Expected calverRelease.IsCalVer() to be true")
	}
}

func TestCalVerFormats(t *testing.T) {
	testCases := []struct {
		tag      string
		function func(string) bool
		expected bool
	}{
		{tag: "v2024.10.02", function: version.IsCalVerYYYYMMDD, expected: true},
		{tag: "2024.10.02", function: version.IsCalVerYYYYMMDD, expected: true},
		{tag: "v1.2.3", function: version.IsCalVerYYYYMMDD, expected: false},

		{tag: "v24.10.123", function: version.IsCalVerYYMMMICRO, expected: true},
		{tag: "24.10.123", function: version.IsCalVerYYMMMICRO, expected: true},
		{tag: "v1.2.3", function: version.IsCalVerYYMMMICRO, expected: false},

		{tag: "v2024.42", function: version.IsCalVerYYYYWW, expected: true},
		{tag: "2024.42", function: version.IsCalVerYYYYWW, expected: true},
		{tag: "v1.2.3", function: version.IsCalVerYYYYWW, expected: false},

		{tag: "v2024.10.02.456", function: version.IsCalVerYYYYMMDDMICRO, expected: true},
		{tag: "2024.10.02.456", function: version.IsCalVerYYYYMMDDMICRO, expected: true},
		{tag: "v1.2.3", function: version.IsCalVerYYYYMMDDMICRO, expected: false},
	}

	for _, tc := range testCases {
		result := tc.function(tc.tag)
		if result != tc.expected {
			t.Errorf("Function(%q) = %v, expected %v", tc.tag, result, tc.expected)
		}
	}
}

func TestMajorMinorPatchWithCalVer(t *testing.T) {
	originalTag := version.GitTag
	defer func() { version.GitTag = originalTag }()

	// Test with CalVer YYYY.MM.DD
	version.GitTag = "v2024.10.02"
	if major := version.Major(); major != 2024 {
		t.Errorf("Expected Major() to return 2024 for CalVer, got %d", major)
	}
	if minor := version.Minor(); minor != 10 {
		t.Errorf("Expected Minor() to return 10 for CalVer, got %d", minor)
	}
	if patch := version.Patch(); patch != 2 {
		t.Errorf("Expected Patch() to return 2 for CalVer, got %d", patch)
	}

	// Test with CalVer YY.MM.MICRO
	version.GitTag = "v24.10.123"
	if major := version.Major(); major != 24 {
		t.Errorf("Expected Major() to return 24 for CalVer YY.MM.MICRO, got %d", major)
	}
	if minor := version.Minor(); minor != 10 {
		t.Errorf("Expected Minor() to return 10 for CalVer YY.MM.MICRO, got %d", minor)
	}
	if patch := version.Patch(); patch != 123 {
		t.Errorf("Expected Patch() to return 123 for CalVer YY.MM.MICRO, got %d", patch)
	}
}
