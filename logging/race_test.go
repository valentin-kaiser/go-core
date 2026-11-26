package logging_test

import (
	"bytes"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/valentin-kaiser/go-core/v2/logging"
	logpkg "github.com/valentin-kaiser/go-core/v2/logging/log"
)

const (
	concurrentIterations = 1000
	packageName          = "p"
	racePackageName      = "racepkg"
)

// The debug flag is toggled while another goroutine logs.
func TestConcurrentDebugFlag(t *testing.T) {
	a := logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel)
	t.Cleanup(func() { logging.DisableDebug() })

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range concurrentIterations {
			if i%2 == 0 {
				logging.EnableDebug()
			} else {
				logging.DisableDebug()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range concurrentIterations {
			a.Trace().Msg("x")
			a.Info().Msg("x")
		}
	}()
	wg.Wait()
}

// The level is changed while another goroutine logs and derives package adapters.
func TestConcurrentSetLevel(_ *testing.T) {
	a := logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel)
	s := logging.NewStandardAdapter().SetLevel(logging.InfoLevel)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for range concurrentIterations {
			a.SetLevel(logging.DebugLevel)
			s.SetLevel(logging.ErrorLevel)
		}
	}()
	go func() {
		defer wg.Done()
		for range concurrentIterations {
			a.Info().Msg("x")
			a.WithPackage(packageName).Level()
		}
	}()
	go func() {
		defer wg.Done()
		for range concurrentIterations {
			s.Level()
			s.Enabled()
		}
	}()
	wg.Wait()
}

// A level change on the parent has to reach the cached package adapter.
func TestWithPackageFollowsParentLevel(t *testing.T) {
	a := logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel)
	if got := a.WithPackage(packageName).Level(); got != logging.InfoLevel {
		t.Fatalf("level = %v, want info", got)
	}
	a.SetLevel(logging.ErrorLevel)
	if got := a.WithPackage(packageName).Level(); got != logging.ErrorLevel {
		t.Fatalf("package level = %v after parent change, want error", got)
	}
	if a.WithPackage(packageName) != a.WithPackage(packageName) {
		t.Fatal("package adapter is not cached")
	}
}

// Global adapter and package overrides set at runtime must be picked up by existing package loggers.
func TestPackageLoggerFollowsRegistry(t *testing.T) {
	prev := logging.CurrentAdapter()
	t.Cleanup(func() { logging.SetGlobalAdapter(prev); logging.EnablePackage(racePackageName) })

	pl := logging.PackageLogger(racePackageName)
	logging.SetGlobalAdapter(logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel))
	if got := pl.Level(); got != logging.InfoLevel {
		t.Fatalf("level = %v, want info", got)
	}
	logging.SetGlobalAdapter(logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.ErrorLevel))
	if got := pl.Level(); got != logging.ErrorLevel {
		t.Fatalf("level = %v after global swap, want error", got)
	}
	logging.SetPackageLevel(racePackageName, logging.WarnLevel)
	if got := pl.Level(); got != logging.WarnLevel {
		t.Fatalf("level = %v after package override, want warn", got)
	}
	logging.EnablePackage(racePackageName)
	if got := pl.Level(); got != logging.ErrorLevel {
		t.Fatalf("level = %v after removing override, want error", got)
	}
}

// With caller tracking on, the field has to point at the code that logs, not into the logging package.
func TestCallerFieldPointsAtCaller(t *testing.T) {
	var buf bytes.Buffer
	zl := logging.NewZerologAdapterWithLogger(zerolog.New(&buf)).SetLevel(logging.InfoLevel)
	st := logging.NewStandardAdapterWithLogger(log.New(&buf, "", 0)).SetLevel(logging.InfoLevel)
	logging.EnableDebug()
	t.Cleanup(func() { logging.DisableDebug() })

	// The depth is calibrated for the log facade
	prev := logging.CurrentAdapter()
	t.Cleanup(func() { logging.SetGlobalAdapter(prev) })
	logging.SetGlobalAdapter(zl)
	logpkg.Info().Msg("zerolog")
	logging.SetGlobalAdapter(st)
	logpkg.Info().Msg("standard")
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if !strings.Contains(line, "race_test.go") {
			t.Errorf("caller does not point at the test file: %s", line)
		}
	}
}

// The typed setters must produce the same JSON as the generic Interface path they replace.
func TestFieldOutputMatchesInterface(t *testing.T) {
	values := []any{
		"text with \"quotes\"", 1, int8(-2), int16(3), int32(-4), int64(5),
		uint(6), uint8(7), uint16(8), uint32(9), uint64(10),
		float32(1.5), 2.25, true, time.Second, time.Unix(1700000000, 123456789).UTC(), []byte("ab"),
		[]string{"a"}, map[string]int{"k": 1}, nil,
	}
	for _, v := range values {
		var typed, generic bytes.Buffer
		logging.NewZerologAdapterWithLogger(zerolog.New(&typed)).Info().Field("k", v).Msg("m")
		gl := zerolog.New(&generic)
		gl.Info().Interface("k", v).Msg("m")
		if typed.String() != generic.String() {
			t.Errorf("%T: typed %q, generic %q", v, typed.String(), generic.String())
		}
	}
}

// The standard adapter's line format must stay as it was while avoiding fmt for common values.
func TestStandardAdapterLineFormat(t *testing.T) {
	var buf bytes.Buffer
	a := logging.NewStandardAdapterWithLogger(log.New(&buf, "", 0)).SetLevel(logging.InfoLevel).WithPackage(packageName)
	err := errors.New("boom")
	a.Warn().Field("s", "text").Field("i", 42).Field("u", uint64(7)).Field("b", true).Field("f", 1.5).
		Field("st", time.Second).Field("sl", []int{1, 2}).Err(err).Msg("hello")
	want := "[WARN] hello pkg=p s=text i=42 u=7 b=true f=1.5 st=1s sl=[1 2] error=boom\n"
	if buf.String() != want {
		t.Fatalf("got  %q\nwant %q", buf.String(), want)
	}
}
