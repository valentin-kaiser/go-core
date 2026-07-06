package flag

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDataPathForInteractiveRoot(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\Test\AppData\Local`)
	t.Setenv("APPDATA", `C:\Users\Test\AppData\Roaming`)
	t.Setenv("PROGRAMDATA", `C:\ProgramData`)
	t.Setenv("USERPROFILE", `C:\Users\Test`)
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")

	got := dataPathFor("SampleApp", false)
	if got == "" {
		t.Fatal("expected a default interactive data path")
	}

	if runtime.GOOS == "windows" {
		if !strings.Contains(strings.ToLower(got), strings.ToLower("SampleApp")) {
			t.Fatalf("expected interactive data path to include the app name, got %q", got)
		}

		if !strings.HasPrefix(strings.ToLower(got), strings.ToLower(`C:\Users\Test\AppData\Local`)) {
			t.Fatalf("expected interactive data path to use local app data, got %q", got)
		}
		return
	}

	if runtime.GOOS == "linux" {
		if !strings.HasPrefix(got, filepath.Clean("/tmp/xdg-data")) {
			t.Fatalf("expected interactive data path to use XDG data home, got %q", got)
		}
	}
}

func TestDataPathForServiceRoot(t *testing.T) {
	t.Setenv("PROGRAMDATA", `C:\ProgramData`)

	got := dataPathFor("SampleService", true)
	if got == "" {
		t.Fatal("expected a default service data path")
	}

	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(strings.ToLower(got), strings.ToLower(`C:\ProgramData`)) {
			t.Fatalf("expected service data path to use ProgramData, got %q", got)
		}
	}

	if runtime.GOOS == "linux" {
		if !strings.HasPrefix(got, "/var/lib") {
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
