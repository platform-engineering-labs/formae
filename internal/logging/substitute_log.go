// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logging

import "log/slog"

type slogWriter struct{}

func (w *slogWriter) Write(p []byte) (n int, err error) {
	if len(p) > 6 && string(p[:5]) == "ERROR" {
		slog.Error(string(p[6:]))
		return len(p), nil
	} else if len(p) > 5 && string(p[:4]) == "WARN" {
		slog.Warn(string(p[5:]))
		return len(p), nil
	} else if len(p) > 5 && string(p[:4]) == "INFO" {
		slog.Info(string(p[5:]))
		return len(p), nil
	}

	slog.Debug(string(p))
	return len(p), nil
}
