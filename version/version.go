// Package version provides utilities to manage, retrieve, and validate version
// information of a Go application at runtime, supporting multiple version formats
// including Semantic Versioning (SemVer) and Calendar Versioning (CalVer).
//
// This package captures key metadata about the application build, including the
// Git tag, full and short commit hashes, build date, Go runtime version, target
// platform, and the list of Go modules used in the build. These values can be
// set at build time via linker flags (-ldflags), or automatically extracted from
// debug.BuildInfo at runtime when available.
//
// Version Information Sources:
//
// 1. Build-time ldflags (recommended for releases):
//   - Provides precise control over version information
//   - Set via -ldflags during go build
//
// 2. Runtime debug.BuildInfo (automatic fallback):
//   - Automatically used when ldflags are not set
//   - Extracts vcs.revision (git commit), vcs.time (build date), vcs.modified
//   - Available when building with Go 1.18+ and VCS information embedded
//   - Can be manually triggered via LoadFromBuildInfo()
//
// Supported Version Formats:
//
// 1. Semantic Versioning (SemVer): vX.Y.Z format
//   - Example: v1.2.3, v2.0.0-alpha
//   - Follows semantic versioning specification
//
// 2. Calendar Versioning (CalVer): Various formats based on dates
//   - YYYY.MM.DD: v2024.10.02
//   - YY.MM.MICRO: v24.10.123
//   - YYYY.WW: v2024.42 (year and week number)
//   - YYYY.MM.DD.MICRO: v2024.10.02.456
//
// The package automatically detects the version format and provides appropriate
// parsing, validation, and comparison functions. It includes functions to parse
// version components, validate version structs, compare versions, and extract
// semantic or calendar-based information from Git tags.
//
// The module list is automatically populated at runtime using debug.BuildInfo
// (available since Go 1.12+), which extracts module dependencies embedded by the
// Go build system.
//
// Typical usage involves setting the version variables during build with
// `-ldflags`, for example in a Makefile:
//
//	GIT_TAG := $(shell git describe --tags)
//	GIT_COMMIT := $(shell git rev-parse HEAD)
//	GIT_SHORT := $(shell git rev-parse --short HEAD)
//	BUILD_TIME := $(shell date +%FT%T%z)
//	VERSION_PACKAGE := github.com/valentin-kaiser/go-core/v2/version
//
//	go build -ldflags "-X $(VERSION_PACKAGE).GitTag=$(GIT_TAG) \
//	                 -X $(VERSION_PACKAGE).GitCommit=$(GIT_COMMIT) \
//	                 -X $(VERSION_PACKAGE).GitShort=$(GIT_SHORT) \
//	                 -X $(VERSION_PACKAGE).BuildDate=$(BUILD_TIME)" main.go
//
// The package defines the Release struct encapsulating all relevant fields,
// the ParsedVersion struct for version components, and the VersionParser
// interface for extensible version format support.
//
// Example usage with different version formats:
//
//	// Get current version information
//	v := version.GetVersion()
//	fmt.Printf("App version: %s (commit %s)\n", v.GitTag, v.GitShort)
//	fmt.Printf("Format: %s\n", v.VersionFormat.String())
//
//	// Parse any supported version format
//	parsed, err := version.ParseVersion("v2024.10.02")
//	if err == nil {
//		fmt.Printf("Year: %d, Month: %d, Day: %d\n", parsed.Year, parsed.Month, parsed.Day)
//	}
//
//	// Compare versions of the same format
//	result, err := version.CompareVersions("v1.2.3", "v1.2.4")
//	if err == nil && result < 0 {
//		fmt.Println("First version is older")
//	}
package version

import (
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/valentin-kaiser/go-core/v2/apperror"
	"github.com/valentin-kaiser/go-core/v2/logging"
)

const (
	unknownValue      = "unknown"
	develVersion      = "(devel)"
	vcsRevisionKey    = "vcs.revision"
	vcsTimeKey        = "vcs.time"
	shortCommitLength = 7

	versionPrefix       = "v"
	preReleaseSeparator = "-"
	segmentSeparator    = "."
	splitPartsCount     = 2
	patchSegmentIndex   = 2

	calverShortYearDigits = 2
	calverLongYearDigits  = 4
	maxShortYear          = 99
	maxMonth              = 12
	maxDay                = 31
	maxWeek               = 53

	// Match counts include the full match at index 0 followed by the capture groups.
	twoPartMatchCount   = 3
	threePartMatchCount = 4
	fourPartMatchCount  = 5
	microIndex          = 3

	formatNameSemVer              = "semantic"
	formatNameCalVerYYYYMMDD      = "calver-yyyy.mm.dd"
	formatNameCalVerYYMMMICRO     = "calver-yy.mm.micro"
	formatNameCalVerYYYYWW        = "calver-yyyy.ww"
	formatNameCalVerYYYYMMDDMICRO = "calver-yyyy.mm.dd.micro"

	componentFormat   = "format"
	componentOriginal = "original"
	componentMajor    = "major"
	componentMinor    = "minor"
	componentPatch    = "patch"
	componentMicro    = "micro"
	componentYear     = "year"
	componentMonth    = "month"
	componentDay      = "day"
	componentWeek     = "week"
)

const (
	// FormatUnknown represents an unknown or unsupported version format
	FormatUnknown Format = iota
	// FormatSemVer represents semantic versioning (vX.Y.Z)
	FormatSemVer
	// FormatCalVerYYYYMMDD represents calendar versioning YYYY.MM.DD
	FormatCalVerYYYYMMDD
	// FormatCalVerYYMMMICRO represents calendar versioning YY.MM.MICRO
	FormatCalVerYYMMMICRO
	// FormatCalVerYYYYWW represents calendar versioning YYYY.WW
	FormatCalVerYYYYWW
	// FormatCalVerYYYYMMDDMICRO represents calendar versioning YYYY.MM.DD.MICRO
	FormatCalVerYYYYMMDDMICRO
)

var (
	// GitTag is the release tag of the application, typically in the format "vX.Y.Z".
	GitTag = "v0.0.0"
	// GitCommit is the full commit hash of the application at build time.
	GitCommit = unknownValue
	// GitShort is the short commit hash of the application at build time.
	GitShort = unknownValue
	// BuildDate is the date and time when the application was built.
	BuildDate = unknownValue
	// GoVersion is the version of the Go runtime used to build the application.
	GoVersion = runtime.Version()
	// Platform is the target platform of the application, formatted as "GOOS/GOARCH".
	Platform = runtime.GOOS + "/" + runtime.GOARCH
	// Modules is a list of Go modules used in the application, populated from debug.BuildInfo.
	Modules = make([]*Module, 0)
)

var (
	semver = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)(?:-[a-zA-Z0-9\-\.]+)?(?:\+[a-zA-Z0-9\-\.]+)?$`)
	// CalVer patterns for common formats
	calverYYYYMMDD      = regexp.MustCompile(`^v?([0-9]{4})\.([0-9]{1,2})\.([0-9]{1,2})$`)           // YYYY.MM.DD
	calverYYMMMICRO     = regexp.MustCompile(`^v?([0-9]{2})\.([0-9]{1,2})\.([0-9]+)$`)               // YY.MM.MICRO
	calverYYYYWW        = regexp.MustCompile(`^v?([0-9]{4})\.([0-9]{1,2})$`)                         // YYYY.WW
	calverYYYYMMDDMICRO = regexp.MustCompile(`^v?([0-9]{4})\.([0-9]{1,2})\.([0-9]{1,2})\.([0-9]+)$`) // YYYY.MM.DD.MICRO

	logger = logging.GetPackageLogger("version")
)

// lastTag remembers the result for the most recent tag. GitTag is set at link time and
// does not change while the program runs, so Get does not need to parse it on every call.
var lastTag atomic.Pointer[tagInfo]

// Format represents the type of version format
type Format int

// String returns the string representation of the version format
func (f Format) String() string {
	switch f {
	case FormatSemVer:
		return formatNameSemVer
	case FormatCalVerYYYYMMDD:
		return formatNameCalVerYYYYMMDD
	case FormatCalVerYYMMMICRO:
		return formatNameCalVerYYMMMICRO
	case FormatCalVerYYYYWW:
		return formatNameCalVerYYYYWW
	case FormatCalVerYYYYMMDDMICRO:
		return formatNameCalVerYYYYMMDDMICRO
	default:
		return unknownValue
	}
}

// Parser defines the interface for parsing and validating version strings
type Parser interface {
	// Parse extracts version components from a version string
	Parse(tag string) (*ParsedVersion, error)
	// IsValid checks if a version string is valid for this format
	IsValid(tag string) bool
	// Format returns the version format type
	Format() Format
	// Compare compares two version strings, returns -1, 0, or 1
	Compare(tag1, tag2 string) (int, error)
}

// ParsedVersion represents the parsed components of a version string
type ParsedVersion struct {
	Original string         `json:"original"`
	Format   Format         `json:"format"`
	Major    int            `json:"major,omitempty"`
	Minor    int            `json:"minor,omitempty"`
	Patch    int            `json:"patch,omitempty"`
	Micro    int            `json:"micro,omitempty"`
	Year     int            `json:"year,omitempty"`
	Month    int            `json:"month,omitempty"`
	Day      int            `json:"day,omitempty"`
	Week     int            `json:"week,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
}

// String returns the version string without the "v" prefix
func (pv *ParsedVersion) String() string {
	version, _, _ := strings.Cut(pv.Original, preReleaseSeparator)
	return strings.TrimPrefix(version, versionPrefix)
}

// Release represents the version information of the application.
// It includes the Git tag, commit hash, build date, Go version, platform, and a list of modules.
type Release struct {
	ID            uint64         `json:"-" gorm:"primaryKey"`
	GitTag        string         `json:"gitTag" gorm:"uniqueIndex:idx_version_module"`
	GitCommit     string         `json:"gitCommit" gorm:"uniqueIndex:idx_version_module"`
	GitShort      string         `json:"gitShort"`
	BuildDate     string         `json:"buildDate" gorm:"uniqueIndex:idx_version_module"`
	GoVersion     string         `json:"goVersion" gorm:"uniqueIndex:idx_version_module"`
	Platform      string         `json:"platform" gorm:"uniqueIndex:idx_version_module"`
	Modules       []*Module      `json:"modules" gorm:"-"`
	VersionFormat Format         `json:"versionFormat" gorm:"-"`
	ParsedVersion *ParsedVersion `json:"parsedVersion" gorm:"-"`
}

// Compare compares the Git tag and commit hash of the current version with another version.
func (v *Release) Compare(c *Release) bool {
	return v.CompareTag(c) && v.CompareCommit(c)
}

// CompareTag compares the Git tag of the current version with another version.
func (v *Release) CompareTag(c *Release) bool {
	return v.GitTag == c.GitTag
}

// CompareCommit compares the Git commit hash and short hash of the current version with another version.
func (v *Release) CompareCommit(c *Release) bool {
	return v.GitCommit == c.GitCommit && v.GitShort == c.GitShort
}

// CompareVersions compares the version of the current release with another release using proper version comparison
func (v *Release) CompareVersions(c *Release) (int, error) {
	return CompareVersions(v.GitTag, c.GitTag)
}

// GetVersionComponents returns the parsed version components
func (v *Release) GetVersionComponents() (map[string]any, error) {
	if v.ParsedVersion != nil {
		return GetVersionComponents(v.GitTag)
	}
	return GetVersionComponents(v.GitTag)
}

// IsCalVer returns true if the release uses Calendar Versioning
func (v *Release) IsCalVer() bool {
	return v.VersionFormat == FormatCalVerYYYYMMDD ||
		v.VersionFormat == FormatCalVerYYMMMICRO ||
		v.VersionFormat == FormatCalVerYYYYWW ||
		v.VersionFormat == FormatCalVerYYYYMMDDMICRO
}

// IsSemVer returns true if the release uses Semantic Versioning
func (v *Release) IsSemVer() bool {
	return v.VersionFormat == FormatSemVer
}

// Validate checks if the provided version information is valid.
func (v *Release) Validate(change *Release) error {
	if strings.TrimSpace(change.GitTag) == "" {
		return apperror.NewError("tag cannot be empty")
	}

	if strings.TrimSpace(change.GitCommit) == "" {
		return apperror.NewError("commit cannot be empty")
	}

	if strings.TrimSpace(change.GitShort) == "" {
		return apperror.NewError("short commit cannot be empty")
	}

	if strings.TrimSpace(change.BuildDate) == "" {
		return apperror.NewError("build date cannot be empty")
	}

	if strings.TrimSpace(change.GoVersion) == "" {
		return apperror.NewError("go version cannot be empty")
	}

	if strings.TrimSpace(change.Platform) == "" {
		return apperror.NewError("platform cannot be empty")
	}

	return nil
}

// Module represents a module dependency of the application.
// It includes the module path, version, checksum, and an optional replacement module.
type Module struct {
	Path    string
	Version string
	Sum     string
	Replace *Module `json:",omitempty"`
}

// fromBuildInfo populates the Module struct with information from the debug.Module.
func (m *Module) fromBuildInfo(mod *debug.Module) *Module {
	m.Path = mod.Path
	m.Version = mod.Version
	m.Sum = mod.Sum
	if mod.Replace != nil {
		m.Replace = &Module{}
		m.Replace.fromBuildInfo(mod.Replace)
	}
	return m
}

// tagInfo is the detected format and parsed version of a git tag
type tagInfo struct {
	tag    string
	format Format
	parsed *ParsedVersion
}

// SemVerParser implements VersionParser for semantic versioning
type SemVerParser struct{}

// Parse parses a semantic version tag and returns a ParsedVersion struct with major, minor, and patch components.
func (p *SemVerParser) Parse(tag string) (*ParsedVersion, error) {
	if !p.IsValid(tag) {
		return nil, apperror.NewError("invalid semantic version format")
	}

	return p.parseValid(tag), nil
}

// IsValid checks if the given tag is a valid semantic version format.
func (p *SemVerParser) IsValid(tag string) bool {
	return IsSemver(tag)
}

// Format returns the version format type handled by this parser.
func (p *SemVerParser) Format() Format {
	return FormatSemVer
}

// Compare compares two semantic version tags and returns -1, 0, or 1.
func (p *SemVerParser) Compare(tag1, tag2 string) (int, error) {
	pv1, err := p.Parse(tag1)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag1).AddError(err)
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag2).AddError(err)
	}

	if pv1.Major != pv2.Major {
		if pv1.Major < pv2.Major {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Minor != pv2.Minor {
		if pv1.Minor < pv2.Minor {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Patch != pv2.Patch {
		if pv1.Patch < pv2.Patch {
			return -1, nil
		}
		return 1, nil
	}

	return 0, nil
}

// parseValid builds the result for a tag that IsSemver already accepted
func (p *SemVerParser) parseValid(tag string) *ParsedVersion {
	return &ParsedVersion{
		Original: tag,
		Format:   FormatSemVer,
		Major:    semverSegment(tag, 0),
		Minor:    semverSegment(tag, 1),
		Patch:    semverSegment(tag, 2),
	}
}

// CalVerYYYYMMDDParser implements VersionParser for YYYY.MM.DD CalVer format
type CalVerYYYYMMDDParser struct{}

// Parse parses a YYYY.MM.DD calendar version tag and returns a ParsedVersion struct with year, month, and day components.
func (p *CalVerYYYYMMDDParser) Parse(tag string) (*ParsedVersion, error) {
	if !p.IsValid(tag) {
		return nil, apperror.NewError("invalid YYYY.MM.DD CalVer format")
	}

	matches := calverYYYYMMDD.FindStringSubmatch(tag)
	if len(matches) < threePartMatchCount {
		return nil, apperror.NewError("parsing YYYY.MM.DD CalVer format failed")
	}

	nums, err := parseNumbers(matches[1:threePartMatchCount]...)
	if err != nil {
		return nil, apperror.NewError("parsing CalVer components failed").AddError(err)
	}
	year, month, day := nums[0], nums[1], nums[2]

	pv := &ParsedVersion{
		Original: tag,
		Format:   FormatCalVerYYYYMMDD,
		Year:     year,
		Month:    month,
		Day:      day,
	}

	return pv, nil
}

// IsValid checks if the given tag is a valid YYYY.MM.DD calendar version format.
func (p *CalVerYYYYMMDDParser) IsValid(tag string) bool {
	return IsCalVerYYYYMMDD(tag)
}

// Format returns the version format type handled by this parser.
func (p *CalVerYYYYMMDDParser) Format() Format {
	return FormatCalVerYYYYMMDD
}

// Compare compares two YYYY.MM.DD calendar version tags and returns -1, 0, or 1.
func (p *CalVerYYYYMMDDParser) Compare(tag1, tag2 string) (int, error) {
	pv1, err := p.Parse(tag1)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag1).AddError(err)
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag2).AddError(err)
	}

	if pv1.Year != pv2.Year {
		if pv1.Year < pv2.Year {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Month != pv2.Month {
		if pv1.Month < pv2.Month {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Day != pv2.Day {
		if pv1.Day < pv2.Day {
			return -1, nil
		}
		return 1, nil
	}

	return 0, nil
}

// CalVerYYMMMICROParser implements VersionParser for YY.MM.MICRO CalVer format
type CalVerYYMMMICROParser struct{}

// Parse parses a YY.MM.MICRO calendar version tag and returns a ParsedVersion struct with year, month, and micro components.
func (p *CalVerYYMMMICROParser) Parse(tag string) (*ParsedVersion, error) {
	if !p.IsValid(tag) {
		return nil, apperror.NewError("invalid YY.MM.MICRO CalVer format")
	}

	matches := calverYYMMMICRO.FindStringSubmatch(tag)
	if len(matches) < threePartMatchCount {
		return nil, apperror.NewError("parsing YY.MM.MICRO CalVer format failed")
	}

	nums, err := parseNumbers(matches[1:threePartMatchCount]...)
	if err != nil {
		return nil, apperror.NewError("parsing CalVer components failed").AddError(err)
	}
	year, month, micro := nums[0], nums[1], nums[2]

	pv := &ParsedVersion{
		Original: tag,
		Format:   FormatCalVerYYMMMICRO,
		Year:     year,
		Month:    month,
		Micro:    micro,
	}

	return pv, nil
}

// IsValid checks if the given tag is a valid YY.MM.MICRO calendar version format.
func (p *CalVerYYMMMICROParser) IsValid(tag string) bool {
	return IsCalVerYYMMMICRO(tag)
}

// Format returns the version format type handled by this parser.
func (p *CalVerYYMMMICROParser) Format() Format {
	return FormatCalVerYYMMMICRO
}

// Compare compares two YY.MM.MICRO calendar version tags and returns -1, 0, or 1.
func (p *CalVerYYMMMICROParser) Compare(tag1, tag2 string) (int, error) {
	pv1, err := p.Parse(tag1)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag1).AddError(err)
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag2).AddError(err)
	}

	if pv1.Year != pv2.Year {
		if pv1.Year < pv2.Year {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Month != pv2.Month {
		if pv1.Month < pv2.Month {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Micro != pv2.Micro {
		if pv1.Micro < pv2.Micro {
			return -1, nil
		}
		return 1, nil
	}

	return 0, nil
}

// CalVerYYYYWWParser implements VersionParser for YYYY.WW CalVer format
type CalVerYYYYWWParser struct{}

// Parse parses a YYYY.WW calendar version tag and returns a ParsedVersion struct with year and week components.
func (p *CalVerYYYYWWParser) Parse(tag string) (*ParsedVersion, error) {
	if !p.IsValid(tag) {
		return nil, apperror.NewError("invalid YYYY.WW CalVer format")
	}

	matches := calverYYYYWW.FindStringSubmatch(tag)
	if len(matches) < twoPartMatchCount {
		return nil, apperror.NewError("parsing YYYY.WW CalVer format failed")
	}

	nums, err := parseNumbers(matches[1:twoPartMatchCount]...)
	if err != nil {
		return nil, apperror.NewError("parsing CalVer components failed").AddError(err)
	}
	year, week := nums[0], nums[1]

	pv := &ParsedVersion{
		Original: tag,
		Format:   FormatCalVerYYYYWW,
		Year:     year,
		Week:     week,
	}

	return pv, nil
}

// IsValid checks if the given tag is a valid YYYY.WW calendar version format.
func (p *CalVerYYYYWWParser) IsValid(tag string) bool {
	return IsCalVerYYYYWW(tag)
}

// Format returns the version format type handled by this parser.
func (p *CalVerYYYYWWParser) Format() Format {
	return FormatCalVerYYYYWW
}

// Compare compares two YYYY.WW calendar version tags and returns -1, 0, or 1.
func (p *CalVerYYYYWWParser) Compare(tag1, tag2 string) (int, error) {
	pv1, err := p.Parse(tag1)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag1).AddError(err)
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag2).AddError(err)
	}

	if pv1.Year != pv2.Year {
		if pv1.Year < pv2.Year {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Week != pv2.Week {
		if pv1.Week < pv2.Week {
			return -1, nil
		}
		return 1, nil
	}

	return 0, nil
}

// CalVerYYYYMMDDMICROParser implements VersionParser for YYYY.MM.DD.MICRO CalVer format
type CalVerYYYYMMDDMICROParser struct{}

// Parse parses a YYYY.MM.DD.MICRO calendar version tag and returns a ParsedVersion struct with year, month, day, and micro components.
func (p *CalVerYYYYMMDDMICROParser) Parse(tag string) (*ParsedVersion, error) {
	if !p.IsValid(tag) {
		return nil, apperror.NewError("invalid YYYY.MM.DD.MICRO CalVer format")
	}

	matches := calverYYYYMMDDMICRO.FindStringSubmatch(tag)
	if len(matches) < fourPartMatchCount {
		return nil, apperror.NewError("parsing YYYY.MM.DD.MICRO CalVer format failed")
	}

	nums, err := parseNumbers(matches[1:fourPartMatchCount]...)
	if err != nil {
		return nil, apperror.NewError("parsing CalVer components failed").AddError(err)
	}
	year, month, day, micro := nums[0], nums[1], nums[2], nums[microIndex]

	pv := &ParsedVersion{
		Original: tag,
		Format:   FormatCalVerYYYYMMDDMICRO,
		Year:     year,
		Month:    month,
		Day:      day,
		Micro:    micro,
	}

	return pv, nil
}

// IsValid checks if the given tag is a valid YYYY.MM.DD.MICRO calendar version format.
func (p *CalVerYYYYMMDDMICROParser) IsValid(tag string) bool {
	return IsCalVerYYYYMMDDMICRO(tag)
}

// Format returns the version format type handled by this parser.
func (p *CalVerYYYYMMDDMICROParser) Format() Format {
	return FormatCalVerYYYYMMDDMICRO
}

// Compare compares two YYYY.MM.DD.MICRO calendar version tags and returns -1, 0, or 1.
func (p *CalVerYYYYMMDDMICROParser) Compare(tag1, tag2 string) (int, error) {
	pv1, err := p.Parse(tag1)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag1).AddError(err)
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, apperror.NewErrorf("parsing version %q failed", tag2).AddError(err)
	}

	if pv1.Year != pv2.Year {
		if pv1.Year < pv2.Year {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Month != pv2.Month {
		if pv1.Month < pv2.Month {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Day != pv2.Day {
		if pv1.Day < pv2.Day {
			return -1, nil
		}
		return 1, nil
	}

	if pv1.Micro != pv2.Micro {
		if pv1.Micro < pv2.Micro {
			return -1, nil
		}
		return 1, nil
	}

	return 0, nil
}

// Get returns the current version information of the application.
func Get() *Release {
	tag := GitTag
	info := parsedTag(tag)

	var parsedVersion *ParsedVersion
	if info.parsed != nil {
		// A copy, so a caller changing the result does not change what later calls see
		copied := *info.parsed
		parsedVersion = &copied
	}

	return &Release{
		GitTag:        tag,
		GitCommit:     GitCommit,
		GitShort:      GitShort,
		BuildDate:     BuildDate,
		GoVersion:     GoVersion,
		Platform:      Platform,
		Modules:       Modules,
		VersionFormat: info.format,
		ParsedVersion: parsedVersion,
	}
}

// Major returns the major version number from the Git tag if it follows semantic versioning.
func Major() int {
	if IsSemver(GitTag) {
		return ParseSemver(GitTag, 0)
	}

	// For CalVer formats, try to return year as major
	parser := GetParser(DetectFormat(GitTag))
	if parser != nil {
		pv, err := parser.Parse(GitTag)
		if err == nil {
			if pv.Year > 0 {
				return pv.Year
			}
			return pv.Major
		}
	}
	return 0
}

// Minor returns the minor version number from the Git tag if it follows semantic versioning.
func Minor() int {
	if IsSemver(GitTag) {
		return ParseSemver(GitTag, 1)
	}

	// For CalVer formats, try to return month as minor
	parser := GetParser(DetectFormat(GitTag))
	if parser != nil {
		pv, err := parser.Parse(GitTag)
		if err == nil {
			if pv.Month > 0 {
				return pv.Month
			}
			if pv.Week > 0 {
				return pv.Week
			}
			return pv.Minor
		}
	}
	return 0
}

// Patch returns the patch version number from the Git tag if it follows semantic versioning.
func Patch() int {
	if IsSemver(GitTag) {
		return ParseSemver(GitTag, patchSegmentIndex)
	}

	// For CalVer formats, try to return day as patch
	parser := GetParser(DetectFormat(GitTag))
	if parser != nil {
		pv, err := parser.Parse(GitTag)
		if err == nil {
			if pv.Day > 0 {
				return pv.Day
			}
			if pv.Micro > 0 {
				return pv.Micro
			}
			return pv.Patch
		}
	}
	return 0
}

// String returns the version tag as a string without the "v" prefix.
func String() string {
	version, _, _ := strings.Cut(GitTag, preReleaseSeparator)
	return strings.TrimPrefix(version, versionPrefix)
}

// IsSemver checks if the provided tag is a valid Git tag in semantic versioning format "vX.Y.Z".
func IsSemver(tag string) bool {
	return semver.MatchString(tag)
}

// ParseSemver parses the specified segment (major, minor, or patch) from the Git tag.
func ParseSemver(tag string, n int) int {
	if !IsSemver(tag) {
		logger.Error().Fields(logging.F("tag", tag)).Msg("invalid git tag format")
		return 0
	}

	version := strings.TrimPrefix(strings.SplitN(tag, preReleaseSeparator, splitPartsCount)[0], versionPrefix)
	segments := strings.Split(version, segmentSeparator)
	if n >= len(segments) {
		logger.Error().Fields(
			logging.F("index", n),
			logging.F("reason", "index out of range"),
		).Msg("parsing version segment failed")
		return 0
	}

	v, err := strconv.Atoi(segments[n])
	if err != nil {
		logger.Error().Err(err).Fields(
			logging.F("index", n),
		).Msg("parsing version segment failed")
		return 0
	}

	return v
}

// ExtractSemanticVersion parses the version number from the Git tag.
// It returns the version string without the "v" prefix and any pre-release/build metadata.
// If the tag is not a valid Git tag, it returns an empty string.
func ExtractSemanticVersion(tag string) string {
	if !IsSemver(tag) {
		logger.Error().Fields(logging.F("tag", tag)).Msg("invalid git tag format")
		return ""
	}

	return strings.TrimPrefix(strings.SplitN(tag, preReleaseSeparator, splitPartsCount)[0], versionPrefix)
}

// ParseVersion parses a version string and returns a ParsedVersion struct
func ParseVersion(tag string) (*ParsedVersion, error) {
	format := DetectFormat(tag)
	// DetectFormat has just matched the tag against the semver pattern; do not match it again
	if format == FormatSemVer {
		return (&SemVerParser{}).parseValid(tag), nil
	}
	parser := GetParser(format)
	if parser == nil {
		return nil, apperror.NewError("unsupported version format")
	}
	return parser.Parse(tag)
}

// IsValidVersion checks if a version string is valid in any supported format
func IsValidVersion(tag string) bool {
	format := DetectFormat(tag)
	return format != FormatUnknown
}

// CompareVersions compares two version strings, returns -1, 0, or 1
func CompareVersions(tag1, tag2 string) (int, error) {
	format1 := DetectFormat(tag1)
	format2 := DetectFormat(tag2)

	if format1 != format2 {
		return 0, apperror.NewError("cannot compare versions of different formats")
	}

	parser := GetParser(format1)
	if parser == nil {
		return 0, apperror.NewError("unsupported version format")
	}

	return parser.Compare(tag1, tag2)
}

// GetVersionComponents returns the version components as a map for any supported format
func GetVersionComponents(tag string) (map[string]any, error) {
	pv, err := ParseVersion(tag)
	if err != nil {
		return nil, apperror.NewErrorf("parsing version %q failed", tag).AddError(err)
	}

	components := make(map[string]any)
	components[componentFormat] = pv.Format.String()
	components[componentOriginal] = pv.Original

	if pv.Major > 0 {
		components[componentMajor] = pv.Major
	}
	if pv.Minor > 0 {
		components[componentMinor] = pv.Minor
	}
	if pv.Patch > 0 {
		components[componentPatch] = pv.Patch
	}
	if pv.Micro > 0 {
		components[componentMicro] = pv.Micro
	}
	if pv.Year > 0 {
		components[componentYear] = pv.Year
	}
	if pv.Month > 0 {
		components[componentMonth] = pv.Month
	}
	if pv.Day > 0 {
		components[componentDay] = pv.Day
	}
	if pv.Week > 0 {
		components[componentWeek] = pv.Week
	}

	return components, nil
}

// DetectFormat automatically detects the version format from a tag string
func DetectFormat(tag string) Format {
	// Every CalVer pattern starts with a two or four digit number, so most tags (all
	// semantic versions, for one) skip the four regular expressions
	if calverCandidate(tag) {
		if IsCalVerYYYYMMDDMICRO(tag) {
			return FormatCalVerYYYYMMDDMICRO
		}
		if IsCalVerYYYYMMDD(tag) {
			return FormatCalVerYYYYMMDD
		}
		if IsCalVerYYMMMICRO(tag) {
			return FormatCalVerYYMMMICRO
		}
		if IsCalVerYYYYWW(tag) {
			return FormatCalVerYYYYWW
		}
	}
	if IsSemver(tag) {
		return FormatSemVer
	}
	return FormatUnknown
}

// GetParser returns the appropriate parser for the given format
func GetParser(format Format) Parser {
	switch format {
	case FormatSemVer:
		return &SemVerParser{}
	case FormatCalVerYYYYMMDD:
		return &CalVerYYYYMMDDParser{}
	case FormatCalVerYYMMMICRO:
		return &CalVerYYMMMICROParser{}
	case FormatCalVerYYYYWW:
		return &CalVerYYYYWWParser{}
	case FormatCalVerYYYYMMDDMICRO:
		return &CalVerYYYYMMDDMICROParser{}
	default:
		return nil
	}
}

// IsCalVerYYYYMMDD checks if the tag is in YYYY.MM.DD CalVer format
func IsCalVerYYYYMMDD(tag string) bool {
	if !calverYYYYMMDD.MatchString(tag) {
		return false
	}
	matches := calverYYYYMMDD.FindStringSubmatch(tag)
	if len(matches) < threePartMatchCount {
		return false
	}

	nums, err := parseNumbers(matches[1:threePartMatchCount]...)
	if err != nil {
		return false
	}
	year, month, day := nums[0], nums[1], nums[2]

	return year > 0 && month >= 1 && month <= maxMonth && day >= 1 && day <= maxDay
}

// IsCalVerYYMMMICRO checks if the tag is in YY.MM.MICRO CalVer format
func IsCalVerYYMMMICRO(tag string) bool {
	if !calverYYMMMICRO.MatchString(tag) {
		return false
	}
	matches := calverYYMMMICRO.FindStringSubmatch(tag)
	if len(matches) < threePartMatchCount {
		return false
	}

	nums, err := parseNumbers(matches[1:threePartMatchCount]...)
	if err != nil {
		return false
	}
	year, month, micro := nums[0], nums[1], nums[2]

	return year >= 0 && year <= maxShortYear && month >= 1 && month <= maxMonth && micro >= 0
}

// IsCalVerYYYYWW checks if the tag is in YYYY.WW CalVer format
func IsCalVerYYYYWW(tag string) bool {
	if !calverYYYYWW.MatchString(tag) {
		return false
	}
	matches := calverYYYYWW.FindStringSubmatch(tag)
	if len(matches) < twoPartMatchCount {
		return false
	}

	nums, err := parseNumbers(matches[1:twoPartMatchCount]...)
	if err != nil {
		return false
	}
	year, week := nums[0], nums[1]

	return year > 0 && week >= 1 && week <= maxWeek
}

// IsCalVerYYYYMMDDMICRO checks if the tag is in YYYY.MM.DD.MICRO CalVer format
func IsCalVerYYYYMMDDMICRO(tag string) bool {
	if !calverYYYYMMDDMICRO.MatchString(tag) {
		return false
	}
	matches := calverYYYYMMDDMICRO.FindStringSubmatch(tag)
	if len(matches) < fourPartMatchCount {
		return false
	}

	nums, err := parseNumbers(matches[1:fourPartMatchCount]...)
	if err != nil {
		return false
	}
	year, month, day, micro := nums[0], nums[1], nums[2], nums[microIndex]

	return year > 0 && month >= 1 && month <= maxMonth && day >= 1 && day <= maxDay && micro >= 0
}

func init() {
	info, available := debug.ReadBuildInfo()
	if !available {
		return
	}

	for _, mod := range info.Deps {
		Modules = append(Modules, (&Module{}).fromBuildInfo(mod))
	}

	if GitCommit != unknownValue || BuildDate != unknownValue {
		return
	}

	if info.Main.Version != "" && info.Main.Version != develVersion {
		GitTag = info.Main.Version
	}

	for _, setting := range info.Settings {
		applyBuildSetting(setting)
	}
}

func applyBuildSetting(setting debug.BuildSetting) {
	switch setting.Key {
	case vcsRevisionKey:
		if setting.Value == "" {
			return
		}
		GitCommit = setting.Value
		GitShort = GitCommit
		if len(GitShort) >= shortCommitLength {
			GitShort = GitShort[:shortCommitLength]
		}
	case vcsTimeKey:
		BuildDate = setting.Value
	}
}

func parsedTag(tag string) *tagInfo {
	if info := lastTag.Load(); info != nil && info.tag == tag {
		return info
	}

	info := &tagInfo{tag: tag, format: DetectFormat(tag)}
	if parser := GetParser(info.format); parser != nil {
		pv, err := parser.Parse(tag)
		if err == nil {
			info.parsed = pv
		}
	}
	lastTag.Store(info)
	return info
}

// semverSegment returns segment n of a tag that IsSemver already accepted, without validating
// it again or splitting the whole tag. Missing or unparsable segments are 0.
func semverSegment(tag string, n int) int {
	tag = strings.TrimPrefix(tag, versionPrefix)
	if i := strings.IndexByte(tag, preReleaseSeparator[0]); i >= 0 {
		tag = tag[:i]
	}
	for ; n > 0; n-- {
		i := strings.IndexByte(tag, segmentSeparator[0])
		if i < 0 {
			return 0
		}
		tag = tag[i+1:]
	}
	if i := strings.IndexByte(tag, segmentSeparator[0]); i >= 0 {
		tag = tag[:i]
	}
	v, err := strconv.Atoi(tag)
	if err != nil {
		return 0
	}
	return v
}

// parseNumbers converts regex captures to integers and fails on values that overflow int.
func parseNumbers(parts ...string) ([]int, error) {
	nums := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, apperror.NewErrorf("parsing number %q failed", part).AddError(err)
		}
		nums[i] = n
	}
	return nums, nil
}

// calverCandidate reports whether the tag starts, after an optional "v", with a number of
// two or four digits followed by a dot, which every CalVer pattern requires.
func calverCandidate(tag string) bool {
	tag = strings.TrimPrefix(tag, versionPrefix)
	digits := 0
	for digits < len(tag) && tag[digits] >= '0' && tag[digits] <= '9' {
		digits++
	}
	return (digits == calverShortYearDigits || digits == calverLongYearDigits) && digits < len(tag) && tag[digits] == segmentSeparator[0]
}
