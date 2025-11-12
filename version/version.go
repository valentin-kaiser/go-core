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
//	VERSION_PACKAGE := github.com/valentin-kaiser/go-core/version
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

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/logging"
)

var (
	// GitTag is the release tag of the application, typically in the format "vX.Y.Z".
	GitTag = "v0.0.0"
	// GitCommit is the full commit hash of the application at build time.
	GitCommit = "unknown"
	// GitShort is the short commit hash of the application at build time.
	GitShort = "unknown"
	// BuildDate is the date and time when the application was built.
	BuildDate = "unknown"
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

func init() {
	info, available := debug.ReadBuildInfo()
	if !available {
		return
	}

	for _, mod := range info.Deps {
		Modules = append(Modules, (&Module{}).fromBuildInfo(mod))
	}

	if GitCommit != "unknown" || BuildDate != "unknown" {
		return
	}

	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		GitTag = info.Main.Version
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if setting.Value != "" {
				GitCommit = setting.Value
				GitShort = GitCommit

				if len(GitShort) >= 7 {
					GitShort = GitShort[:7]
				}
			}
		case "vcs.time":
			BuildDate = setting.Value
		}
	}
}

// Format represents the type of version format
type Format int

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

// String returns the string representation of the version format
func (f Format) String() string {
	switch f {
	case FormatSemVer:
		return "semantic"
	case FormatCalVerYYYYMMDD:
		return "calver-yyyy.mm.dd"
	case FormatCalVerYYMMMICRO:
		return "calver-yy.mm.micro"
	case FormatCalVerYYYYWW:
		return "calver-yyyy.ww"
	case FormatCalVerYYYYMMDDMICRO:
		return "calver-yyyy.mm.dd.micro"
	default:
		return "unknown"
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
	Original string                 `json:"original"`
	Format   Format                 `json:"format"`
	Major    int                    `json:"major,omitempty"`
	Minor    int                    `json:"minor,omitempty"`
	Patch    int                    `json:"patch,omitempty"`
	Micro    int                    `json:"micro,omitempty"`
	Year     int                    `json:"year,omitempty"`
	Month    int                    `json:"month,omitempty"`
	Day      int                    `json:"day,omitempty"`
	Week     int                    `json:"week,omitempty"`
	Extra    map[string]interface{} `json:"extra,omitempty"`
}

// String returns the version string without the "v" prefix
func (pv *ParsedVersion) String() string {
	version := strings.SplitN(pv.Original, "-", 2)[0]
	return strings.TrimPrefix(version, "v")
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

// Module represents a module dependency of the application.
// It includes the module path, version, checksum, and an optional replacement module.
type Module struct {
	Path    string
	Version string
	Sum     string
	Replace *Module `json:",omitempty"`
}

// Get returns the current version information of the application.
func Get() *Release {
	format := DetectFormat(GitTag)
	parser := GetParser(format)

	var parsedVersion *ParsedVersion
	if parser != nil {
		pv, err := parser.Parse(GitTag)
		if err == nil {
			parsedVersion = pv
		}
	}

	return &Release{
		GitTag:        GitTag,
		GitCommit:     GitCommit,
		GitShort:      GitShort,
		BuildDate:     BuildDate,
		GoVersion:     GoVersion,
		Platform:      Platform,
		Modules:       Modules,
		VersionFormat: format,
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
		return ParseSemver(GitTag, 2)
	}

	// For CalVer formats, try to return day as patch
	parser := GetParser(DetectFormat(GitTag))
	if parser != nil {
		if pv, err := parser.Parse(GitTag); err == nil {
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
	version := strings.SplitN(GitTag, "-", 2)[0]
	return strings.TrimPrefix(version, "v")
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

	version := strings.TrimPrefix(strings.SplitN(tag, "-", 2)[0], "v")
	segments := strings.Split(version, ".")
	if n >= len(segments) {
		logger.Error().Fields(
			logging.F("index", n),
			logging.F("error", "index out of range"),
		).Msg("error parsing version segment")
		return 0
	}

	v, err := strconv.Atoi(segments[n])
	if err != nil {
		logger.Error().Fields(
			logging.F("index", n),
			logging.F("error", err),
		).Msg("error parsing version segment")
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

	return strings.TrimPrefix(strings.SplitN(tag, "-", 2)[0], "v")
}

// ParseVersion parses a version string and returns a ParsedVersion struct
func ParseVersion(tag string) (*ParsedVersion, error) {
	format := DetectFormat(tag)
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

	// Only compare versions of the same format
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
func GetVersionComponents(tag string) (map[string]interface{}, error) {
	pv, err := ParseVersion(tag)
	if err != nil {
		return nil, err
	}

	components := make(map[string]interface{})
	components["format"] = pv.Format.String()
	components["original"] = pv.Original

	if pv.Major > 0 {
		components["major"] = pv.Major
	}
	if pv.Minor > 0 {
		components["minor"] = pv.Minor
	}
	if pv.Patch > 0 {
		components["patch"] = pv.Patch
	}
	if pv.Micro > 0 {
		components["micro"] = pv.Micro
	}
	if pv.Year > 0 {
		components["year"] = pv.Year
	}
	if pv.Month > 0 {
		components["month"] = pv.Month
	}
	if pv.Day > 0 {
		components["day"] = pv.Day
	}
	if pv.Week > 0 {
		components["week"] = pv.Week
	}

	return components, nil
}

// DetectFormat automatically detects the version format from a tag string
func DetectFormat(tag string) Format {
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
	if len(matches) < 4 {
		return false
	}

	year, _ := strconv.Atoi(matches[1])
	month, _ := strconv.Atoi(matches[2])
	day, _ := strconv.Atoi(matches[3])

	return year > 0 && month >= 1 && month <= 12 && day >= 1 && day <= 31
}

// IsCalVerYYMMMICRO checks if the tag is in YY.MM.MICRO CalVer format
func IsCalVerYYMMMICRO(tag string) bool {
	if !calverYYMMMICRO.MatchString(tag) {
		return false
	}
	matches := calverYYMMMICRO.FindStringSubmatch(tag)
	if len(matches) < 4 {
		return false
	}

	year, _ := strconv.Atoi(matches[1])
	month, _ := strconv.Atoi(matches[2])
	micro, _ := strconv.Atoi(matches[3])

	// Validate ranges - YY format with month and micro
	return year >= 0 && year <= 99 && month >= 1 && month <= 12 && micro >= 0
}

// IsCalVerYYYYWW checks if the tag is in YYYY.WW CalVer format
func IsCalVerYYYYWW(tag string) bool {
	if !calverYYYYWW.MatchString(tag) {
		return false
	}
	matches := calverYYYYWW.FindStringSubmatch(tag)
	if len(matches) < 3 {
		return false
	}

	year, _ := strconv.Atoi(matches[1])
	week, _ := strconv.Atoi(matches[2])

	// Validate ranges - year and week of year
	return year > 0 && week >= 1 && week <= 53
}

// IsCalVerYYYYMMDDMICRO checks if the tag is in YYYY.MM.DD.MICRO CalVer format
func IsCalVerYYYYMMDDMICRO(tag string) bool {
	if !calverYYYYMMDDMICRO.MatchString(tag) {
		return false
	}
	matches := calverYYYYMMDDMICRO.FindStringSubmatch(tag)
	if len(matches) < 5 {
		return false
	}

	year, _ := strconv.Atoi(matches[1])
	month, _ := strconv.Atoi(matches[2])
	day, _ := strconv.Atoi(matches[3])
	micro, _ := strconv.Atoi(matches[4])

	// Validate date ranges
	return year > 0 && month >= 1 && month <= 12 && day >= 1 && day <= 31 && micro >= 0
}

// SemVerParser implements VersionParser for semantic versioning
type SemVerParser struct{}

// Parse parses a semantic version tag and returns a ParsedVersion struct with major, minor, and patch components.
func (p *SemVerParser) Parse(tag string) (*ParsedVersion, error) {
	if !p.IsValid(tag) {
		return nil, apperror.NewError("invalid semantic version format")
	}

	pv := &ParsedVersion{
		Original: tag,
		Format:   FormatSemVer,
		Major:    ParseSemver(tag, 0),
		Minor:    ParseSemver(tag, 1),
		Patch:    ParseSemver(tag, 2),
	}

	return pv, nil
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
		return 0, err
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, err
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

// CalVerYYYYMMDDParser implements VersionParser for YYYY.MM.DD CalVer format
type CalVerYYYYMMDDParser struct{}

// Parse parses a YYYY.MM.DD calendar version tag and returns a ParsedVersion struct with year, month, and day components.
func (p *CalVerYYYYMMDDParser) Parse(tag string) (*ParsedVersion, error) {
	if !p.IsValid(tag) {
		return nil, apperror.NewError("invalid YYYY.MM.DD CalVer format")
	}

	matches := calverYYYYMMDD.FindStringSubmatch(tag)
	if len(matches) < 4 {
		return nil, apperror.NewError("failed to parse YYYY.MM.DD CalVer format")
	}

	year, _ := strconv.Atoi(matches[1])
	month, _ := strconv.Atoi(matches[2])
	day, _ := strconv.Atoi(matches[3])

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
		return 0, err
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, err
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
	if len(matches) < 4 {
		return nil, apperror.NewError("failed to parse YY.MM.MICRO CalVer format")
	}

	year, _ := strconv.Atoi(matches[1])
	month, _ := strconv.Atoi(matches[2])
	micro, _ := strconv.Atoi(matches[3])

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
		return 0, err
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, err
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
	if len(matches) < 3 {
		return nil, apperror.NewError("failed to parse YYYY.WW CalVer format")
	}

	year, _ := strconv.Atoi(matches[1])
	week, _ := strconv.Atoi(matches[2])

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
		return 0, err
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, err
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
	if len(matches) < 5 {
		return nil, apperror.NewError("failed to parse YYYY.MM.DD.MICRO CalVer format")
	}

	year, _ := strconv.Atoi(matches[1])
	month, _ := strconv.Atoi(matches[2])
	day, _ := strconv.Atoi(matches[3])
	micro, _ := strconv.Atoi(matches[4])

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
		return 0, err
	}
	pv2, err := p.Parse(tag2)
	if err != nil {
		return 0, err
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
func (v *Release) GetVersionComponents() (map[string]interface{}, error) {
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
