// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logging

import (
	"fmt"
	"log/slog"
)

type PosthogLogger struct {
}

func (p *PosthogLogger) Debugf(format string, args ...interface{}) {
	slog.Debug(fmt.Sprintf("Posthog: "+format, args...))
}

func (p *PosthogLogger) Logf(format string, args ...interface{}) {
	slog.Info(fmt.Sprintf("Posthog: "+format, args...))
}

func (p *PosthogLogger) Warnf(format string, args ...interface{}) {
	slog.Warn(fmt.Sprintf("Posthog: "+format, args...))
}

func (p *PosthogLogger) Errorf(format string, args ...interface{}) {
	slog.Error(fmt.Sprintf("Posthog: "+format, args...))
}
