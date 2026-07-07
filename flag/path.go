package flag

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func DataPath(service bool, appName string) string {
	if appName == "" {
		execName := executableName()
		if execName == "" {
			execName = "app"
		}
		appName = execName
	}

	return dataPathFor(appName, service)
}

func dataPathFor(appName string, service bool) string {
	path := filepath.Join(dataRoot(service), appName)
	if absPath, err := filepath.Abs(path); err == nil {
		return absPath
	}

	return filepath.Clean(path)
}

func dataRoot(service bool) string {
	switch runtime.GOOS {
	case "windows":
		if service {
			if programData := os.Getenv("PROGRAMDATA"); programData != "" {
				return programData
			}

			return `C:\ProgramData`
		}

		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return localAppData
		}

		if appData := os.Getenv("APPDATA"); appData != "" {
			return appData
		}

		if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
			return filepath.Join(userProfile, "AppData", "Local")
		}

		return `C:\Users\Public\AppData\Local`
	case "linux":
		if service {
			return "/var/lib"
		}

		if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
			return xdgDataHome
		}

		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, ".local", "share")
		}

		return ".local/share"
	case "darwin":
		if service {
			return "/Library/Application Support"
		}

		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, "Library", "Application Support")
		}

		return "Library/Application Support"
	default:
		if service {
			return string(os.PathSeparator) + "var" + string(os.PathSeparator) + "lib"
		}

		return "."
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
