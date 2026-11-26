package logging_test

import (
	"bytes"
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/valentin-kaiser/go-core/logging"
)

func TestNewSlogLogger_MapsLevelsAndFields(t *testing.T) {
	var out bytes.Buffer
	adapter := logging.NewStandardAdapterWithLogger(log.New(&out, "", 0)).SetLevel(logging.DebugLevel)

	logger := logging.NewSlogLogger(adapter)
	logger.Info("hello", "user", "alice", "attempt", 2)

	line := out.String()
	if !strings.Contains(line, "[INFO]") {
		t.Fatalf("expected info level prefix in %q", line)
	}
	if !strings.Contains(line, "hello") {
		t.Fatalf("expected message in %q", line)
	}
	if !strings.Contains(line, "user=alice") {
		t.Fatalf("expected user field in %q", line)
	}
	if !strings.Contains(line, "attempt=2") {
		t.Fatalf("expected attempt field in %q", line)
	}
}

func TestNewSlogLogger_RespectsAdapterLevel(t *testing.T) {
	var out bytes.Buffer
	adapter := logging.NewStandardAdapterWithLogger(log.New(&out, "", 0)).SetLevel(logging.WarnLevel)

	logger := logging.NewSlogLogger(adapter)
	logger.Info("hidden")
	logger.Warn("visible")

	line := out.String()
	if strings.Contains(line, "hidden") {
		t.Fatalf("did not expect info message in %q", line)
	}
	if !strings.Contains(line, "visible") {
		t.Fatalf("expected warn message in %q", line)
	}
}

func TestNewSlogLogger_WithAttrsAndGroups(t *testing.T) {
	var out bytes.Buffer
	adapter := logging.NewStandardAdapterWithLogger(log.New(&out, "", 0))

	logger := logging.NewSlogLogger(adapter).With("service", "api").WithGroup("http")
	logger.Info("request", slog.String("method", "GET"))

	line := out.String()
	if !strings.Contains(line, "service=api") {
		t.Fatalf("expected bound attr in %q", line)
	}
	if !strings.Contains(line, "http.method=GET") {
		t.Fatalf("expected grouped attr in %q", line)
	}
}
