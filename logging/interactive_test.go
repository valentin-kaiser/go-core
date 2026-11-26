package logging_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/logging"
)

// stdout redirected to a regular file (command > app.log) still counts as interactive
func TestInteractiveRedirectedToFile(t *testing.T) {
	logging.SetModeDetector(nil)

	f, err := os.Create(filepath.Join(t.TempDir(), "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer apperror.Catch(f.Close, "closing log file failed")

	old := os.Stdout
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = old })

	if !logging.Interactive() {
		t.Fatal("stdout redirected to a regular file is not interactive")
	}
}
