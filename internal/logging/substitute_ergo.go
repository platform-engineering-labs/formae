// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logging

import (
	"fmt"
	"log/slog"

	"ergo.services/ergo/gen"
	"github.com/fatih/color"
)

type ErgoLogger struct {
}

func NewErgoLogger() (gen.LoggerBehavior, error) {
	return &ErgoLogger{}, nil
}

func (l *ErgoLogger) Log(message gen.MessageLog) {
	attrs := []any{}

	// Add source information
	sourceAttrs := l.getSourceAttrs(message.Source)
	attrs = append(attrs, sourceAttrs...)
	if message.Level == gen.LogLevelPanic {
		attrs = append(attrs, "panic", true)
	}

	// Format message args with colors
	args := l.formatArgs(message.Args)
	msg := fmt.Sprintf(message.Format, args...)

	// Append all attributes to the message in order to keep colors
	for i := 0; i < len(attrs); i += 2 {
		if i+1 < len(attrs) {
			msg = fmt.Sprintf("%s [%v=%v]", msg, attrs[i], attrs[i+1])
		}
	}

	// Log with appropriate level
	switch message.Level {
	case gen.LogLevelTrace:
		slog.Debug(msg)
	case gen.LogLevelDebug:
		slog.Debug(msg)
	case gen.LogLevelInfo:
		slog.Info(msg)
	case gen.LogLevelWarning:
		slog.Warn(msg)
	case gen.LogLevelError:
		slog.Error(msg)
	case gen.LogLevelPanic:
		slog.Error(msg)
	default:
		slog.Info(msg)
	}
}

func (l *ErgoLogger) getSourceAttrs(source interface{}) []any {
	var attrs []any

	switch src := source.(type) {
	case gen.MessageLogNode:
		attrs = append(attrs, "node", color.GreenString(src.Node.CRC32()))
	case gen.MessageLogNetwork:
		attrs = append(attrs,
			"node", color.GreenString(src.Node.CRC32()),
			"peer", color.GreenString(src.Peer.CRC32()))
	case gen.MessageLogProcess:
		attrs = append(attrs, "pid", color.BlueString("%s", src.PID))
		if src.Name != "" {
			attrs = append(attrs, "name", color.GreenString(src.Name.String()))
		}

		attrs = append(attrs, "behavior", color.GreenString(src.Behavior))
	case gen.MessageLogMeta:
		attrs = append(attrs, "meta", color.CyanString("%s", src.Meta))
		attrs = append(attrs, "behavior", src.Behavior)
	}

	return attrs
}

func (l *ErgoLogger) formatArgs(args []interface{}) []interface{} {
	formatted := make([]any, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case gen.PID, gen.ProcessID:
			formatted[i] = color.BlueString("%s", v)
		case gen.Atom:
			formatted[i] = color.GreenString("%s", v)
		case gen.Ref, gen.Alias, gen.Event:
			formatted[i] = color.CyanString("%s", v)
		default:
			formatted[i] = v
		}
	}
	return formatted
}

func (l *ErgoLogger) Terminate() {}
