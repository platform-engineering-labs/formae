// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"log/slog"
)

// Logger provides structured logging for plugin authors.
// Automatically includes plugin metadata (namespace, resource type, operation).
type Logger interface {
	Debug(msg string, attrs ...any)
	Info(msg string, attrs ...any)
	Warn(msg string, attrs ...any)
	Error(msg string, attrs ...any)
	With(attrs ...any) Logger
}

type loggerKey struct{}

// LoggerFromContext extracts the logger from context.
// Returns a no-op logger if none is present.
func LoggerFromContext(ctx context.Context) Logger {
	if l, ok := ctx.Value(loggerKey{}).(Logger); ok {
		return l
	}
	return noopLogger{}
}

// WithLogger returns a new context with the given logger.
func WithLogger(ctx context.Context, l Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// pluginLogger wraps slog.Logger with persistent attributes.
type pluginLogger struct {
	slog  *slog.Logger
	attrs []any
}

// NewPluginLogger creates a new Logger wrapping the given slog.Logger.
func NewPluginLogger(logger *slog.Logger) Logger {
	return &pluginLogger{slog: logger, attrs: nil}
}

func (l *pluginLogger) Debug(msg string, attrs ...any) {
	l.slog.Debug(msg, append(l.attrs, attrs...)...)
}

func (l *pluginLogger) Info(msg string, attrs ...any) {
	l.slog.Info(msg, append(l.attrs, attrs...)...)
}

func (l *pluginLogger) Warn(msg string, attrs ...any) {
	l.slog.Warn(msg, append(l.attrs, attrs...)...)
}

func (l *pluginLogger) Error(msg string, attrs ...any) {
	l.slog.Error(msg, append(l.attrs, attrs...)...)
}

func (l *pluginLogger) With(attrs ...any) Logger {
	return &pluginLogger{slog: l.slog, attrs: append(l.attrs, attrs...)}
}

// noopLogger does nothing - used when no logger in context.
type noopLogger struct{}

func (noopLogger) Debug(msg string, attrs ...any) {}
func (noopLogger) Info(msg string, attrs ...any)  {}
func (noopLogger) Warn(msg string, attrs ...any)  {}
func (noopLogger) Error(msg string, attrs ...any) {}
func (noopLogger) With(attrs ...any) Logger       { return noopLogger{} }
