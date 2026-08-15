// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubBootRecorder struct {
	calls []string
	err   error
}

func (s *stubBootRecorder) RecordAgentBoot(version string) error {
	s.calls = append(s.calls, version)
	return s.err
}

func TestRecordBootWritesOnce(t *testing.T) {
	rec := &stubBootRecorder{}

	recordBoot(rec, "0.89.0")

	require.Len(t, rec.calls, 1, "exactly one boot row per process start")
	assert.Equal(t, []string{"0.89.0"}, rec.calls)
}

// A failing boot record must not stop the agent. The row exists to tell a
// console which version is running; letting a display feature take down a
// customer's agent inverts the value entirely, which is the same reasoning that
// makes the exporter sidecar non-essential.
func TestRecordBootSwallowsFailure(t *testing.T) {
	rec := &stubBootRecorder{err: errors.New("datastore unavailable")}

	assert.NotPanics(t, func() {
		recordBoot(rec, "0.89.0")
	}, "a failed boot record must never propagate or panic")

	assert.Len(t, rec.calls, 1, "the failure is not retried in-line")
}
