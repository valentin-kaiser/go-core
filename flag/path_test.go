package flag

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	localAppData = `C:\Users\Test\AppData\Local`
	roamingData  = `C:\Users\Test\AppData\Roaming`
	programData  = `C:\ProgramData`
	userProfile  = `C:\Users\Test`
	xdgDataHome  = "/tmp/xdg-data"
	serviceLib   = "/var/lib"
)

func TestAppDataPath(t *testing.T) {
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("APPDATA", roamingData)
	t.Setenv("PROGRAMDATA", programData)
	t.Setenv("USERPROFILE", userProfile)
	t.Setenv("XDG_DATA_HOME", xdgDataHome)

	got := AppDataPath("SampleApp")
	if got == "" {
		t.Fatal("expected a default interactive data path")
	}

	if runtime.GOOS == "windows" {
		if !strings.Contains(strings.ToLower(got), strings.ToLower("SampleApp")) {
			t.Fatalf("expected interactive data path to include the app name, got %q", got)
		}

		if !strings.HasPrefix(strings.ToLower(got), strings.ToLower(localAppData)) {
			t.Fatalf("expected interactive data path to use local app data, got %q", got)
		}
		return
	}

	if runtime.GOOS == "linux" {
		if !strings.HasPrefix(got, filepath.Clean(xdgDataHome)) {
			t.Fatalf("expected interactive data path to use XDG data home, got %q", got)
		}
	}
}

func TestServiceAppDataPath(t *testing.T) {
	t.Setenv("PROGRAMDATA", programData)

	got := ServiceAppDataPath("SampleService")
	if got == "" {
		t.Fatal("expected a default service data path")
	}

	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(strings.ToLower(got), strings.ToLower(programData)) {
			t.Fatalf("expected service data path to use ProgramData, got %q", got)
		}
	}

	if runtime.GOOS == "linux" {
		if !strings.HasPrefix(got, serviceLib) {
			t.Fatalf("expected service data path to use /var/lib, got %q", got)
		}
	}
}

func TestExecutableNameFallback(t *testing.T) {
	if executableName() == "" {
		t.Fatal("expected executable name to be derived")
	}

	if os.PathSeparator == 0 {
		t.Fatal("unexpected path separator")
	}
}
