// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ergo.services/ergo/gen"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// debugCapturingLog records Debug-level messages so a test can inspect what a
// handler rendered into them.
type debugCapturingLog struct {
	gen.Log
	mu   sync.Mutex
	msgs []string
}

func (l *debugCapturingLog) Debug(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, fmt.Sprintf(format, args...))
}
func (l *debugCapturingLog) Trace(string, ...any)   {}
func (l *debugCapturingLog) Info(string, ...any)    {}
func (l *debugCapturingLog) Warning(string, ...any) {}
func (l *debugCapturingLog) Error(string, ...any)   {}
func (l *debugCapturingLog) Panic(string, ...any)   {}

func (l *debugCapturingLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.msgs...)
}

// persistingProcess answers proc.Call with a resource version so the state
// machine proceeds past the persist step, and captures Debug output.
type persistingProcess struct {
	*stubUpdaterProcess
	log *debugCapturingLog
}

func (p *persistingProcess) Log() gen.Log { return p.log }
func (p *persistingProcess) Call(_ any, _ any) (any, error) {
	return "resource-version-1", nil
}

// opaqueGenEnvelope builds a property document whose single property holds a
// drawn generator value: an opaque envelope carrying live plaintext.
func opaqueGenEnvelope(plaintext string) json.RawMessage {
	return json.RawMessage(`{"MasterUserPassword":{"$gen":true,"$generator":"2ABcDeFgHiJkLmNoPqRsTuVwXyZ","$output":"value","$visibility":"Opaque","$value":"` + plaintext + `"}}`)
}

// TestHandleProgressUpdate_RejectionLogWithholdsOpaqueValues asserts that the
// log line explaining why a synchronizing update was rejected names the
// properties that differ without rendering the plaintext of any opaque value
// either of them carries.
func TestHandleProgressUpdate_RejectionLogWithholdsOpaqueValues(t *testing.T) {
	const desiredPlaintext = "desired-plaintext-value-9f3a"
	const previousPlaintext = "previous-plaintext-value-4c7b"

	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		DesiredState: pkgmodel.Resource{
			Label:      "app-database",
			Type:       "FakeAWS::RDS::DBInstance",
			Stack:      "test-stack",
			Properties: opaqueGenEnvelope(desiredPlaintext),
		},
		PreviousProperties: opaqueGenEnvelope(previousPlaintext),
		ResourceTarget:     pkgmodel.Target{Label: "us-east-1"},
		StackLabel:         "test-stack",
	}

	data := ResourceUpdateData{
		resourceUpdate: ru,
		commandID:      "cmd-reject",
	}

	readProgress := plugin.TrackedProgress{
		ProgressResult: resource.ProgressResult{
			Operation:       resource.OperationRead,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        "db-1",
		},
	}

	clog := &debugCapturingLog{}
	proc := &persistingProcess{stubUpdaterProcess: &stubUpdaterProcess{}, log: clog}

	state, _, _, err := handleProgressUpdate(gen.PID{}, StateSynchronizing, data, readProgress, proc)
	require.NoError(t, err)
	require.Equal(t, StateRejected, state, "a change detected during synchronize must reject the update")

	msgs := clog.all()
	var rejection string
	for _, m := range msgs {
		if strings.Contains(m, "rejected") {
			rejection = m
		}
	}
	require.NotEmpty(t, rejection, "the rejection must be explained in a log line, got: %v", msgs)

	assert.Contains(t, rejection, "MasterUserPassword",
		"the line must still name the property that differs")
	assert.NotContains(t, rejection, desiredPlaintext,
		"the drawn value in the desired properties must not be rendered")
	assert.NotContains(t, rejection, previousPlaintext,
		"the drawn value in the previous properties must not be rendered")
}

// TestGetPreservedValueString_WithholdsOpaqueValues asserts that the value
// reported when a SetOnce property or tag keeps its existing setting is
// withheld for an opaque property and reported verbatim for any other.
func TestGetPreservedValueString_WithholdsOpaqueValues(t *testing.T) {
	opaque := gjson.Parse(`{"$strategy":"SetOnce","$visibility":"Opaque","$value":"kept-plaintext-value-2b8f"}`)
	assert.Equal(t, pkgmodel.RedactedForLog, getPreservedValueString(opaque),
		"an opaque preserved value must not be reported")

	clear := gjson.Parse(`{"$strategy":"SetOnce","$visibility":"Clear","$value":"us-east-1a"}`)
	assert.Equal(t, "us-east-1a", getPreservedValueString(clear),
		"a clear preserved value must still be reported")

	plain := gjson.Parse(`"us-east-1a"`)
	assert.Equal(t, "us-east-1a", getPreservedValueString(plain),
		"a bare preserved value must still be reported")
}
