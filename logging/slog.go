package logging

import (
	"context"
	"log/slog"
	"strings"
)

// SLogger returns a slog logger backed by the current global logging adapter.
func SLogger() *slog.Logger {
	return NewSlogLogger(GetGlobalAdapterInterface())
}

// NewSlogLogger creates a slog logger backed by the provided logging adapter.
func NewSlogLogger(adapter Adapter) *slog.Logger {
	if adapter == nil {
		adapter = NewNoOpAdapter()
	}

	return slog.New(&slogHandler{adapter: adapter})
}

type slogHandler struct {
	adapter Adapter
	attrs   []slog.Attr
	groups  []string
}

func (h *slogHandler) Enabled(_ context.Context, level slog.Level) bool {
	if h.adapter == nil || !h.adapter.Enabled() {
		return false
	}

	return slogToLevel(level) >= h.adapter.GetLevel()
}

func (h *slogHandler) Handle(_ context.Context, record slog.Record) error {
	event := h.eventFor(record.Level)

	for _, attr := range h.attrs {
		appendSlogAttr(event, h.groups, attr)
	}

	record.Attrs(func(attr slog.Attr) bool {
		appendSlogAttr(event, h.groups, attr)
		return true
	})

	event.Msg(record.Message)
	return nil
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nextAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	nextAttrs = append(nextAttrs, h.attrs...)
	nextAttrs = append(nextAttrs, attrs...)

	return &slogHandler{
		adapter: h.adapter,
		attrs:   nextAttrs,
		groups:  append([]string(nil), h.groups...),
	}
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	nextGroups := make([]string, 0, len(h.groups)+1)
	nextGroups = append(nextGroups, h.groups...)
	nextGroups = append(nextGroups, name)

	return &slogHandler{
		adapter: h.adapter,
		attrs:   append([]slog.Attr(nil), h.attrs...),
		groups:  nextGroups,
	}
}

func (h *slogHandler) eventFor(level slog.Level) Event {
	if level <= slog.LevelDebug {
		if level < slog.LevelDebug {
			return h.adapter.Trace()
		}
		return h.adapter.Debug()
	}

	if level < slog.LevelWarn {
		return h.adapter.Info()
	}

	if level < slog.LevelError {
		return h.adapter.Warn()
	}

	return h.adapter.Error()
}

func slogToLevel(level slog.Level) Level {
	if level <= slog.LevelDebug {
		if level < slog.LevelDebug {
			return TraceLevel
		}
		return DebugLevel
	}

	if level < slog.LevelWarn {
		return InfoLevel
	}

	if level < slog.LevelError {
		return WarnLevel
	}

	return ErrorLevel
}

func appendSlogAttr(event Event, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}

	if attr.Value.Kind() == slog.KindGroup {
		nextGroups := append(append([]string(nil), groups...), attr.Key)
		for _, nested := range attr.Value.Group() {
			appendSlogAttr(event, nextGroups, nested)
		}
		return
	}

	key := attr.Key
	if len(groups) > 0 {
		prefix := strings.Join(groups, ".")
		if key == "" {
			key = prefix
		} else {
			key = prefix + "." + key
		}
	}

	if key == "" {
		return
	}

	event.Field(key, attr.Value.Any())
}
