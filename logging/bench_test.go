package logging_test

import (
	"io"
	"log"
	"testing"

	"github.com/rs/zerolog"
	"github.com/valentin-kaiser/go-core/v2/logging"
)

const (
	streamBufferSize   = 100
	listenerBufferSize = 1024
)

func BenchmarkStreamWriterWrite(b *testing.B) {
	sw := logging.NewStreamWriter(streamBufferSize)
	line := []byte(`{"level":"info","time":"2026-01-01T00:00:00Z","message":"hello world"}` + "\n")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := sw.Write(line)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStreamWriterWriteParallel(b *testing.B) {
	sw := logging.NewStreamWriter(streamBufferSize)
	ch := make(chan string, listenerBufferSize)
	sw.AddListener(ch)
	go func() {
		for range ch {
		}
	}()
	b.Cleanup(func() { sw.RemoveListener(ch) })
	line := []byte(`{"level":"info","time":"2026-01-01T00:00:00Z","message":"hello world"}` + "\n")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := sw.Write(line)
			if err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkStandardDisabledMsgf(b *testing.B) {
	a := logging.NewStandardAdapterWithLogger(log.New(io.Discard, "", 0)).SetLevel(logging.InfoLevel)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.Trace().Msgf("entries=%d name=%s", i, "x")
	}
}

func BenchmarkStandardEnabledFields(b *testing.B) {
	a := logging.NewStandardAdapterWithLogger(log.New(io.Discard, "", 0)).SetLevel(logging.InfoLevel)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.Info().Field("a", i).Field("b", "x").Msg("hello")
	}
}

func BenchmarkZerologEnabledNoFields(b *testing.B) {
	a := logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.Info().Msg("hello")
	}
}

func BenchmarkZerologEnabledFields(b *testing.B) {
	a := logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.Info().Field("a", i).Field("b", "x").Field("c", 1.5).Msg("hello")
	}
}

func BenchmarkZerologEnabledFieldsParallel(b *testing.B) {
	a := logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			a.Info().Field("a", 1).Field("b", "x").Msg("hello")
		}
	})
}

func BenchmarkZerologDisabledFields(b *testing.B) {
	a := logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.Trace().Field("a", i).Field("b", "x").Msg("hello")
	}
}

func BenchmarkZerologWithPackage(b *testing.B) {
	a := logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(logging.InfoLevel)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if a.WithPackage("bench") == nil {
			b.Fatal("WithPackage returned nil")
		}
	}
}
