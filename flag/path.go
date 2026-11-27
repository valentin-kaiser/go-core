package flag

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	defaultAppName = "app"

	osWindows = "windows"
	osLinux   = "linux"
	osDarwin  = "darwin"

	envProgramData  = "PROGRAMDATA"
	envLocalAppData = "LOCALAPPDATA"
	envAppData      = "APPDATA"
	envUserProfile  = "USERPROFILE"
	envXDGDataHome  = "XDG_DATA_HOME"

	windowsAppDataDir          = "AppData"
	windowsLocalDir            = "Local"
	windowsProgramDataFallback = `C:\ProgramData`
	windowsPublicFallback      = `C:\Users\Public\AppData\Local`

	linuxServiceRoot      = "/var/lib"
	linuxLocalDir         = ".local"
	linuxShareDir         = "share"
	linuxRelativeFallback = ".local/share"

	darwinServiceRoot      = "/Library/Application Support"
	darwinLibraryDir       = "Library"
	darwinSupportDir       = "Application Support"
	darwinRelativeFallback = "Library/Application Support"

	unixVarDir = "var"
	unixLibDir = "lib"
	currentDir = "."
)

// DataPath returns the directory for the data of an application. A service gets a system wide
// location, an interactive application one in the profile of the user. An empty appName is
// replaced by the name of the executable.
func DataPath(service bool, appName string) string {
	if appName == "" {
		execName := executableName()
		if execName == "" {
			execName = defaultAppName
		}
		appName = execName
	}

	return dataPathFor(appName, service)
}

func dataPathFor(appName string, service bool) string {
	path := filepath.Join(dataRoot(service), appName)
	absPath, err := filepath.Abs(path)
	if err == nil {
		return absPath
	}

	return filepath.Clean(path)
}

func dataRoot(service bool) string {
	switch runtime.GOOS {
	case osWindows:
		if service {
			if programData := os.Getenv(envProgramData); programData != "" {
				return programData
			}

			return windowsProgramDataFallback
		}

		if localAppData := os.Getenv(envLocalAppData); localAppData != "" {
			return localAppData
		}

		if appData := os.Getenv(envAppData); appData != "" {
			return appData
		}

		if userProfile := os.Getenv(envUserProfile); userProfile != "" {
			return filepath.Join(userProfile, windowsAppDataDir, windowsLocalDir)
		}

		return windowsPublicFallback
	case osLinux:
		if service {
			return linuxServiceRoot
		}

		if xdgDataHome := os.Getenv(envXDGDataHome); xdgDataHome != "" {
			return xdgDataHome
		}

		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return filepath.Join(home, linuxLocalDir, linuxShareDir)
		}

		return linuxRelativeFallback
	case osDarwin:
		if service {
			return darwinServiceRoot
		}

		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return filepath.Join(home, darwinLibraryDir, darwinSupportDir)
		}

		return darwinRelativeFallback
	default:
		if service {
			return string(os.PathSeparator) + unixVarDir + string(os.PathSeparator) + unixLibDir
		}

		return currentDir
	}
}

func executableName() string {
	executable, err := os.Executable()
	if err != nil || executable == "" {
		return ""
	}

	baseName := filepath.Base(executable)
	return strings.TrimSuffix(baseName, filepath.Ext(baseName))
}
