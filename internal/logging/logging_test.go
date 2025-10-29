// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logging

import (
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

type TestWriter struct {
	Entries []string
}

func NewTestWriter() *TestWriter {
	return &TestWriter{
		Entries: make([]string, 0),
	}
}

func (w *TestWriter) Write(p []byte) (n int, err error) {
	w.Entries = append(w.Entries, string(p))
	return len(p), nil
}

func (w *TestWriter) Contains(substr string) bool {
	for _, entry := range w.Entries {
		if strings.Contains(entry, substr) {
			return true
		}
	}

	return false
}

func TestLogging_DirectSlogInfo(t *testing.T) {
	writer := NewTestWriter()
	slog.SetDefault(slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})))

	slog.Info("test info")

	if !writer.Contains("test info") {
		t.Error("expected 'test info' in log entries")
	}
}

func TestLogging_LogProxyInfo(t *testing.T) {
	writer := NewTestWriter()
	slog.SetDefault(slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})))
	lw := &slogWriter{}
	log.SetOutput(lw)

	log.Print("ERROR: test info")

	if !writer.Contains("test info") {
		t.Error("expected 'test info' in log entries")
	}
}

func TestLogging_EchoInfo(t *testing.T) {
	writer := NewTestWriter()
	slog.SetDefault(slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})))
	e := echo.New()
	e.HideBanner = true
	e.Logger = NewEchoLogger()

	e.Logger.Info("test info")

	if !writer.Contains("test info") {
		t.Error("expected 'test info' in log entries")
	}
}
