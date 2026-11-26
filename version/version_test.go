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
		{"v1.2.3", true},
		{"v0.0.1", true},
		{"v10.20.30", true},
		{"1.2.3", false},
		{"v1.2", false},
		{"v1.2.3.4", false},    // This is not valid semantic versioning
		{"v1.2.3-alpha", true}, // Pre-release is valid
		{"v1.2.3+build", true}, // Build metadata is valid
		{"invalid", false},
		{"", false},
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
		{"v1.2.3", 0, 1},
		{"v1.2.3", 1, 2},
		{"v1.2.3", 2, 3},
		{"v1.2.3", 3, 0},  // out of range
		{"invalid", 0, 0}, // invalid tag
		{"v1.2", 2, 0},    // insufficient segments
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
		{"v1.2.3", "1.2.3"},
		{"v1.2.3-alpha", "1.2.3"},
		{"invalid", ""},
		{"", ""},
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

func TestVersionValidate(t *testing.T) {
	v := &version.Release{}

	// Test valid version
	validVersion := &version.Release{
		GitTag:    "v1.2.3",
		GitCommit: "abc123",
		GitShort:  "abc",
		BuildDate: "2024-01-01",
		GoVersion: "go1.21",
		Platform:  "linux/amd64",
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
		{"empty tag", &version.Release{GitTag: "", GitCommit: "abc", GitShort: "abc", BuildDate: "2024-01-01", GoVersion: "go1.21", Platform: "linux/amd64"}},
		{"empty commit", &version.Release{GitTag: "v1.2.3", GitCommit: "", GitShort: "abc", BuildDate: "2024-01-01", GoVersion: "go1.21", Platform: "linux/amd64"}},
		{"empty short", &version.Release{GitTag: "v1.2.3", GitCommit: "abc123", GitShort: "", BuildDate: "2024-01-01", GoVersion: "go1.21", Platform: "linux/amd64"}},
		{"empty build date", &version.Release{GitTag: "v1.2.3", GitCommit: "abc123", GitShort: "abc", BuildDate: "", GoVersion: "go1.21", Platform: "linux/amd64"}},
		{"empty go version", &version.Release{GitTag: "v1.2.3", GitCommit: "abc123", GitShort: "abc", BuildDate: "2024-01-01", GoVersion: "", Platform: "linux/amd64"}},
		{"empty platform", &version.Release{GitTag: "v1.2.3", GitCommit: "abc123", GitShort: "abc", BuildDate: "2024-01-01", GoVersion: "go1.21", Platform: ""}},
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
		{"v1.2.3", version.FormatSemVer},
		{"v2024.10.02", version.FormatCalVerYYYYMMDD},
		{"v24.10.123", version.FormatCalVerYYMMMICRO},
		{"v2024.42", version.FormatCalVerYYYYWW},
		{"v2024.10.02.456", version.FormatCalVerYYYYMMDDMICRO},
		{"invalid", version.FormatUnknown},
		{"", version.FormatUnknown},
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
		{version.FormatSemVer, "semantic"},
		{version.FormatCalVerYYYYMMDD, "calver-yyyy.mm.dd"},
		{version.FormatCalVerYYMMMICRO, "calver-yy.mm.micro"},
		{version.FormatCalVerYYYYWW, "calver-yyyy.ww"},
		{version.FormatCalVerYYYYMMDDMICRO, "calver-yyyy.mm.dd.micro"},
		{version.FormatUnknown, "unknown"},
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
		{"v1.2.3", false, 0, 0, 0, 1, 2, 3},
		{"v2024.10.02", false, 2024, 10, 2, 0, 0, 0},
		{"v24.10.123", false, 24, 10, 0, 0, 0, 0},
		{"v2024.42", false, 2024, 0, 0, 0, 0, 0},
		{"v2024.10.02.456", false, 2024, 10, 2, 0, 0, 0},
		{"invalid", true, 0, 0, 0, 0, 0, 0},
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
		{"v1.2.3", true},
		{"v2024.10.02", true},
		{"v24.10.123", true},
		{"v2024.42", true},
		{"v2024.10.02.456", true},
		{"invalid", false},
		{"", false},
		{"1.2.3", false}, // missing 'v' prefix for semver
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
		{"v1.2.3", "v1.2.3", 0, false},
		{"v1.2.3", "v1.2.4", -1, false},
		{"v1.2.4", "v1.2.3", 1, false},
		{"v1.3.0", "v1.2.9", 1, false},
		{"v2.0.0", "v1.9.9", 1, false},

		// CalVer YYYY.MM.DD comparisons
		{"v2024.10.02", "v2024.10.02", 0, false},
		{"v2024.10.01", "v2024.10.02", -1, false},
		{"v2024.10.02", "v2024.10.01", 1, false},
		{"v2024.11.01", "v2024.10.31", 1, false},
		{"v2025.01.01", "v2024.12.31", 1, false},

		// Different formats should error
		{"v1.2.3", "v2024.10.02", 0, true},
		{"v2024.10.02", "v24.10.123", 0, true},

		// Invalid versions should error
		{"invalid", "v1.2.3", 0, true},
		{"v1.2.3", "invalid", 0, true},
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
		{"v1.2.3", false},
		{"v2024.10.02", false},
		{"v24.10.123", false},
		{"v2024.42", false},
		{"v2024.10.02.456", false},
		{"invalid", true},
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
		{"v2024.10.02", version.IsCalVerYYYYMMDD, true},
		{"2024.10.02", version.IsCalVerYYYYMMDD, true},
		{"v1.2.3", version.IsCalVerYYYYMMDD, false},

		{"v24.10.123", version.IsCalVerYYMMMICRO, true},
		{"24.10.123", version.IsCalVerYYMMMICRO, true},
		{"v1.2.3", version.IsCalVerYYMMMICRO, false},

		{"v2024.42", version.IsCalVerYYYYWW, true},
		{"2024.42", version.IsCalVerYYYYWW, true},
		{"v1.2.3", version.IsCalVerYYYYWW, false},

		{"v2024.10.02.456", version.IsCalVerYYYYMMDDMICRO, true},
		{"2024.10.02.456", version.IsCalVerYYYYMMDDMICRO, true},
		{"v1.2.3", version.IsCalVerYYYYMMDDMICRO, false},
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
