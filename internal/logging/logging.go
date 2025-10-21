// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logging

import (
	"context"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/lmittmann/tint"
	"github.com/platform-engineering-labs/formae/internal/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"gopkg.in/natefinch/lumberjack.v2"
)

const NoLoggingLevel = slog.Level(100) // A level higher than any standard level to disable logging

func SetupInitialLogging() {
	w := os.Stdout
	slog.SetDefault(slog.New(
		tint.NewHandler(w, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.RFC3339,
		}),
	))

	//overwrite standard log so it's always redirected to slog, in case some deep dep is using it
	lw := &slogWriter{}
	log.Default().SetOutput(lw)
	log.SetOutput(lw)
}

func SetupClientLogging(logFilePath string) {
	if err := util.EnsureFileFolderHierarchy(logFilePath); err != nil {
		slog.Error("Failed to create log folder hierarchy", "error", err)
		return
	}

	lumber := &lumberjack.Logger{
		Filename: logFilePath,
		Compress: true,
	}

	handler := &MultiLevelHandler{
		fileHandler: tint.NewHandler(lumber, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.RFC3339,
		}),
	}

	slog.SetDefault(slog.New(handler))

	//overwrite standard log so it's always redirected to slog, in case some deep dep is using it
	lw := &slogWriter{}
	log.Default().SetOutput(lw)
	log.SetOutput(lw)
}

func SetupBackendLogging(loggingConfig *pkgmodel.LoggingConfig, otelConfig *pkgmodel.OTelConfig) {
	if err := util.EnsureFileFolderHierarchy(loggingConfig.FilePath); err != nil {
		slog.Error("Failed to create log folder hierarchy", "error", err)
		return
	}

	lumber := &lumberjack.Logger{
		Filename: loggingConfig.FilePath,
		Compress: true,
	}

	var consoleHandler slog.Handler = nil
	if loggingConfig.ConsoleLogLevel != NoLoggingLevel {
		consoleHandler = tint.NewHandler(os.Stdout, &tint.Options{
			Level:      loggingConfig.ConsoleLogLevel,
			TimeFormat: time.RFC3339,
		})
	}

	var otelHandler slog.Handler = nil
	if otelConfig != nil && otelConfig.Enabled {
		otelHandler = setupOTelHandler(otelConfig.ServiceName,
			otelConfig.OTLP.Endpoint,
			otelConfig.OTLP.Protocol,
			otelConfig.OTLP.Insecure)
	}

	handler := &MultiLevelHandler{
		fileHandler: tint.NewHandler(lumber, &tint.Options{
			Level:      loggingConfig.FileLogLevel,
			TimeFormat: time.RFC3339,
		}),
		consoleHandler: consoleHandler,
		otelHandler:    otelHandler,
	}

	slog.SetDefault(slog.New(handler))

	//overwrite standard log so it's always redirected to slog, in case some deep dep is using it
	lw := &slogWriter{}
	log.Default().SetOutput(lw)
	log.SetOutput(lw)
}

type MultiLevelHandler struct {
	fileHandler    slog.Handler
	consoleHandler slog.Handler
	otelHandler    slog.Handler
}

func (h *MultiLevelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.fileHandler.Enabled(ctx, level) ||
		h.consoleHandler.Enabled(ctx, level)
}

func (h *MultiLevelHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.fileHandler.Enabled(ctx, r.Level) {
		if err := h.fileHandler.Handle(ctx, r); err != nil {
			return err
		}
	}

	if h.consoleHandler != nil && h.consoleHandler.Enabled(ctx, r.Level) {
		if err := h.consoleHandler.Handle(ctx, r); err != nil {
			return err
		}
	}

	if h.otelHandler != nil && h.otelHandler.Enabled(ctx, r.Level) {
		if err := h.otelHandler.Handle(ctx, r); err != nil {
			return err
		}
	}

	return nil
}

func (h *MultiLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &MultiLevelHandler{
		fileHandler:    h.fileHandler.WithAttrs(attrs),
		consoleHandler: h.consoleHandler.WithAttrs(attrs),
		otelHandler:    h.otelHandler.WithAttrs(attrs),
	}
}

func (h *MultiLevelHandler) WithGroup(name string) slog.Handler {
	return &MultiLevelHandler{
		fileHandler:    h.fileHandler.WithGroup(name),
		consoleHandler: h.consoleHandler.WithGroup(name),
		otelHandler:    h.otelHandler.WithGroup(name),
	}
}
