// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	glog "github.com/labstack/gommon/log"
)

type EchoLogger struct {
	Logger *slog.Logger
}

// This is Echo's Logger interface implementation

func (l *EchoLogger) Output() io.Writer {
	return io.Discard
}

func (l *EchoLogger) SetOutput(w io.Writer) {
	// no-op as slog handles its own output
}

func (l *EchoLogger) Prefix() string {
	return ""
}

func (l *EchoLogger) SetPrefix(p string) {
	// no-op as slog handles its own prefix
}

func (l *EchoLogger) Level() glog.Lvl {
	return glog.DEBUG
}

func (l *EchoLogger) SetLevel(v glog.Lvl) {
}

func (l *EchoLogger) SetHeader(h string) {
	// no-op as slog handles its own header
}

func (l *EchoLogger) Print(i ...any) {
	l.Logger.Info(fmt.Sprint(i...))
}

func (l *EchoLogger) Printf(format string, args ...any) {
	l.Logger.Info(fmt.Sprintf(format, args...))
}

func (l *EchoLogger) Printj(j glog.JSON) {
	l.Logger.Info("json", "data", j)
}

func (l *EchoLogger) Debug(i ...any) {
	l.Logger.Debug(fmt.Sprint(i...))
}

func (l *EchoLogger) Debugf(format string, args ...any) {
	l.Logger.Debug(fmt.Sprintf(format, args...))
}

func (l *EchoLogger) Debugj(j glog.JSON) {
	l.Logger.Debug("json", "data", j)
}

func (l *EchoLogger) Info(i ...any) {
	l.Logger.Info(fmt.Sprint(i...))
}

func (l *EchoLogger) Infof(format string, args ...any) {
	l.Logger.Info(fmt.Sprintf(format, args...))
}

func (l *EchoLogger) Infoj(j glog.JSON) {
	l.Logger.Info("json", "data", j)
}

func (l *EchoLogger) Warn(i ...any) {
	l.Logger.Warn(fmt.Sprint(i...))
}

func (l *EchoLogger) Warnf(format string, args ...any) {
	l.Logger.Warn(fmt.Sprintf(format, args...))
}

func (l *EchoLogger) Warnj(j glog.JSON) {
	l.Logger.Warn("json", "data", j)
}

func (l *EchoLogger) Error(i ...any) {
	l.Logger.Error(fmt.Sprint(i...))
}

func (l *EchoLogger) Errorf(format string, args ...any) {
	l.Logger.Error(fmt.Sprintf(format, args...))
}

func (l *EchoLogger) Errorj(j glog.JSON) {
	l.Logger.Error("json", "data", j)
}

func (l *EchoLogger) Fatal(i ...any) {
	l.Logger.Error(fmt.Sprint(i...))
	os.Exit(1)
}

func (l *EchoLogger) Fatalf(format string, args ...any) {
	l.Logger.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}

func (l *EchoLogger) Fatalj(j glog.JSON) {
	l.Logger.Error("json", "data", j)
	os.Exit(1)
}

func (l *EchoLogger) Panic(i ...any) {
	s := fmt.Sprint(i...)
	l.Logger.Error(s)
	panic(s)
}

func (l *EchoLogger) Panicf(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	l.Logger.Error(s)
	panic(s)
}

func (l *EchoLogger) Panicj(j glog.JSON) {
	l.Logger.Error("json", "data", j)
	panic(j)
}

func NewEchoLogger() *EchoLogger {
	return &EchoLogger{
		Logger: slog.Default(),
	}
}
