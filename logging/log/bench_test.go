package log_test

import (
	"io"
	"net"
	"testing"

	"github.com/rs/zerolog"
	"github.com/valentin-kaiser/go-core/logging"
	"github.com/valentin-kaiser/go-core/logging/log"
)

// Cost of a log call whose level is disabled. This is the common case on a hot
// path: a Trace()/Debug() line in production. The question each benchmark
// answers is what the call site pays although nothing is written.

func setupDisabled(b *testing.B, debug bool) {
	b.Helper()
	prev := logging.GetGlobalAdapterInterface()
	logging.SetGlobalAdapter(logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel))
	logging.Debug(debug)
	logging.Anonymous(true)
	b.Cleanup(func() {
		logging.Debug(false)
		logging.Anonymous(false)
		logging.SetGlobalAdapter(prev)
	})
}

var sinkMAC = net.HardwareAddr{0x02, 0xb1, 0x00, 0x00, 0x00, 0x01}

func BenchmarkDisabledTraceMsg(b *testing.B) {
	setupDisabled(b, false)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		log.Trace().Msg("scan persisted")
	}
}

// Msgf with arguments: the arguments are boxed and sinkMAC.String() is
// evaluated before the level is looked at, like intake.go does per scan.
func BenchmarkDisabledTraceMsgfArgs(b *testing.B) {
	setupDisabled(b, false)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		log.Trace().Msgf("scan gateway=%s entries=%d batch=%d", sinkMAC.String(), i, uint64(i))
	}
}

// Same call with the caller tracking that `--debug` switches on.
func BenchmarkDisabledTraceMsgDebugOn(b *testing.B) {
	setupDisabled(b, true)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		log.Trace().Msg("scan persisted")
	}
}

func BenchmarkDisabledTraceMsgParallel(b *testing.B) {
	setupDisabled(b, false)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			log.Trace().Msg("scan persisted")
		}
	})
}

// The package logger is what the database middleware uses
// (GetPackageLogger("database.bleep")): a DynamicAdapter that resolves the
// adapter on every call.
func BenchmarkDisabledPackageLoggerTrace(b *testing.B) {
	setupDisabled(b, false)
	l := logging.GetPackageLogger("database.bleep")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Trace().Msg("query")
	}
}

func BenchmarkDisabledPackageLoggerTraceParallel(b *testing.B) {
	setupDisabled(b, false)
	l := logging.GetPackageLogger("database.bleep")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Trace().Msg("query")
		}
	})
}

// Guarded variant: what a call site would cost if it asked first.
func BenchmarkDisabledGuarded(b *testing.B) {
	setupDisabled(b, false)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if log.GetLevel() <= logging.TraceLevel {
			log.Trace().Msgf("scan gateway=%s entries=%d", sinkMAC.String(), i)
		}
	}
}

// An enabled Info line to io.Discard, the cost of a line that is really written.
func BenchmarkEnabledInfoMsgf(b *testing.B) {
	setupDisabled(b, false)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		log.Info().Msgf("scan gateway=%s entries=%d", "02:b1:00:00:00:01", i)
	}
}
