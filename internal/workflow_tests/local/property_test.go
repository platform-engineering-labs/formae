// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build property

// Next steps:
// 1) CloudModify, CloudDelete
// 2) Add dependencies in the mix via Res
//  - With dependencies, we have a bug with updates
//  	- For dependencies, don't utilize updates until fix -> (https://github.com/platform-engineering-labs/formae/issues/90)

// Other Considerations:
// - Multi stack?

package workflow_tests_local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestMetastructure_PropertyBasedChaos(outerT *testing.T) {
	testutil.RunTestFromProjectRoot(outerT, func(t *testing.T) {
		resourceCount := 5

		// Enable individual test operation logging
		// This will create a log file in ./testdata/chaos_logs for each test run
		enableOpsLog := false

		rapid.Check(t, func(rt *rapid.T) {
			// Operation generation
			ops := rapid.SliceOfN(OperationGen(resourceCount), 15, 40).Draw(rt, "ops")

			pt, err := NewPropertyTest(t, rt, resourceCount, enableOpsLog)
			if err != nil {
				rt.Fatalf("Failed to create property test: %v", err)
			}

			// Failure handling that will generate a reproduction test on failure
			defer func() {
				if rt.Failed() {
					testCode := GenerateReproductionTest(pt.ExecutedOps, "TestReproduction")

					fileName := "internal/workflow_tests/repro_test.go"
					if err := os.WriteFile(fileName, []byte(testCode), 0644); err == nil {
						_ = fileName
					} else {
						_ = err
					}
				}

				pt.Cleanup()
			}()

			opsJSON, _ := json.Marshal(ops)
			pt.WriteToLog("Operations: %s\n", opsJSON)
			pt.Log("Operations: %s", opsJSON)

			for _, op := range ops {
				pt.ExecuteOperation(op)
			}
		})
	})
}

func SetupTestEnvironment(t *testing.T) (*metastructure.Metastructure, *FakePlugin, func()) {
	t.Helper()

	fakePlugin := NewFakePlugin()
	overrides := &plugin.ResourcePluginOverrides{
		Create: fakePlugin.Create,
		Read:   fakePlugin.Read,
		Update: fakePlugin.Update,
		Delete: fakePlugin.Delete,
	}

	cfg := test_helpers.NewTestMetastructureConfig()
	cfg.Agent.Synchronization.Interval = 0 // 0 means sync is disabled
	cfg.Agent.Logging.ConsoleLogLevel = slog.LevelDebug
	cfg.Agent.Retry.StatusCheckInterval = 1
	cfg.Agent.Datastore.DatastoreType = pkgmodel.SqliteDatastore
	cfg.Agent.Datastore.Sqlite = pkgmodel.SqliteConfig{
		FilePath: t.TempDir() + "/formae.db",
	}

	db, err := datastore.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
	require.NoError(t, err)

	m, def, err := test_helpers.NewTestMetastructureWithEverything(t, overrides, db, cfg)
	require.NoError(t, err)

	return m, fakePlugin, def
}

func newTestForma(count int, seed string) *pkgmodel.Forma {
	resources := make([]pkgmodel.Resource, count)
	for i := 0; i < count; i++ {
		props := map[string]string{
			"foo": fmt.Sprintf("%s-val-%d", seed, i),
			"bar": fmt.Sprintf("%s-val-%d", seed, i+10),
		}
		propBytes, _ := json.Marshal(props)

		resources[i] = pkgmodel.Resource{
			Label:      fmt.Sprintf("%d", i),
			Type:       "FakeAWS::S3::Bucket",
			Properties: propBytes,
			Stack:      "test-stack",
			Target:     "test-target",

			// Simulate a schema for the resource
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "bar"},
			},
		}
	}

	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{
			{
				Label: "test-stack",
			},
		},
		Resources: resources,
		Targets: []pkgmodel.Target{{
			Label:  "test-target",
			Config: json.RawMessage(`{"region": "us-east-1", "type": "AWS"}`),
		}},
	}
}

type PropertyTest struct {
	T             *rapid.T    // Rapid test instance for property testing
	StandardT     *testing.T  // Standard test instance for assertions
	ExecutedOps   []Operation // Track operations that were actually executed
	Meta          *metastructure.Metastructure
	Plugin        *FakePlugin // Cloud fake plugin
	EnableOpsLog  bool        // Enable ops logging
	LogFile       *os.File    // Log file for operations
	LogPath       string      // Path to the log file
	OpSeq         int         // Current operation sequence number
	ResourceCount int         // Number of resources being tested
	cleanup       func()      // Cleanup function to be called on test completion
}

// NewPropertyTest creates a new property test environment
// It sets up a Metastructure instance with a fake plugin and a specified number of resources
// EnableOpsLog logs individual operations to a file for each  test execution
func NewPropertyTest(t *testing.T, rt *rapid.T, resourceCount int, enableOpsLog bool) (*PropertyTest, error) {
	m, fakePlugin, cleanup := SetupTestEnvironment(t)

	// Setup logging
	var logFile *os.File
	var logPath string
	var err error
	if enableOpsLog {
		logDir := "./testdata/chaos_logs"
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}
		timestamp := time.Now().Format("20060102-150405")
		logPath = filepath.Join(logDir, fmt.Sprintf("chaos-ops-%s.log", timestamp))

		logFile, err = os.Create(logPath)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("failed to create log file: %w", err)
		}
	}

	pt := &PropertyTest{
		T:             rt,
		StandardT:     t,
		Meta:          m,
		Plugin:        fakePlugin,
		LogFile:       logFile,
		LogPath:       logPath,
		ResourceCount: resourceCount,
		EnableOpsLog:  enableOpsLog,
		OpSeq:         0,
		cleanup:       cleanup,
	}

	return pt, nil
}

// NewReproductionTest creates a test environment for reproducing a specific test case
func NewReproductionTest(t *testing.T, resourceCount int) (*PropertyTest, error) {
	pt, err := NewPropertyTest(t, nil, resourceCount, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create: %w", err)
	}
	return pt, nil
}

func (pt *PropertyTest) Cleanup() {
	if pt.LogFile != nil {
		pt.LogFile.Close()
	}
	if pt.cleanup != nil {
		pt.cleanup()
	}
}

func (pt *PropertyTest) Log(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if pt.T != nil {
		// Property test mode
		pt.T.Logf("[rapid] %s", message)
	} else {
		// Reproduction test mode
		pt.StandardT.Logf("[repro] %s", message)
	}
}

func (pt *PropertyTest) WriteToLog(format string, args ...any) {
	if pt.EnableOpsLog != false && pt.LogFile != nil {
		fmt.Fprintf(pt.LogFile, format, args...)
	}
}

func (pt *PropertyTest) WaitForCommandSuccess(commandID string, clientId string) {
	var t require.TestingT
	if pt.T != nil {
		pt.T.Helper()
		t = pt.T
	} else {
		pt.StandardT.Helper()
		t = pt.StandardT
	}

	require.Eventually(
		t,
		func() bool {
			result, err := pt.Meta.ListFormaCommandStatus("id:"+commandID, clientId, 1)
			if err != nil {
				pt.Log("GetFormaCommandsStatus error: %v", err)
				return false
			}
			if len(result.Commands) == 0 {
				pt.Log("GetFormaCommandsStatus returned no commands for id:%s", commandID)
				return false
			}
			return result.Commands[0].State == "Success"
		},
		10*time.Second,
		500*time.Millisecond,
		"Command should eventually succeed.",
	)
}

// VerifyState performs a synchronization and verifies all resources are in sync with the cloud
func (pt *PropertyTest) VerifyState() {
	pt.Meta.ForceSync()
	pt.AssertAllInSync()
}

// Apply creates and applies resources to the system
func (pt *PropertyTest) Apply(resourceIds []int, applyMode pkgmodel.FormaApplyMode) {
	// Generate different property values for patch operations
	modSuffix := fmt.Sprintf("user-mod-%d", pt.OpSeq)
	if applyMode == pkgmodel.FormaApplyModePatch {
		// For patch, add a suffix to generate different values
		modSuffix = fmt.Sprintf("user-mod-%d-patch", pt.OpSeq)
	}

	forma := newTestForma(pt.ResourceCount, modSuffix)

	// Filter to only include the selected resources
	selectedResources := make([]pkgmodel.Resource, 0, len(resourceIds))
	for _, idx := range resourceIds {
		for _, resource := range forma.Resources {
			if resource.Label == fmt.Sprintf("%d", idx) {
				selectedResources = append(selectedResources, resource)
				break
			}
		}
	}
	forma.Resources = selectedResources

	clientId := util.NewID()
	cmdConfig := &config.FormaCommandConfig{
		Mode: applyMode,
	}

	// This accomidates soft reconcile mode, #247
	if cmdConfig.Mode == pkgmodel.FormaApplyModeReconcile {
		cmdConfig.Force = true
	}

	res, err := pt.Meta.ApplyForma(forma, cmdConfig, clientId)
	time.Sleep(100 * time.Millisecond)

	// Check for patch rejection (expected when stack doesn't exist yet)
	var patchRejected apimodel.FormaPatchRejectedError
	if errors.As(err, &patchRejected) {
		return
	}

	if err != nil {
		pt.Log("Apply forma error: %v", err)
	}

	cmdId := res.CommandID
	pt.WaitForCommandSuccess(cmdId, clientId)
	pt.AssertFormaInSync(forma)
}

// Destroy removes resources from the system
func (pt *PropertyTest) Destroy(resourceIds []int) {
	stack, err := pt.Meta.ExtractResources("stack:test-stack")
	if err != nil {
		pt.Log("Failed to get stack: %v", err)
		return
	}

	if stack == nil {
		pt.Log("No stacks exist, nothing to destroy")
		return
	}

	// Filter resources to destroy
	selectedResources := make([]pkgmodel.Resource, 0, len(resourceIds))
	for _, idx := range resourceIds {
		for _, resource := range stack.Resources {
			if resource.Label == fmt.Sprintf("%d", idx) {
				pt.Log("Selected resource for destruction: %s", resource.Label)
				selectedResources = append(selectedResources, resource)
				break
			}
		}
	}

	if len(selectedResources) == 0 {
		pt.Log("No matching resources found to destroy")
		return
	}

	destroyForma := &pkgmodel.Forma{
		Stacks:    stack.Stacks,
		Targets:   stack.Targets,
		Resources: selectedResources,
	}

	clientId := util.NewID()
	res, _ := pt.Meta.DestroyForma(destroyForma, &config.FormaCommandConfig{}, clientId)
	cmdId := res.CommandID
	pt.WaitForCommandSuccess(cmdId, clientId)

	pt.AssertResourcesDeleted(selectedResources)
}

func (pt *PropertyTest) ExecuteOperation(op Operation) {
	pt.OpSeq++
	op.SequenceNum = pt.OpSeq
	opJSON, _ := json.Marshal(op)
	pt.WriteToLog("[%s] Executing: %s\n", time.Now().Format(time.RFC3339Nano), opJSON)
	pt.Log("Executing: %s", opJSON)

	pt.ExecutedOps = append(pt.ExecutedOps, op)

	// Uncomment to illustrate a failure
	// if pt.OpSeq == 3 {
	// 	pt.T.Fatal("FORCED FAILURE ON OPERATION 3 FOR TESTING")
	// 	return
	// }

	switch op.Kind {
	case OpUserApply:
		pt.Apply(op.ResourceIds, op.ApplyMode)

	case OpUserDestroy:
		pt.Destroy(op.ResourceIds)

	case OpVerifyState:
		pt.VerifyState()

	case OpCloudModify:
		pt.Log("Placeholder for CloudModify operation")

	case OpCloudDelete:
		pt.Log("Placeholder for CloudDelete operation")
	}
}

// AssertFormaInSync verifies that forma resources are in sync with cloud state
func (pt *PropertyTest) AssertFormaInSync(forma *pkgmodel.Forma) {
	var t require.TestingT
	if pt.T != nil {
		pt.T.Helper()
		t = pt.T
	} else {
		pt.StandardT.Helper()
		t = pt.StandardT
	}

	require.Eventually(
		t,
		func() bool {
			for _, resource := range forma.Resources {
				nativeID := fmt.Sprintf("id-%s", resource.Label)
				cloudProps, exists := pt.Plugin.resourceState.Load(nativeID)
				if !exists {
					pt.Log("Resource %s missing in cloud", nativeID)
					return false
				}
				var sysProps, cloudPropsMap map[string]string
				if err := json.Unmarshal(resource.Properties, &sysProps); err != nil {
					pt.Log("Failed to unmarshal sysProps for %s: %v", nativeID, err)
					return false
				}
				if err := json.Unmarshal([]byte(cloudProps.(string)), &cloudPropsMap); err != nil {
					pt.Log("Failed to unmarshal cloudProps for %s: %v", nativeID, err)
					return false
				}
				if !reflect.DeepEqual(cloudPropsMap, sysProps) {
					pt.Log("Props mismatch for %s: sys=%v, cloud=%v", nativeID, sysProps, cloudPropsMap)
					return false
				}
			}
			return true
		},
		5*time.Second,
		500*time.Millisecond,
		"Resources in forma should eventually sync with cloud state",
	)
}

// AssertAllInSync verifies that all system resources are in sync with cloud state, and vice versa
func (pt *PropertyTest) AssertAllInSync() {
	var t require.TestingT
	if pt.T != nil {
		pt.T.Helper()
		t = pt.T
	} else {
		pt.StandardT.Helper()
		t = pt.StandardT
	}

	require.Eventually(
		t,
		func() bool {
			forma, err := pt.Meta.ExtractResources("stack:test-stack")

			// If the stack doesn't exist, we ignore the error and proceed to
			// assert for orphaned resources
			if err != nil && !strings.Contains(err.Error(), "not found") {
				pt.Log("GetStack error: %v", err)
				return false
			}

			// If the stack is gone, there should be no cloud resources either
			if forma == nil {
				empty := true
				pt.Plugin.resourceState.Range(func(key, value any) bool {
					pt.Log("Orphaned cloud resource: %v", key)
					empty = false
					return true
				})
				return empty
			}

			// Check all system resources exist in cloud and match
			for _, resource := range forma.Resources {
				nativeID := fmt.Sprintf("id-%s", resource.Label)
				cloudProps, exists := pt.Plugin.resourceState.Load(nativeID)
				if !exists {
					pt.Log("MISSING: Resource %s exists in system but not in cloud", nativeID)
					return false
				}

				var sysProps, cloudPropsMap map[string]string
				json.Unmarshal(resource.Properties, &sysProps)
				json.Unmarshal([]byte(cloudProps.(string)), &cloudPropsMap)
				if !reflect.DeepEqual(cloudPropsMap, sysProps) {
					pt.Log("MISMATCH: Resource %s properties don't match", nativeID)
					pt.Log("System: %v, Cloud: %v", sysProps, cloudProps)
					return false
				}
			}

			// Check no orphaned cloud resources
			orphaned := false
			pt.Plugin.resourceState.Range(func(key, value any) bool {
				nativeID := key.(string)
				resourceLabel := strings.TrimPrefix(nativeID, "id-")
				found := false
				for _, resource := range forma.Resources {
					if resource.Label == resourceLabel {
						found = true
						break
					}
				}
				if !found {
					pt.Log("ORPHANED: Resource %s exists in cloud but not in system", nativeID)
					orphaned = true
					return false
				}
				return true
			})
			return !orphaned
		},
		5*time.Second,
		500*time.Millisecond,
		"All resources should eventually sync with cloud state",
	)
}

func (pt *PropertyTest) AssertResourcesDeleted(resources []pkgmodel.Resource) {
	var t require.TestingT
	if pt.T != nil {
		pt.T.Helper()
		t = pt.T
	} else {
		pt.StandardT.Helper()
		t = pt.StandardT
	}

	require.Eventually(
		t,
		func() bool {
			for _, resource := range resources {
				nativeID := fmt.Sprintf("id-%s", resource.Label)
				if _, exists := pt.Plugin.resourceState.Load(nativeID); exists {
					pt.Log("Resource %s still exists, waiting for deletion", nativeID)
					return false
				}
			}
			return true
		},
		2*time.Second,
		500*time.Millisecond,
		"Resources should be deleted",
	)
}

type OperationKind int

const (
	OpUserApply OperationKind = iota
	OpUserDestroy
	OpVerifyState
	OpCloudModify
	OpCloudDelete
)

func (k OperationKind) String() string {
	switch k {
	case OpUserApply:
		return "UserApply"
	case OpUserDestroy:
		return "UserDestroy"
	case OpVerifyState:
		return "VerifyState"
	case OpCloudModify:
		return "CloudModify"
	case OpCloudDelete:
		return "CloudDelete"
	default:
		return "Unknown"
	}
}

type Operation struct {
	Kind        OperationKind
	ResourceIds []int
	SequenceNum int
	ApplyMode   pkgmodel.FormaApplyMode
}

func (o Operation) MarshalJSON() ([]byte, error) {
	type Alias Operation
	return json.Marshal(struct {
		Kind        string `json:"Kind"`
		ApplyMode   string `json:"ApplyMode,omitempty"`
		ResourceIds []int  `json:"ResourceIds"`
		*Alias
	}{
		Kind:        o.Kind.String(),
		ApplyMode:   string(o.ApplyMode),
		ResourceIds: o.ResourceIds,
		Alias:       (*Alias)(&o),
	})
}

// OperationGen generates a random operation for the chaos test
func OperationGen(resourceCount int) *rapid.Generator[Operation] {
	return rapid.Custom(func(t *rapid.T) Operation {
		// Omit CloudModify and CloudDelete for now
		kind := rapid.IntRange(0, int(OpVerifyState)).Draw(t, "kind")

		// On apply, randomly choose between patch and replace mode
		var applyMode pkgmodel.FormaApplyMode
		if kind == int(OpUserApply) {
			mode := rapid.IntRange(0, 1).Draw(t, "applyMode")
			if mode == 0 {
				applyMode = pkgmodel.FormaApplyModePatch
			} else {
				applyMode = pkgmodel.FormaApplyModeReconcile
			}
		}

		// Generate affected resource indices based on operation type
		var resourceIds []int
		switch OperationKind(kind) {
		case OpUserApply, OpUserDestroy:
			allIndices := make([]int, resourceCount)
			for i := 0; i < resourceCount; i++ {
				allIndices[i] = i
			}

			// Shuffle the indices
			for i := resourceCount - 1; i > 0; i-- {
				j := rapid.IntRange(0, i).Draw(t, "shuffle")
				allIndices[i], allIndices[j] = allIndices[j], allIndices[i]
			}
			count := rapid.IntRange(1, resourceCount).Draw(t, "applyCount")
			resourceIds = make([]int, count)
			copy(resourceIds, allIndices[:count])

		// For single-resource operations, select one random resource
		case OpCloudModify, OpCloudDelete:
			idx := rapid.IntRange(0, resourceCount-1).Draw(t, "resourceIdx")
			resourceIds = []int{idx}
		}

		return Operation{
			Kind:        OperationKind(kind),
			ResourceIds: resourceIds,
			SequenceNum: 0, // Set by the test during execution
			ApplyMode:   applyMode,
		}
	})
}

type FakePlugin struct {
	// resourceState fakes a cloud provider's DB of resources
	resourceState sync.Map

	// resourceMutexes tracks locking for each cloud resource
	resourceMutexes sync.Map
}

func NewFakePlugin() *FakePlugin {
	return &FakePlugin{}
}

// getMutex returns the mutex for a resource, creating it if needed
func (p *FakePlugin) getMutex(resourceType, resourceLabel string) *sync.Mutex {
	mKey := resourceType + ":" + resourceLabel
	mRaw, _ := p.resourceMutexes.LoadOrStore(mKey, &sync.Mutex{})
	return mRaw.(*sync.Mutex)
}

func (p *FakePlugin) Create(request *resource.CreateRequest) (*resource.CreateResult, error) {
	mutex := p.getMutex(request.DesiredState.Type, request.DesiredState.Label)
	mutex.Lock()
	defer mutex.Unlock()

	nativeID := fmt.Sprintf("id-%s", request.DesiredState.Label)
	p.resourceState.Store(nativeID, string(request.DesiredState.Properties))

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusSuccess,
			RequestID:       request.DesiredState.Label,
			NativeID:        nativeID,
			ResourceType:    request.DesiredState.Type,
		},
	}, nil
}

func (p *FakePlugin) Read(request *resource.ReadRequest) (*resource.ReadResult, error) {
	nativeID := request.NativeID
	val, exists := p.resourceState.Load(nativeID)
	if !exists {
		return nil, fmt.Errorf("resource not found")
	}

	var jsonCheck map[string]any
	if err := json.Unmarshal([]byte(val.(string)), &jsonCheck); err != nil {
		// If we can't unmarshal to a map, it's not a valid JSON object
		return nil, fmt.Errorf("invalid resource properties format: %v", err)
	}

	return &resource.ReadResult{
		ResourceType: request.ResourceType,
		Properties:   val.(string),
	}, nil
}

func (p *FakePlugin) Update(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	mutex := p.getMutex(request.DesiredState.Type, request.DesiredState.Label)
	mutex.Lock()
	defer mutex.Unlock()

	nativeID := fmt.Sprintf("id-%s", request.DesiredState.Label)

	existingPropsRaw, exists := p.resourceState.Load(nativeID)
	if !exists {
		return nil, fmt.Errorf("resource %s not found for update", nativeID)
	}

	var existingProps map[string]any
	if err := json.Unmarshal([]byte(existingPropsRaw.(string)), &existingProps); err != nil {
		return nil, fmt.Errorf("failed to parse existing properties: %v", err)
	}

	patchStr := *request.PatchDocument

	// Check if patch document - RFC6902 (patch starts with '[')
	if strings.HasPrefix(strings.TrimSpace(patchStr), "[") {
		var patches []map[string]any
		if err := json.Unmarshal([]byte(patchStr), &patches); err == nil {
			for _, patch := range patches {
				op, okOp := patch["op"].(string)
				path, okPath := patch["path"].(string)
				value, okValue := patch["value"]

				if okOp && okPath && okValue {
					switch op {
					case "replace", "add":
						fieldName := strings.TrimPrefix(path, "/")
						existingProps[fieldName] = value
					// Prepare for future operations
					case "remove":
						fieldName := strings.TrimPrefix(path, "/")
						delete(existingProps, fieldName)
					}
				}
			}

			updatedProps, err := json.Marshal(existingProps)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal updated properties: %v", err)
			}
			patchStr = string(updatedProps)
		} else {
		}
	}

	p.resourceState.Store(nativeID, patchStr)

	return &resource.UpdateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationUpdate,
			OperationStatus:    resource.OperationStatusSuccess,
			RequestID:          request.DesiredState.Label,
			ResourceProperties: []byte(patchStr),
			NativeID:           nativeID,
			ResourceType:       request.DesiredState.Type,
		},
	}, nil
}

func (p *FakePlugin) Delete(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	nativeID := *request.NativeID
	resourceLabel := strings.TrimPrefix(nativeID, "id-")

	mutex := p.getMutex(request.ResourceType, resourceLabel)
	mutex.Lock()
	defer mutex.Unlock()

	_, exists := p.resourceState.Load(nativeID)
	result := &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusSuccess,
			RequestID:       nativeID, // Critical field
			NativeID:        nativeID, // Critical field
			ResourceType:    request.ResourceType,
		},
	}

	if exists {
		p.resourceState.Delete(nativeID)
	} else {
	}

	return result, nil
}

// SimulateCloudMods randomly modifies a resource's properties
func (p *FakePlugin) SimulateCloudMods(t *testing.T, iteration int) {
	p.resourceState.Range(func(key, value any) bool {
		if iteration%3 == 0 {
			nativeID := key.(string)
			resourceLabel := strings.TrimPrefix(nativeID, "id-")

			mutex := p.getMutex("FakeAWS::S3::Bucket", resourceLabel)
			mutex.Lock()
			defer mutex.Unlock()

			oldProps := value.(string)
			var propsMap map[string]string

			// Try to unmarshal, but if we fail simply create new properties
			if err := json.Unmarshal([]byte(oldProps), &propsMap); err != nil {

				fixedProps := map[string]string{
					"foo": fmt.Sprintf("fixed-cloud-%d", iteration),
					"bar": fmt.Sprintf("fixed-cloud-%d", iteration+10),
				}
				newProps, _ := json.Marshal(fixedProps)
				p.resourceState.Store(nativeID, string(newProps))

				return true
			}

			updatedProps := make(map[string]string)
			for k, v := range propsMap {
				updatedProps[k] = v
			}
			updatedProps["foo"] = fmt.Sprintf("cloud-manual-%d", iteration)
			updatedProps["modified"] = "true"

			newProps, _ := json.Marshal(updatedProps)
			p.resourceState.Store(nativeID, string(newProps))
		}
		return true
	})
}

// SimulateCloudDeletion deletes a specific resource
func (p *FakePlugin) SimulateCloudDeletion(t *testing.T, targetLabel string) {
	targetID := fmt.Sprintf("id-%s", targetLabel)

	mutex := p.getMutex("FakeAWS::S3::Bucket", targetLabel)
	mutex.Lock()
	defer mutex.Unlock()

	p.resourceState.Delete(targetID)
}

// GenerateReproductionTest generates a standalone test file to reproduce a sequence of operations
func GenerateReproductionTest(ops []Operation, testName string) string {
	var sb strings.Builder

	// Add pkg header / imports
	sb.WriteString("// Generated reproduction test\n\n")
	sb.WriteString("package workflow_tests\n\n")
	sb.WriteString("import (\n")
	sb.WriteString("\t\"testing\"\n")
	sb.WriteString("\t\"github.com/platform-engineering-labs/formae/internal/metastructure/testutil\"\n")
	sb.WriteString(")\n\n")

	// Generate the test function
	sb.WriteString(fmt.Sprintf("func %s(t *testing.T) {\n", testName))
	sb.WriteString("\ttestutil.RunTestFromProjectRoot(t, func(t *testing.T) {\n")
	sb.WriteString("\t\tpt, err := NewReproductionTest(t, 5)\n")
	sb.WriteString("\t\tif err != nil {\n")
	sb.WriteString("\t\t\tt.Fatalf(\"Failed to create test environment: %v\", err)\n")
	sb.WriteString("\t\t}\n")
	sb.WriteString("\t\tdefer pt.Cleanup()\n\n")

	// Add each operation
	for i, op := range ops {
		sb.WriteString(fmt.Sprintf("\t\t// Operation %d: %s\n", i+1, op.Kind.String()))

		switch op.Kind {
		case OpUserApply:
			sb.WriteString(fmt.Sprintf("\t\tpt.Apply([]int{%s}, \"%s\")\n",
				intSliceToString(op.ResourceIds), op.ApplyMode))

		case OpUserDestroy:
			sb.WriteString(fmt.Sprintf("\t\tpt.Destroy([]int{%s})\n",
				intSliceToString(op.ResourceIds)))

		case OpVerifyState:
			sb.WriteString("\t\tpt.VerifyState()\n")
		}

		sb.WriteString("\n")
	}

	sb.WriteString("\t})\n")
	sb.WriteString("}\n")

	return sb.String()
}

func intSliceToString(slice []int) string {
	strs := make([]string, len(slice))
	for i, n := range slice {
		strs[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(strs, ", ")
}
