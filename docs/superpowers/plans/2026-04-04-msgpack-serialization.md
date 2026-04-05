# MessagePack Serialization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace EDF's native struct encoding with MessagePack + zstd compression on all cross-node plugin message types, achieving backwards/forward compatibility for the plugin wire protocol.

**Architecture:** Each cross-node message type implements Ergo's `MarshalEDF`/`UnmarshalEDF` interfaces, delegating to shared helpers that do MessagePack serialization + zstd compression. This makes the wire format transparent to actor handlers while eliminating the need for nested EDF type registration and ad-hoc per-field gzip compression.

**Tech Stack:** `vmihailenco/msgpack/v5` (MessagePack codec), `klauspost/compress/zstd` (compression), Ergo `net/edf` (custom marshaler interfaces)

**Worktree:** `/home/jeroen/dev/pel/formae-msgpack` (branch `feat/msgpack-serialization`, based on `feat/extensions-coordinator`)

**Spec:** `docs/superpowers/specs/2026-04-01-msgpack-serialization-design.md`

---

### Task 1: Shared MessagePack + zstd helpers

**Files:**
- Create: `pkg/plugin/msgpack.go`
- Create: `pkg/plugin/msgpack_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// pkg/plugin/msgpack_test.go
package plugin

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStruct is a simple struct for round-trip tests
type testStruct struct {
	Name  string          `msgpack:"Name"`
	Value int             `msgpack:"Value"`
	Data  json.RawMessage `msgpack:"Data"`
}

func TestMsgpackRoundTrip(t *testing.T) {
	original := testStruct{
		Name:  "test",
		Value: 42,
		Data:  json.RawMessage(`{"key":"value"}`),
	}

	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	require.NoError(t, err)

	var decoded testStruct
	err = decodeMsgpack(buf.Bytes(), &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Value, decoded.Value)
	assert.JSONEq(t, string(original.Data), string(decoded.Data))
}

func TestMsgpackForwardCompatibility(t *testing.T) {
	// Simulates an old plugin receiving a message with unknown fields.
	// The unknown fields should be silently ignored.
	type v2Struct struct {
		Name     string `msgpack:"Name"`
		Value    int    `msgpack:"Value"`
		NewField bool   `msgpack:"NewField"`
	}
	type v1Struct struct {
		Name  string `msgpack:"Name"`
		Value int    `msgpack:"Value"`
	}

	original := v2Struct{Name: "test", Value: 42, NewField: true}
	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	require.NoError(t, err)

	var decoded v1Struct
	err = decodeMsgpack(buf.Bytes(), &decoded)
	require.NoError(t, err)

	assert.Equal(t, "test", decoded.Name)
	assert.Equal(t, 42, decoded.Value)
}

func TestMsgpackBackwardCompatibility(t *testing.T) {
	// Simulates a new agent receiving a message from an old plugin
	// missing a field. The missing field should get its zero value.
	type v1Struct struct {
		Name  string `msgpack:"Name"`
		Value int    `msgpack:"Value"`
	}
	type v2Struct struct {
		Name     string `msgpack:"Name"`
		Value    int    `msgpack:"Value"`
		NewField bool   `msgpack:"NewField"`
	}

	original := v1Struct{Name: "test", Value: 42}
	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	require.NoError(t, err)

	var decoded v2Struct
	err = decodeMsgpack(buf.Bytes(), &decoded)
	require.NoError(t, err)

	assert.Equal(t, "test", decoded.Name)
	assert.Equal(t, 42, decoded.Value)
	assert.False(t, decoded.NewField) // zero value default
}

func TestMsgpackNonZeroDefaults(t *testing.T) {
	// Simulates setting non-zero defaults before decoding
	type v1Struct struct {
		Name string `msgpack:"Name"`
	}
	type v2Struct struct {
		Name     string `msgpack:"Name"`
		NewField bool   `msgpack:"NewField"`
	}

	original := v1Struct{Name: "test"}
	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	require.NoError(t, err)

	var decoded v2Struct
	decoded.NewField = true // set non-zero default before decode
	err = decodeMsgpack(buf.Bytes(), &decoded)
	require.NoError(t, err)

	assert.Equal(t, "test", decoded.Name)
	assert.True(t, decoded.NewField) // preserved because not in payload
}

func TestMsgpackJSONRawMessage_NilHandling(t *testing.T) {
	type msg struct {
		Data json.RawMessage `msgpack:"Data"`
	}

	original := msg{Data: nil}
	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	require.NoError(t, err)

	var decoded msg
	err = decodeMsgpack(buf.Bytes(), &decoded)
	require.NoError(t, err)
	assert.Nil(t, decoded.Data)
}

func TestMsgpackCompression(t *testing.T) {
	// Verify that encoded data is actually compressed (smaller than raw msgpack)
	large := testStruct{
		Name:  "test",
		Value: 42,
		Data:  json.RawMessage(`{"key":"` + strings.Repeat("a", 10000) + `"}`),
	}

	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &large)
	require.NoError(t, err)

	// Compressed should be significantly smaller than the raw JSON
	assert.Less(t, buf.Len(), 10000)

	var decoded testStruct
	err = decodeMsgpack(buf.Bytes(), &decoded)
	require.NoError(t, err)
	assert.JSONEq(t, string(large.Data), string(decoded.Data))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go test -tags=unit -run TestMsgpack -v ./pkg/plugin/...`
Expected: FAIL — `encodeMsgpack` and `decodeMsgpack` are undefined

- [ ] **Step 3: Write the implementation**

```go
// pkg/plugin/msgpack.go
package plugin

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"
)

// encodeMsgpack serializes v to MessagePack, compresses with zstd, and writes to w.
// This is the shared encoder for all cross-node message types.
func encodeMsgpack(w io.Writer, v any) error {
	data, err := marshalMsgpack(v)
	if err != nil {
		return fmt.Errorf("msgpack marshal: %w", err)
	}

	enc, err := zstd.NewWriter(w)
	if err != nil {
		return fmt.Errorf("zstd writer: %w", err)
	}

	if _, err := enc.Write(data); err != nil {
		enc.Close()
		return fmt.Errorf("zstd write: %w", err)
	}

	return enc.Close()
}

// decodeMsgpack decompresses zstd data and deserializes MessagePack into v.
// This is the shared decoder for all cross-node message types.
func decodeMsgpack(data []byte, v any) error {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return fmt.Errorf("zstd reader: %w", err)
	}
	defer dec.Close()

	decompressed, err := dec.DecodeAll(data, nil)
	if err != nil {
		return fmt.Errorf("zstd decompress: %w", err)
	}

	return unmarshalMsgpack(decompressed, v)
}

// marshalMsgpack encodes v to MessagePack with custom json.RawMessage handling.
func marshalMsgpack(v any) ([]byte, error) {
	enc := msgpack.GetEncoder()
	defer msgpack.PutEncoder(enc)

	var buf []byte
	enc.Reset(&sliceWriter{buf: &buf})
	enc.SetCustomStructTag("msgpack")

	// Register json.RawMessage as binary
	enc.UseCompactInts(true)

	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf, nil
}

// unmarshalMsgpack decodes MessagePack data into v with custom json.RawMessage handling.
func unmarshalMsgpack(data []byte, v any) error {
	dec := msgpack.NewDecoder(nil)
	dec.SetCustomStructTag("msgpack")
	dec.ResetBytes(data)
	return dec.Decode(v)
}

// sliceWriter is a minimal io.Writer that appends to a byte slice.
type sliceWriter struct {
	buf *[]byte
}

func (w *sliceWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

// Register json.RawMessage custom encoder/decoder with msgpack.
// json.RawMessage is stored as binary bytes, preserving exact JSON content.
func init() {
	msgpack.Register[json.RawMessage](
		func(enc *msgpack.Encoder, v json.RawMessage) error {
			return enc.EncodeBytes([]byte(v))
		},
		func(dec *msgpack.Decoder) (json.RawMessage, error) {
			b, err := dec.DecodeBytes()
			if err != nil {
				return nil, err
			}
			if b == nil {
				return nil, nil
			}
			return json.RawMessage(b), nil
		},
	)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go test -tags=unit -run TestMsgpack -v ./pkg/plugin/...`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add pkg/plugin/msgpack.go pkg/plugin/msgpack_test.go
git commit -m "feat(plugin): add shared msgpack+zstd serialization helpers"
```

---

### Task 2: Add MarshalEDF/UnmarshalEDF to cross-node message types

**Files:**
- Create: `pkg/plugin/msgpack_edf.go`
- Create: `pkg/plugin/msgpack_edf_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/plugin/msgpack_edf_test.go
package plugin

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateResourceMarshalEDF(t *testing.T) {
	msg := CreateResource{
		Namespace:    "aws",
		ResourceType: "AWS::S3::Bucket",
		Label:        "my-bucket",
		Properties:   json.RawMessage(`{"BucketName":"test"}`),
		TargetConfig: json.RawMessage(`{"region":"us-east-1"}`),
		Version:      1,
	}

	var buf bytes.Buffer
	err := msg.MarshalEDF(&buf)
	require.NoError(t, err)

	var decoded CreateResource
	err = decoded.UnmarshalEDF(buf.Bytes())
	require.NoError(t, err)

	assert.Equal(t, msg.Namespace, decoded.Namespace)
	assert.Equal(t, msg.ResourceType, decoded.ResourceType)
	assert.Equal(t, msg.Label, decoded.Label)
	assert.JSONEq(t, string(msg.Properties), string(decoded.Properties))
	assert.JSONEq(t, string(msg.TargetConfig), string(decoded.TargetConfig))
	assert.Equal(t, msg.Version, decoded.Version)
}

func TestReadResourceMarshalEDF(t *testing.T) {
	msg := ReadResource{
		Namespace:         "aws",
		NativeID:          "bucket-123",
		ResourceType:      "AWS::S3::Bucket",
		ResourceNamespace: "aws",
		TargetConfig:      json.RawMessage(`{"region":"us-east-1"}`),
		Resource:          model.Resource{Type: "AWS::S3::Bucket", NativeID: "bucket-123"},
		ExistingResource:  model.Resource{Type: "AWS::S3::Bucket", NativeID: "bucket-123"},
		IsSync:            true,
		RedactSensitive:   true,
	}

	var buf bytes.Buffer
	err := msg.MarshalEDF(&buf)
	require.NoError(t, err)

	var decoded ReadResource
	err = decoded.UnmarshalEDF(buf.Bytes())
	require.NoError(t, err)

	assert.Equal(t, msg.Namespace, decoded.Namespace)
	assert.Equal(t, msg.NativeID, decoded.NativeID)
	assert.Equal(t, msg.ResourceType, decoded.ResourceType)
	assert.Equal(t, msg.Resource.Type, decoded.Resource.Type)
	assert.Equal(t, msg.IsSync, decoded.IsSync)
	assert.Equal(t, msg.RedactSensitive, decoded.RedactSensitive)
}

func TestTrackedProgressMarshalEDF(t *testing.T) {
	msg := TrackedProgress{
		ProgressResult: resource.ProgressResult{
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           "bucket-123",
			ResourceProperties: json.RawMessage(`{"BucketName":"test"}`),
		},
		ResourceType: "AWS::S3::Bucket",
		StartTs:      time.Now().Truncate(time.Second),
		ModifiedTs:   time.Now().Truncate(time.Second),
		Attempts:     1,
		MaxAttempts:  3,
	}

	var buf bytes.Buffer
	err := msg.MarshalEDF(&buf)
	require.NoError(t, err)

	var decoded TrackedProgress
	err = decoded.UnmarshalEDF(buf.Bytes())
	require.NoError(t, err)

	assert.Equal(t, msg.OperationStatus, decoded.OperationStatus)
	assert.Equal(t, msg.NativeID, decoded.NativeID)
	assert.JSONEq(t, string(msg.ResourceProperties), string(decoded.ResourceProperties))
	assert.Equal(t, msg.ResourceType, decoded.ResourceType)
	assert.Equal(t, msg.Attempts, decoded.Attempts)
}

func TestPluginAnnouncementMarshalEDF(t *testing.T) {
	msg := PluginAnnouncement{
		Namespace:            "aws",
		Version:              "1.0.0",
		NodeName:             "plugin-aws@localhost",
		MaxRequestsPerSecond: 10,
		Capabilities: PluginCapabilities{
			SupportedResources: []ResourceDescriptor{
				{Type: "AWS::S3::Bucket", Discoverable: true, Extractable: true},
			},
			ResourceSchemas: map[string]model.Schema{
				"AWS::S3::Bucket": {
					FieldHints: map[string]model.FieldHint{
						"BucketName": {CreateOnly: true},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	err := msg.MarshalEDF(&buf)
	require.NoError(t, err)

	var decoded PluginAnnouncement
	err = decoded.UnmarshalEDF(buf.Bytes())
	require.NoError(t, err)

	assert.Equal(t, msg.Namespace, decoded.Namespace)
	assert.Equal(t, msg.Version, decoded.Version)
	assert.Equal(t, msg.MaxRequestsPerSecond, decoded.MaxRequestsPerSecond)
	assert.Len(t, decoded.Capabilities.SupportedResources, 1)
	assert.Equal(t, "AWS::S3::Bucket", decoded.Capabilities.SupportedResources[0].Type)
	assert.True(t, decoded.Capabilities.ResourceSchemas["AWS::S3::Bucket"].FieldHints["BucketName"].CreateOnly)
}

func TestListingMarshalEDF(t *testing.T) {
	msg := Listing{
		Namespace:    "aws",
		TargetLabel:  "us-east-1",
		Resources:    []ListedResource{{NativeID: "bucket-1", ResourceType: "AWS::S3::Bucket"}},
		ResourceType: "AWS::S3::Bucket",
		TargetConfig: json.RawMessage(`{"region":"us-east-1"}`),
	}

	var buf bytes.Buffer
	err := msg.MarshalEDF(&buf)
	require.NoError(t, err)

	var decoded Listing
	err = decoded.UnmarshalEDF(buf.Bytes())
	require.NoError(t, err)

	assert.Equal(t, msg.Namespace, decoded.Namespace)
	assert.Len(t, decoded.Resources, 1)
	assert.Equal(t, "bucket-1", decoded.Resources[0].NativeID)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go test -tags=unit -run TestCreateResourceMarshalEDF -v ./pkg/plugin/...`
Expected: FAIL — `MarshalEDF` method not defined on `CreateResource`

- [ ] **Step 3: Write the implementation**

This step has two parts: first update the message struct definitions, then add the MarshalEDF/UnmarshalEDF methods.

**Part A: Update message struct definitions in `pkg/plugin/plugin_operator.go`**

Replace the `ReadResource` struct (lines 134-147):
```go
type ReadResource struct {
	Namespace         string          `msgpack:"Namespace"`
	NativeID          string          `msgpack:"NativeID"`
	ResourceType      string          `msgpack:"ResourceType"`
	ResourceNamespace string          `msgpack:"ResourceNamespace"`
	TargetConfig      json.RawMessage `msgpack:"TargetConfig"`
	Resource          model.Resource  `msgpack:"Resource"`
	ExistingResource  model.Resource  `msgpack:"ExistingResource"`
	IsSync            bool            `msgpack:"IsSync"`
	IsDelete          bool            `msgpack:"IsDelete"`
	RedactSensitive   bool            `msgpack:"RedactSensitive"`
	Version           int             `msgpack:"Version"`
}
```

Replace the `CreateResource` struct (lines 153-160):
```go
type CreateResource struct {
	Namespace    string          `msgpack:"Namespace"`
	ResourceType string          `msgpack:"ResourceType"`
	Label        string          `msgpack:"Label"`
	Properties   json.RawMessage `msgpack:"Properties"`
	TargetConfig json.RawMessage `msgpack:"TargetConfig"`
	Version      int             `msgpack:"Version"`
}
```

Replace the `UpdateResource` struct (lines 162-173):
```go
type UpdateResource struct {
	Namespace         string          `msgpack:"Namespace"`
	NativeID          string          `msgpack:"NativeID"`
	ResourceType      string          `msgpack:"ResourceType"`
	Label             string          `msgpack:"Label"`
	PriorProperties   json.RawMessage `msgpack:"PriorProperties"`
	DesiredProperties json.RawMessage `msgpack:"DesiredProperties"`
	PatchDocument     string          `msgpack:"PatchDocument"`
	TargetConfig      json.RawMessage `msgpack:"TargetConfig"`
	Version           int             `msgpack:"Version"`
}
```

Replace the `DeleteResource` struct (lines 175-181):
```go
type DeleteResource struct {
	Namespace    string          `msgpack:"Namespace"`
	NativeID     string          `msgpack:"NativeID"`
	ResourceType string          `msgpack:"ResourceType"`
	TargetConfig json.RawMessage `msgpack:"TargetConfig"`
	Resource     model.Resource  `msgpack:"Resource"`
	Version      int             `msgpack:"Version"`
}
```

Replace the `TrackedProgress` struct (lines 103-111):
```go
type TrackedProgress struct {
	resource.ProgressResult `msgpack:"ProgressResult"`
	ResourceType            string    `msgpack:"ResourceType,omitempty"`
	StartTs                 time.Time `msgpack:"StartTs,omitempty"`
	ModifiedTs              time.Time `msgpack:"ModifiedTs,omitempty"`
	Attempts                int       `msgpack:"Attempts,omitempty"`
	MaxAttempts             int       `msgpack:"MaxAttempts,omitempty"`
	Version                 int       `msgpack:"Version"`
}
```

Add `msgpack` tags to `ListParam` (lines 183-187):
```go
type ListParam struct {
	ParentProperty string `msgpack:"ParentProperty"`
	ListParam      string `msgpack:"ListParam"`
	ListValue      string `msgpack:"ListValue"`
}
```

Add `msgpack` tags to `ListResources` (lines 189-195):
```go
type ListResources struct {
	Namespace      string              `msgpack:"Namespace"`
	TargetLabel    string              `msgpack:"TargetLabel"`
	ResourceType   string              `msgpack:"ResourceType"`
	TargetConfig   json.RawMessage     `msgpack:"TargetConfig"`
	ListParameters map[string]ListParam `msgpack:"ListParameters"`
	Version        int                 `msgpack:"Version"`
}
```

Add `msgpack` tags to `ListedResource` (lines 200-203):
```go
type ListedResource struct {
	NativeID     string `msgpack:"NativeID"`
	ResourceType string `msgpack:"ResourceType"`
}
```

Replace the `Listing` struct (lines 207-215). Note: `Resources` changes from `[]byte` (gzip-compressed) to `[]ListedResource` directly, and the `Error` field changes from `error` to `string` (errors are not serializable across nodes):
```go
type Listing struct {
	Namespace      string               `msgpack:"Namespace"`
	TargetLabel    string               `msgpack:"TargetLabel"`
	Resources      []ListedResource     `msgpack:"Resources"`
	ResourceType   string               `msgpack:"ResourceType"`
	ListParameters map[string]ListParam  `msgpack:"ListParameters"`
	TargetConfig   json.RawMessage      `msgpack:"TargetConfig"`
	Error          string               `msgpack:"Error"`
	Version        int                  `msgpack:"Version"`
}
```

Add `msgpack` tags to `PluginOperatorCheckStatus` (lines 356-364):
```go
type PluginOperatorCheckStatus struct {
	Namespace         string              `msgpack:"Namespace"`
	RequestID         string              `msgpack:"RequestID"`
	NativeID          string              `msgpack:"NativeID"`
	ResourceType      string              `msgpack:"ResourceType"`
	TargetConfig      json.RawMessage     `msgpack:"TargetConfig"`
	ResourceOperation resource.Operation  `msgpack:"ResourceOperation"`
	Request           any                 `msgpack:"Request"`
	Version           int                 `msgpack:"Version"`
}
```

Add `msgpack` tags to lifecycle message types (lines 80-94):
```go
type StartPluginOperation struct {
	Version int `msgpack:"Version"`
}

type PluginOperatorShutdown struct {
	Version int `msgpack:"Version"`
}

type ResumeWaitingForResource struct {
	Namespace         string                  `msgpack:"Namespace"`
	ResourceOperation resource.Operation      `msgpack:"ResourceOperation"`
	Request           PluginOperatorCheckStatus `msgpack:"Request"`
	PreviousAttempts  int                     `msgpack:"PreviousAttempts"`
	Version           int                     `msgpack:"Version"`
}

type PluginOperatorRetry struct {
	ResourceOperation resource.Operation `msgpack:"ResourceOperation"`
	Request           any                `msgpack:"Request"`
	Version           int                `msgpack:"Version"`
}
```

**Part B: Update `PluginAnnouncement` in `pkg/plugin/run.go`** (lines 48-58):
```go
type PluginAnnouncement struct {
	Namespace            string              `msgpack:"Namespace"`
	Version              string              `msgpack:"Version"`
	NodeName             string              `msgpack:"NodeName"`
	MaxRequestsPerSecond int                 `msgpack:"MaxRequestsPerSecond"`
	Capabilities         PluginCapabilities  `msgpack:"Capabilities"`
	MsgVersion           int                 `msgpack:"MsgVersion"`
}
```

Add `msgpack` tags to `PluginCapabilities` (lines 39-44):
```go
type PluginCapabilities struct {
	SupportedResources []ResourceDescriptor    `msgpack:"SupportedResources"`
	ResourceSchemas    map[string]model.Schema `msgpack:"ResourceSchemas"`
	MatchFilters       []MatchFilter           `msgpack:"MatchFilters"`
	LabelConfig        LabelConfig             `msgpack:"LabelConfig"`
}
```

**Part C: Create `pkg/plugin/msgpack_edf.go` with all MarshalEDF/UnmarshalEDF methods:**

```go
// pkg/plugin/msgpack_edf.go
package plugin

import "io"

// MarshalEDF/UnmarshalEDF implementations for all cross-node message types.
// These use the shared encodeMsgpack/decodeMsgpack helpers which handle
// MessagePack serialization + zstd compression transparently.

func (m *ReadResource) MarshalEDF(w io.Writer) error       { return encodeMsgpack(w, m) }
func (m *ReadResource) UnmarshalEDF(data []byte) error      { return decodeMsgpack(data, m) }

func (m *CreateResource) MarshalEDF(w io.Writer) error      { return encodeMsgpack(w, m) }
func (m *CreateResource) UnmarshalEDF(data []byte) error     { return decodeMsgpack(data, m) }

func (m *UpdateResource) MarshalEDF(w io.Writer) error      { return encodeMsgpack(w, m) }
func (m *UpdateResource) UnmarshalEDF(data []byte) error     { return decodeMsgpack(data, m) }

func (m *DeleteResource) MarshalEDF(w io.Writer) error      { return encodeMsgpack(w, m) }
func (m *DeleteResource) UnmarshalEDF(data []byte) error     { return decodeMsgpack(data, m) }

func (m *ListResources) MarshalEDF(w io.Writer) error       { return encodeMsgpack(w, m) }
func (m *ListResources) UnmarshalEDF(data []byte) error      { return decodeMsgpack(data, m) }

func (m *Listing) MarshalEDF(w io.Writer) error             { return encodeMsgpack(w, m) }
func (m *Listing) UnmarshalEDF(data []byte) error            { return decodeMsgpack(data, m) }

func (m *PluginOperatorCheckStatus) MarshalEDF(w io.Writer) error { return encodeMsgpack(w, m) }
func (m *PluginOperatorCheckStatus) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m *TrackedProgress) MarshalEDF(w io.Writer) error     { return encodeMsgpack(w, m) }
func (m *TrackedProgress) UnmarshalEDF(data []byte) error    { return decodeMsgpack(data, m) }

func (m *PluginAnnouncement) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, m) }
func (m *PluginAnnouncement) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m *StartPluginOperation) MarshalEDF(w io.Writer) error { return encodeMsgpack(w, m) }
func (m *StartPluginOperation) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m *PluginOperatorShutdown) MarshalEDF(w io.Writer) error { return encodeMsgpack(w, m) }
func (m *PluginOperatorShutdown) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m *ResumeWaitingForResource) MarshalEDF(w io.Writer) error { return encodeMsgpack(w, m) }
func (m *ResumeWaitingForResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m *PluginOperatorRetry) MarshalEDF(w io.Writer) error { return encodeMsgpack(w, m) }
func (m *PluginOperatorRetry) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }
```

**NOTE:** The `msgpack` struct tags also need to be added to all model types that appear as fields in cross-node messages. Specifically these types in `pkg/model/` and `pkg/plugin/`:
- `model.Resource`, `model.Schema`, `model.FieldHint`, `model.Description`, `model.Prop`, `model.Target`, `model.RetryConfig`, `model.ConfigFieldHint`, `model.ConfigSchema`
- `resource.ProgressResult`, `resource.OperationStatus`, `resource.Operation`, `resource.OperationErrorCode`
- `ResourceDescriptor`, `ListParameter`, `MatchFilter`, `FilterCondition`, `LabelConfig`

For each of these, add `msgpack` tags mirroring the existing `json` tags. The `vmihailenco/msgpack` library can use `json` tags via `SetCustomStructTag("json")`, but we use explicit `msgpack` tags for cross-node types to make the wire contract visible. For model types that already have `json` tags, configure the msgpack encoder/decoder to fall back to `json` tags. Update the `marshalMsgpack`/`unmarshalMsgpack` helpers to call `enc.SetCustomStructTag("msgpack")` and `dec.SetCustomStructTag("msgpack")` — the library will first look for `msgpack` tags, then fall back to `json` tags if not found.

**IMPORTANT:** Verify that `vmihailenco/msgpack` falls back to `json` tags when `msgpack` tags are not present. If it does, model types with only `json` tags will serialize correctly without adding `msgpack` tags. Test this in step 4.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go test -tags=unit -run "TestCreateResourceMarshalEDF|TestReadResourceMarshalEDF|TestTrackedProgressMarshalEDF|TestPluginAnnouncementMarshalEDF|TestListingMarshalEDF" -v ./pkg/plugin/...`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add pkg/plugin/msgpack_edf.go pkg/plugin/msgpack_edf_test.go pkg/plugin/plugin_operator.go pkg/plugin/run.go
git commit -m "feat(plugin): add MarshalEDF/UnmarshalEDF to all cross-node message types"
```

---

### Task 3: Simplify EDF registration

**Files:**
- Modify: `pkg/plugin/edf_registration.go`

- [ ] **Step 1: Replace the entire registration function**

The old function registered ~40 types in dependency order. The new version only registers the 13 cross-node message types — EDF never looks inside them because our custom marshaler handles the internals.

```go
// pkg/plugin/edf_registration.go
package plugin

import (
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
)

// RegisterSharedEDFTypes registers all EDF types shared between the agent and plugins.
// Both the agent and plugin processes must call this function to enable network
// serialization of messages.
//
// Only top-level cross-node message types need registration. Each type implements
// MarshalEDF/UnmarshalEDF which handles serialization via MessagePack + zstd
// compression. Nested types (model.Resource, model.Schema, etc.) are serialized
// inside the MessagePack blob, invisible to EDF.
func RegisterSharedEDFTypes() error {
	types := []any{
		// Agent <-> Plugin CRUD operations
		ReadResource{},
		CreateResource{},
		UpdateResource{},
		DeleteResource{},
		ListResources{},
		PluginOperatorCheckStatus{},

		// Plugin -> Agent results
		TrackedProgress{},
		Listing{},
		PluginAnnouncement{},

		// PluginOperator lifecycle (spawned remotely on plugin node)
		StartPluginOperation{},
		PluginOperatorShutdown{},
		ResumeWaitingForResource{},
		PluginOperatorRetry{},
	}

	for _, t := range types {
		if err := edf.RegisterTypeOf(t); err != nil && err != gen.ErrTaken {
			return err
		}
	}

	return nil
}
```

- [ ] **Step 2: Verify the build compiles**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go build ./...`
Expected: Clean build (no errors). The removed imports (`time`, `model`, `resource`) are no longer needed.

- [ ] **Step 3: Run existing tests**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go test -tags=unit -v ./pkg/plugin/...`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add pkg/plugin/edf_registration.go
git commit -m "refactor(plugin): simplify EDF registration to 13 cross-node types"
```

---

### Task 4: Remove ad-hoc compression from PluginOperator (plugin side)

**Files:**
- Modify: `pkg/plugin/plugin_operator.go` — remove compression helpers and decompression in `create`, `update`, `handlePluginResult`, `list`
- Modify: `pkg/plugin/run.go` — remove `CompressCapabilities`/`DecompressCapabilities`
- Modify: `pkg/plugin/actor.go` — remove capability compression call
- Delete or rewrite: `pkg/plugin/plugin_operator_test.go` — old compression tests no longer apply

- [ ] **Step 1: Remove compression helpers from `plugin_operator.go`**

Delete these functions entirely:
- `CompressListedResources` (lines 219-240)
- `DecompressListedResources` (lines 243-260)
- `CompressResource` (lines 267-288)
- `DecompressResource` (lines 291-308)
- `CompressJSON` (lines 313-333)
- `DecompressJSON` (lines 337-354)

- [ ] **Step 2: Remove decompression from `create` handler (lines 689-700)**

The `create` function currently decompresses `CompressedProperties`. Since `Properties` is now always populated (MessagePack handles it), simplify to:

```go
func create(from gen.PID, state gen.Atom, data PluginUpdateData, operation CreateResource, proc gen.Process) (gen.Atom, PluginUpdateData, TrackedProgress, []statemachine.Action, error) {
	data.requestedBy = from
	data.operationStartTime = time.Now()
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, data.newNamespaceMismatchError(), nil, nil
	}

	proc.Log().Debug("PluginOperator: starting create operation for %s", operation.ResourceType)

	result, err := data.plugin.Create(data.context, &resource.CreateRequest{
		ResourceType: operation.ResourceType,
		Label:        operation.Label,
		Properties:   operation.Properties,
		TargetConfig: operation.TargetConfig,
	})
	// ... rest unchanged
```

- [ ] **Step 3: Remove decompression from `update` handler (lines 726-744)**

Simplify to use `operation.PriorProperties` and `operation.DesiredProperties` directly:

```go
func update(from gen.PID, state gen.Atom, data PluginUpdateData, operation UpdateResource, proc gen.Process) (gen.Atom, PluginUpdateData, TrackedProgress, []statemachine.Action, error) {
	data.requestedBy = from
	data.operationStartTime = time.Now()
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, data.newNamespaceMismatchError(), nil, nil
	}

	result, err := data.plugin.Update(data.context, &resource.UpdateRequest{
		NativeID:          operation.NativeID,
		ResourceType:      operation.ResourceType,
		Label:             operation.Label,
		PriorProperties:   operation.PriorProperties,
		DesiredProperties: operation.DesiredProperties,
		PatchDocument:     &operation.PatchDocument,
		TargetConfig:      operation.TargetConfig,
	})
	// ... rest unchanged
```

- [ ] **Step 4: Remove compression from `handlePluginResult` (lines 917-926)**

Remove the block that compresses `ResourceProperties` into `CompressedResourceProperties`:

```go
// In handlePluginResult, DELETE these lines:
// Compress ResourceProperties for Ergo transport (64KB limit)
if len(progress.ResourceProperties) > 0 {
    compressed, err := CompressJSON(progress.ResourceProperties)
    ...
}
```

- [ ] **Step 5: Update `list` handler — change `Listing.Resources` from compressed bytes to direct slice**

In the `list` function (around line 1061), replace the `CompressListedResources` call:

```go
// Before:
compressedResources, err := CompressListedResources(listedResources)
// ... error handling ...
listing := Listing{
    Resources: compressedResources,
    ...
}

// After:
listing := Listing{
    Resources: listedResources,
    ...
}
```

Also update the `Error` field assignment from `error` to `string` wherever `Listing` is constructed with an error.

- [ ] **Step 6: Remove capability compression from `run.go` and `actor.go`**

In `pkg/plugin/run.go`, delete `CompressCapabilities` (lines 62-83) and `DecompressCapabilities` (lines 86-99).

In `pkg/plugin/actor.go` (lines 57-71), replace the compression logic:

```go
// Before:
caps := PluginCapabilities{...}
compressedCaps, err := CompressCapabilities(caps)
// ...
announcement := PluginAnnouncement{
    Capabilities: compressedCaps,
    ...
}

// After:
caps := PluginCapabilities{...}
announcement := PluginAnnouncement{
    Capabilities: caps,
    ...
}
```

- [ ] **Step 7: Delete or rewrite `plugin_operator_test.go`**

The old tests (`TestCompressDecompressJSON`, `TestCompressJSON_Nil`, etc.) test deleted functions. Remove them. The new `msgpack_test.go` and `msgpack_edf_test.go` cover the replacement functionality.

- [ ] **Step 8: Remove unused imports**

Remove `bytes`, `compress/gzip` from `plugin_operator.go` and `run.go` if no longer used.

- [ ] **Step 9: Verify build and tests**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go build ./pkg/plugin/...`
Expected: Clean build

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go test -tags=unit -v ./pkg/plugin/...`
Expected: ALL PASS

- [ ] **Step 10: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add pkg/plugin/plugin_operator.go pkg/plugin/plugin_operator_test.go pkg/plugin/run.go pkg/plugin/actor.go
git commit -m "refactor(plugin): remove ad-hoc gzip compression from plugin operator"
```

---

### Task 5: Remove ad-hoc compression from agent side

**Files:**
- Modify: `internal/metastructure/resource_update/resource_updater.go` — remove compression calls when building CRUD messages, remove decompression of `TrackedProgress`
- Modify: `internal/metastructure/changeset/resolve_cache.go` — remove `CompressResource` call
- Modify: `internal/metastructure/plugin_coordinator/plugin_coordinator.go` — remove `DecompressCapabilities` call

- [ ] **Step 1: Update `resource_updater.go` — remove compression when building ReadResource**

Around lines 356-367, replace the compression calls:

```go
// Before:
compResource, err := plugin.CompressResource(convertedResource)
// ... error handling ...
compExisting, err := plugin.CompressResource(convertedExisting)
// ... error handling ...
readOp := plugin.ReadResource{
    Resource:         compResource,
    ExistingResource: compExisting,
    ...
}

// After:
readOp := plugin.ReadResource{
    Resource:         convertedResource,
    ExistingResource: convertedExisting,
    ...
}
```

- [ ] **Step 2: Update `resource_updater.go` — remove compression when building DeleteResource**

Around line 400:

```go
// Before:
compDeleteResource, err := plugin.CompressResource(convertedResource)
// ... error handling ...
deleteOp := plugin.DeleteResource{
    Resource: compDeleteResource,
    ...
}

// After:
deleteOp := plugin.DeleteResource{
    Resource: convertedResource,
    ...
}
```

- [ ] **Step 3: Update `resource_updater.go` — remove compression when building CreateResource**

Around lines 482-493:

```go
// Before:
compressedProps, err := plugin.CompressJSON(convertedResource.Properties)
// ... error handling ...
createOperation := plugin.CreateResource{
    CompressedProperties: compressedProps,
    ...
}

// After:
createOperation := plugin.CreateResource{
    Properties: convertedResource.Properties,
    ...
}
```

- [ ] **Step 4: Update `resource_updater.go` — remove compression when building UpdateResource**

Around lines 575-597:

```go
// Before:
compressedDesired, err := plugin.CompressJSON(convertedResource.Properties)
compressedPrior, err := plugin.CompressJSON(convertedExisting.Properties)
updateOperation := plugin.UpdateResource{
    CompressedPriorProperties:   compressedPrior,
    CompressedDesiredProperties: compressedDesired,
    ...
}

// After:
updateOperation := plugin.UpdateResource{
    PriorProperties:   convertedExisting.Properties,
    DesiredProperties: convertedResource.Properties,
    ...
}
```

- [ ] **Step 5: Update `resource_updater.go` — remove decompression of TrackedProgress**

In `handleProgressUpdate` (around lines 668-674), delete the decompression block:

```go
// DELETE:
if len(message.CompressedResourceProperties) > 0 && len(message.ResourceProperties) == 0 {
    decompressed, err := plugin.DecompressJSON(message.CompressedResourceProperties)
    ...
}
```

Also remove the same pattern around lines 919-924 (in the synchronous call path).

- [ ] **Step 6: Update `resolve_cache.go` — remove `CompressResource` call**

Around lines 138-153:

```go
// Before:
compRes, err := plugin.CompressResource(loadResourceResult.Resource)
// ... error handling ...
retry := resolveRetry{
    compRes: compRes,
    ...
}

// After:
retry := resolveRetry{
    ...  // remove compRes field entirely
}
```

Update the `resolveRetry` struct to replace `compRes []byte` with `resource model.Resource`:

```go
type resolveRetry struct {
    From        gen.PID
    ResourceURI pkgmodel.FormaeURI
    Attempt     int
    loadResult  messages.LoadResourceResult
    resource    model.Resource
    config      json.RawMessage
}
```

Update `continueResolve` where it builds `ReadResource` to use the resource directly:

```go
// Before:
ExistingResource: retry.compRes,
Resource:         retry.compRes,

// After:
ExistingResource: retry.resource,
Resource:         retry.resource,
```

- [ ] **Step 7: Update `plugin_coordinator.go` — remove `DecompressCapabilities`**

Around lines 145-149:

```go
// Before:
caps, err := plugin.DecompressCapabilities(msg.Capabilities)

// After (Capabilities is now a direct struct):
caps := msg.Capabilities
```

- [ ] **Step 8: Verify build**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go build ./...`
Expected: Clean build

- [ ] **Step 9: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add internal/metastructure/resource_update/resource_updater.go \
    internal/metastructure/changeset/resolve_cache.go \
    internal/metastructure/plugin_coordinator/plugin_coordinator.go
git commit -m "refactor(agent): remove ad-hoc gzip compression from resource updater and resolve cache"
```

---

### Task 6: Update conformance tests

**Files:**
- Modify: `pkg/plugin-conformance-tests/plugin_coordinator.go`

- [ ] **Step 1: Remove all decompression calls**

The conformance test `TestPluginCoordinator` has 4 decompression sites:
1. `DecompressCapabilities` (line 241) — replace with direct access to `msg.Capabilities`
2. `DecompressJSON` for `CompressedResourceProperties` (lines 272-275) — delete the block
3. `DecompressJSON` for `CompressedResourceProperties` (lines 403-407) — delete the block
4. `CompressResource` for DeleteResource (line 430) — replace with direct `model.Resource`
5. `DecompressJSON` for `CompressedResourceProperties` (lines 466-469) — delete the block

Also update `Listing.Resources` access — it's now `[]ListedResource` directly instead of compressed bytes.

- [ ] **Step 2: Update `Listing.Error` references**

`Listing.Error` changed from `error` to `string`. Update any code that sets or checks this field.

- [ ] **Step 3: Verify build and tests**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go build ./pkg/plugin-conformance-tests/...`
Expected: Clean build

- [ ] **Step 4: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add pkg/plugin-conformance-tests/plugin_coordinator.go
git commit -m "refactor(conformance-tests): remove ad-hoc compression from test plugin coordinator"
```

---

### Task 7: Update Listing.Error consumers and Listing.Resources consumers

**Files:** Find all files that reference `Listing.Error` or `Listing.Resources` and update them.

- [ ] **Step 1: Find all consumers**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
grep -rn "\.Error" --include="*.go" -l | xargs grep -l "Listing" 
grep -rn "DecompressListedResources" --include="*.go"
```

Key sites:
- `internal/metastructure/discovery/` — the Discovery FSM receives `Listing` messages
- `pkg/plugin-conformance-tests/` — already handled in Task 6

- [ ] **Step 2: Update Discovery FSM**

In the discovery code, `Listing.Resources` was `[]byte` (gzip-compressed). Now it's `[]ListedResource` directly. Remove any `DecompressListedResources` calls and use the slice directly.

Also update `Listing.Error` from `error` to `string` comparison.

- [ ] **Step 3: Verify build**

Run: `cd /home/jeroen/dev/pel/formae-msgpack && go build ./...`
Expected: Clean build

- [ ] **Step 4: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add -A
git commit -m "refactor(discovery): update Listing consumers for new message format"
```

---

### Task 8: Promote msgpack dependencies to direct

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Promote indirect dependencies**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
go mod tidy
```

This should promote `vmihailenco/msgpack/v5` and `klauspost/compress` from `// indirect` to direct dependencies since our code now imports them directly.

- [ ] **Step 2: Verify**

Run: `grep -E "msgpack|klauspost/compress" go.mod`
Expected: Both listed without `// indirect`

- [ ] **Step 3: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add go.mod go.sum
git commit -m "chore(deps): promote msgpack and zstd to direct dependencies"
```

---

### Task 9: Run full test suite

- [ ] **Step 1: Run unit tests**

```bash
cd /home/jeroen/dev/pel/formae-msgpack && go test -tags=unit -v ./pkg/plugin/...
```
Expected: ALL PASS

- [ ] **Step 2: Run workflow tests**

```bash
cd /home/jeroen/dev/pel/formae-msgpack && go test -tags=integration -v ./internal/workflow_tests/...
```
Expected: ALL PASS — these spin up a full actor system and exercise the agent<->plugin wire path

- [ ] **Step 3: Run full build**

```bash
cd /home/jeroen/dev/pel/formae-msgpack && make build
```
Expected: Clean build

- [ ] **Step 4: Run linter**

```bash
cd /home/jeroen/dev/pel/formae-msgpack && make lint
```
Expected: No new warnings
