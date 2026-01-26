// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"log/slog"
	"testing"
)

func TestLoggerFromContext_WithLogger(t *testing.T) {
	logger := NewPluginLogger(slog.Default())
	ctx := WithLogger(context.Background(), logger)

	got := LoggerFromContext(ctx)
	if _, ok := got.(*pluginLogger); !ok {
		t.Errorf("expected *pluginLogger, got %T", got)
	}
}

func TestLoggerFromContext_WithoutLogger(t *testing.T) {
	ctx := context.Background()
	got := LoggerFromContext(ctx)
	if _, ok := got.(noopLogger); !ok {
		t.Errorf("expected noopLogger, got %T", got)
	}
}

func TestPluginLogger_With(t *testing.T) {
	logger := NewPluginLogger(slog.Default())
	enriched := logger.With("key1", "value1")

	pl, ok := enriched.(*pluginLogger)
	if !ok {
		t.Fatalf("expected *pluginLogger, got %T", enriched)
	}
	if len(pl.attrs) != 2 {
		t.Errorf("expected 2 attrs, got %d", len(pl.attrs))
	}
}

func TestNoopLogger_DoesNotPanic(t *testing.T) {
	logger := noopLogger{}
	logger.Debug("test")
	logger.Info("test", "key", "value")
	logger.Warn("test")
	logger.Error("test", "error", "some error")
	_ = logger.With("key", "value")
}
