// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_persister

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	pkgresource "github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// storeFailingDatastore rejects every resource write, which is what drives the
// persist-failure reporting under test.
type storeFailingDatastore struct {
	datastore.Datastore
}

func (storeFailingDatastore) StoreResource(*pkgmodel.Resource, string, ...string) (string, error) {
	return "", errors.New("datastore unavailable")
}

// goByteSlice matches the way Go renders a []byte through fmt's %v/%+v verbs:
// a bracketed run of decimal byte values, e.g. "[123 34 80 97 ...]".
var goByteSlice = regexp.MustCompile(`\[\d+(?: \d+)*\]`)

// decodeGoByteSlices returns the text spelled out by every Go-rendered byte
// slice in s. A property document rendered this way carries all of its bytes
// while matching no substring search for the characters they spell, so a
// diagnostic is only clean if the decoded text is clean too.
func decodeGoByteSlices(s string) []string {
	var decoded []string
	for _, match := range goByteSlice.FindAllString(s, -1) {
		fields := strings.Fields(strings.Trim(match, "[]"))
		buf := make([]byte, 0, len(fields))
		ok := true
		for _, f := range fields {
			n, err := strconv.Atoi(f)
			if err != nil || n < 0 || n > 255 {
				ok = false
				break
			}
			buf = append(buf, byte(n))
		}
		if ok && len(buf) > 0 {
			decoded = append(decoded, string(buf))
		}
	}
	return decoded
}

// TestStoreResourceUpdate_PersistFailureWithholdsOpaqueValues asserts that when
// a resource cannot be persisted, neither the log line nor the returned error
// carries the plaintext of an opaque value the resource holds, in any rendering
// — including the byte-slice rendering a raw JSON document takes through fmt.
// Both must still identify the resource and the property that holds the value.
func TestStoreResourceUpdate_PersistFailureWithholdsOpaqueValues(t *testing.T) {
	const plaintext = "drawn-plaintext-value-7e1d"

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	rp := &ResourcePersister{
		datastore:               storeFailingDatastore{},
		persistValueTransformer: transformations.NewPersistValueTransformer(),
	}

	ru := &resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Label: "app-database",
			Type:  "FakeAWS::RDS::DBInstance",
			Stack: "test-stack",
			Properties: json.RawMessage(`{"MasterUserPassword":{"$gen":true,` +
				`"$generator":"2ABcDeFgHiJkLmNoPqRsTuVwXyZ","$output":"value",` +
				`"$visibility":"Opaque","$value":"` + plaintext + `"}}`),
		},
		StackLabel: "test-stack",
		State:      resource_update.ResourceUpdateStateSuccess,
		ProgressResult: []plugin.TrackedProgress{{
			ProgressResult: pkgresource.ProgressResult{
				Operation:       pkgresource.OperationCreate,
				OperationStatus: pkgresource.OperationStatusSuccess,
				NativeID:        "db-1",
			},
		}},
	}

	_, err := rp.storeResourceUpdate("cmd-persist", resource_update.OperationCreate, pkgresource.OperationCreate, ru)
	require.Error(t, err, "a failing datastore must surface as an error")

	logged := buf.String()
	require.Contains(t, logged, "Failed to persist resource updates",
		"the persist failure must be reported in a log line")
	assert.Contains(t, logged, "app-database", "the log line must still identify the resource")

	for _, subject := range []struct {
		name string
		text string
	}{
		{"log", logged},
		{"error", err.Error()},
	} {
		assert.NotContains(t, subject.text, plaintext,
			"the %s must not render the opaque plaintext", subject.name)
		for _, decoded := range decodeGoByteSlices(subject.text) {
			assert.NotContains(t, decoded, plaintext,
				"the %s must not carry the opaque plaintext as a byte slice", subject.name)
		}
	}
}
