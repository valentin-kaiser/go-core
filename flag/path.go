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

// AppDataPath returns the directory for the data of an interactive application, located in the
// profile of the user. An empty appName is replaced by the name of the executable.
func AppDataPath(appName string) string {
	return absoluteDataPath(userDataRoot(), resolveAppName(appName))
}

// ServiceAppDataPath returns the machine-wide directory for the data of a service.
// An empty appName is replaced by the name of the executable.
func ServiceAppDataPath(appName string) string {
	return absoluteDataPath(serviceDataRoot(), resolveAppName(appName))
}

func resolveAppName(appName string) string {
	if appName != "" {
		return appName
	}

	execName := executableName()
	if execName == "" {
		return defaultAppName
	}

	return execName
}

func absoluteDataPath(root, appName string) string {
	path := filepath.Join(root, appName)
	absPath, err := filepath.Abs(path)
	if err == nil {
		return absPath
	}

	return filepath.Clean(path)
}

func userDataRoot() string {
	switch runtime.GOOS {
	case osWindows:
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
		if xdgDataHome := os.Getenv(envXDGDataHome); xdgDataHome != "" {
			return xdgDataHome
		}

		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return filepath.Join(home, linuxLocalDir, linuxShareDir)
		}

		return linuxRelativeFallback
	case osDarwin:
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return filepath.Join(home, darwinLibraryDir, darwinSupportDir)
		}

		return darwinRelativeFallback
	default:
		return currentDir
	}
}

func serviceDataRoot() string {
	switch runtime.GOOS {
	case osWindows:
		if programData := os.Getenv(envProgramData); programData != "" {
			return programData
		}

		return windowsProgramDataFallback
	case osLinux:
		return linuxServiceRoot
	case osDarwin:
		return darwinServiceRoot
	default:
		return string(os.PathSeparator) + unixVarDir + string(os.PathSeparator) + unixLibDir
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
