# Agent Crash Injection Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Move the blackbox test harness from running the agent in-process to launching it as an external subprocess, then add a crash injection operation (`OpCrashAgent`) that kills the agent with SIGKILL and restarts it to test recovery.

**Architecture:** The harness launches the formae binary as a subprocess with a PKL config file pointing at a SQLite database. The test plugin is spawned by the agent (child process). On crash (SIGKILL), both die. On restart, the harness waits for readiness, reconnects Ergo, re-injects cloud state + response queue, and opens the plugin gate. The plugin gate is a `chan struct{}` that blocks all CRUD methods until the harness sends `OpenGate`.

**Tech Stack:** Go, Ergo actor framework, `os/exec`, `syscall.Kill`, PKL config, REST API polling

**Design doc:** `/Users/jeroen/dev/personal/engineering-notes/formae/testing/design/2026-03-09-agent-crash-injection-design.md`

---

### Task 1: Add Plugin Gate to Test Plugin

The gate is a `chan struct{}` that blocks all CRUD methods until closed. On every plugin startup, the gate starts closed (blocking). The harness sends `OpenGate` via TestController to close it, unblocking all CRUD calls.

**Files:**
- Modify: `tests/testplugin/main.go`
- Modify: `tests/testplugin/test_controller.go`
- Modify: `tests/testplugin/plugin.go` (CRUD methods)
- Modify: `tests/testcontrol/messages.go`
- Modify: `tests/testcontrol/register.go`

**Step 1: Add OpenGate message types to testcontrol**

In `tests/testcontrol/messages.go`, add:

```go
// OpenGateRequest asks the TestController to open the plugin gate,
// unblocking all CRUD operations.
type OpenGateRequest struct{}

// OpenGateResponse is the reply to OpenGateRequest.
type OpenGateResponse struct{}
```

In `tests/testcontrol/register.go`, add `OpenGateRequest{}` and `OpenGateResponse{}` to the EDF registration list.

**Step 2: Add gate channel to test plugin shared state**

In `tests/testplugin/main.go`, create the gate channel and pass it through:

```go
gate := make(chan struct{}) // starts closed (blocking)

// Add to Env map:
gen.Env("Gate"): gate,
```

Pass `gate` to `TestPlugin`:

```go
p := &TestPlugin{
    cloudState:    cs,
    injections:    inj,
    responseQueue: rq,
    opLog:         ol,
    gate:          gate,
}
```

**Step 3: Add gate field and wait to TestPlugin CRUD methods**

In the `TestPlugin` struct, add `gate <-chan struct{}`. At the top of every CRUD method (`Create`, `Read`, `Update`, `Delete`, `List`), add:

```go
<-tp.gate // block until gate is opened
```

**Step 4: Handle OpenGate in TestController**

In `tests/testplugin/test_controller.go`, add a `gate chan struct{}` field. In `Init()`, read it from env:

```go
g, ok := tc.Env("Gate")
if !ok {
    return fmt.Errorf("TestController: missing 'Gate' environment variable")
}
tc.gate = g.(chan struct{})
```

In `HandleCall`, add:

```go
case testcontrol.OpenGateRequest:
    close(tc.gate)
    return testcontrol.OpenGateResponse{}, nil
```

**Step 5: Run existing tests to confirm gate doesn't break anything**

The gate must be opened by the harness after plugin registration. Since the current harness doesn't send `OpenGate` yet, we need to add it to the existing `NewTestHarness` before the tests will pass. Add a call at the end of `NewTestHarness` (after `startErgoNode`):

```go
h.OpenGate(t)
```

And add the `OpenGate` method to the harness:

```go
func (h *TestHarness) OpenGate(t *testing.T) {
    t.Helper()
    _, err := h.callTestController(testcontrol.OpenGateRequest{})
    require.NoError(t, err, "OpenGate failed")
}
```

Run: `go test -C ./tests/blackbox -tags=property -run TestProperty_SequentialHappyPath -rapid.steps=5 -count=1 -v`
Expected: PASS

**Step 6: Commit**

```
feat(testplugin): add plugin gate to block CRUD until harness signals readiness
```

---

### Task 2: Build External Agent Process Manager

Replace the in-process agent (metastructure + HTTP server) with an external subprocess. The formae binary is built once per test function. A PKL config file is generated with a dynamic port, SQLite path, fixed nodename, and disabled sync/discovery.

**Files:**
- Modify: `tests/blackbox/harness.go`

**Step 1: Add agent process management to TestHarness**

Replace the in-process fields with subprocess fields:

```go
type TestHarness struct {
    t          *testing.T
    client     *api.Client
    cancel     context.CancelFunc
    cfg        *pkgmodel.Config  // keep for config values (port, cookie, nodename)
    pluginsDir string

    // External agent process
    agentCmd     *exec.Cmd
    agentBinary  string
    agentDataDir string
    agentLogFile *os.File
    configPath   string

    // Ergo node for TestController communication
    ergoNode       gen.Node
    pluginNodeName gen.Atom
}
```

Remove imports:
- `internal/metastructure`
- `internal/datastore/sqlite`
- `internal/logging`
- `internal/api.NewServer`
- `internal/metastructure/util`

Keep imports:
- `internal/api.Client` and `internal/api.NewClient`
- `pkg/api/model`
- `pkg/model`
- `pkg/plugin.RegisterSharedEDFTypes`
- `tests/testcontrol`

**Step 2: Add BuildAgent function**

Add a package-level function that builds the formae binary once per test function (use `sync.Once` or `testing.B` caching — simplest is build in `TestMain`):

```go
// buildFormae compiles the formae binary to a temp directory. Called once per
// test binary via TestMain.
func buildFormae(t *testing.T) string {
    t.Helper()
    dir := t.TempDir()
    binary := filepath.Join(dir, "formae")
    cmd := exec.Command("go", "build", "-o", binary, "./cmd/formae")
    output, err := cmd.CombinedOutput()
    require.NoError(t, err, "Failed to build formae: %s", string(output))
    return binary
}
```

Since we need the binary path across tests, use a package-level variable set in `TestMain`:

```go
var formaeBinary string

func TestMain(m *testing.M) {
    // Build formae binary once
    tmpDir, err := os.MkdirTemp("", "formae-blackbox-*")
    if err != nil {
        log.Fatalf("failed to create temp dir: %v", err)
    }
    defer os.RemoveAll(tmpDir)

    formaeBinary = filepath.Join(tmpDir, "formae")
    cmd := exec.Command("go", "build", "-o", formaeBinary, "./cmd/formae")
    cmd.Dir = projectRoot() // need to be in module root
    output, err := cmd.CombinedOutput()
    if err != nil {
        log.Fatalf("Failed to build formae: %s\n%v", string(output), err)
    }

    os.Exit(m.Run())
}
```

Note: `testutil.RunTestFromProjectRoot` already handles running from project root. The `TestMain` approach avoids rebuilding the binary for every test function.

**Step 3: Write PKL config generator**

Create a helper that writes a PKL config file for the agent:

```go
func writePKLConfig(t *testing.T, dir string, port int, dbPath, nodename, cookie, pluginsDir string) string {
    t.Helper()
    configPath := filepath.Join(dir, "formae.conf.pkl")
    content := fmt.Sprintf(`amends "formae:/Config.pkl"

agent {
    server {
        port = %d
        nodename = %q
        secret = %q
    }
    datastore {
        datastoreType = "sqlite"
        sqlite {
            filePath = %q
        }
    }
    synchronization {
        enabled = false
    }
    discovery {
        enabled = false
    }
    stackExpirer {
        interval = 24.h
    }
    retry {
        maxRetries = 2
        retryDelay = 250.ms
        statusCheckInterval = 1.s
    }
    pluginsDir = %q
}
`, port, nodename, cookie, dbPath, pluginsDir)
    err := os.WriteFile(configPath, []byte(content), 0644)
    require.NoError(t, err)
    return configPath
}
```

Note: Check whether `pluginsDir` is a valid PKL config field. If not, use the `FORMAE_PLUGINS_DIR` environment variable when starting the process. Look at `pkg/model/config.go` for the actual config structure to ensure field names match.

**Step 4: Rewrite NewTestHarness to launch external process**

```go
func NewTestHarness(t *testing.T, timeout time.Duration) *TestHarness {
    t.Helper()

    pluginsDir := t.TempDir()
    buildTestPluginToDir(t, pluginsDir)

    dataDir := t.TempDir()
    port := getFreePort(t)
    dbPath := filepath.Join(dataDir, "formae.db")
    nodename := "agent-bb-" + randomString(10)
    cookie := randomString(32)

    configPath := writePKLConfig(t, dataDir, port, dbPath, nodename, cookie, pluginsDir)

    // Compute plugin node name (same formula as plugin process supervisor)
    pluginNodeName := gen.Atom(fmt.Sprintf("%s-%s-plugin@localhost",
        nodename, strings.ToLower("Test")))

    h := &TestHarness{
        t:              t,
        agentBinary:    formaeBinary,
        agentDataDir:   dataDir,
        configPath:     configPath,
        pluginsDir:     pluginsDir,
        pluginNodeName: pluginNodeName,
        // Store config values we need later
        cfg: &pkgmodel.Config{
            Agent: pkgmodel.AgentConfig{
                Server: pkgmodel.ServerConfig{
                    Nodename: nodename,
                    Hostname: "localhost",
                    Port:     port,
                    Secret:   cookie,
                },
            },
        },
    }

    h.startAgent(t, timeout)
    return h
}
```

**Step 5: Implement startAgent**

```go
func (h *TestHarness) startAgent(t *testing.T, timeout time.Duration) {
    t.Helper()

    ctx, cancel := context.WithCancel(context.Background())
    h.cancel = cancel

    cmd := exec.CommandContext(ctx, h.agentBinary, "agent", "start", "--config", h.configPath)
    cmd.Env = os.Environ()
    cmd.Env = append(cmd.Env, fmt.Sprintf("FORMAE_PID_FILE=%s", filepath.Join(h.agentDataDir, "formae.pid")))

    logFile, err := os.Create(filepath.Join(h.agentDataDir, "agent.log"))
    require.NoError(t, err)
    cmd.Stdout = logFile
    cmd.Stderr = logFile
    h.agentLogFile = logFile

    err = cmd.Start()
    require.NoError(t, err, "Failed to start agent")
    h.agentCmd = cmd

    // Create API client
    h.client = api.NewClient(pkgmodel.APIConfig{
        URL:  "http://localhost",
        Port: h.cfg.Agent.Server.Port,
    }, nil, nil)

    // Poll health endpoint
    h.waitForHealth(t, timeout)

    // Poll stats endpoint until plugin is registered
    h.waitForPlugin(t, timeout)

    // Start Ergo node and connect to TestController
    h.startErgoNode(t)

    // Open the plugin gate
    h.OpenGate(t)
}
```

**Step 6: Implement waitForHealth and waitForPlugin**

```go
func (h *TestHarness) waitForHealth(t *testing.T, timeout time.Duration) {
    t.Helper()
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/v1/health", h.cfg.Agent.Server.Port))
        if err == nil && resp.StatusCode == http.StatusOK {
            resp.Body.Close()
            return
        }
        if resp != nil {
            resp.Body.Close()
        }
        time.Sleep(200 * time.Millisecond)
    }
    t.Fatalf("agent health check failed after %v", timeout)
}

func (h *TestHarness) waitForPlugin(t *testing.T, timeout time.Duration) {
    t.Helper()
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        stats, err := h.client.Stats()
        if err == nil && stats != nil && len(stats.Plugins) > 0 {
            t.Logf("Plugin registered: %v", stats.Plugins)
            return
        }
        time.Sleep(200 * time.Millisecond)
    }
    t.Fatalf("plugin did not register within %v", timeout)
}
```

**Step 7: Rewrite Cleanup for external process**

```go
func (h *TestHarness) Cleanup() {
    if h.ergoNode != nil {
        h.ergoNode.Stop()
    }
    h.stopAgent()
}

func (h *TestHarness) stopAgent() {
    if h.agentCmd == nil || h.agentCmd.Process == nil {
        return
    }
    // SIGTERM for graceful shutdown
    _ = h.agentCmd.Process.Signal(syscall.SIGTERM)
    done := make(chan error, 1)
    go func() { done <- h.agentCmd.Wait() }()
    select {
    case <-done:
    case <-time.After(5 * time.Second):
        h.cancel()
        <-done
    }
    if h.agentLogFile != nil {
        h.agentLogFile.Close()
    }
    // Dump log on failure
    if h.t.Failed() {
        logPath := filepath.Join(h.agentDataDir, "agent.log")
        if data, err := os.ReadFile(logPath); err == nil {
            h.t.Logf("=== Agent log ===\n%s\n=== End ===", string(data))
        }
    }
}
```

**Step 8: Remove internal imports**

Delete:
- `startMetastructure` function
- `setupLogCapture` function
- `newTestConfig` function (replaced by `writePKLConfig`)
- All references to `m *metastructure.Metastructure`, `logCapture`, `dssqlite`, `logging`, `metastructure`, `util` imports

The `randomString` helper currently comes from `internal/metastructure/util`. Replace with a local implementation or `crypto/rand`:

```go
func randomString(n int) string {
    const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
    b := make([]byte, n)
    for i := range b {
        b[i] = letters[rand.IntN(len(letters))]
    }
    return string(b)
}
```

**Step 9: Run existing tests**

Run: `go test -C ./tests/blackbox -tags=property -run TestProperty_SequentialHappyPath -rapid.steps=5 -count=1 -v`
Expected: PASS

The existing tests should pass because the API client interface is identical — only the agent startup mechanism changed.

**Step 10: Commit**

```
feat(blackbox): replace in-process agent with external subprocess
```

---

### Task 3: Add KillAgent and RestartAgent to TestHarness

These are the building blocks for crash injection. `KillAgent` sends SIGKILL. `RestartAgent` starts a new subprocess with the same config and re-establishes TestController communication.

**Files:**
- Modify: `tests/blackbox/harness.go`

**Step 1: Implement KillAgent**

```go
// KillAgent sends SIGKILL to the agent process, killing it and its child plugin
// immediately. Waits for the process to be reaped.
func (h *TestHarness) KillAgent(t *testing.T) {
    t.Helper()
    require.NotNil(t, h.agentCmd, "agent not running")
    require.NotNil(t, h.agentCmd.Process, "agent process not started")

    t.Logf("Killing agent (pid %d)", h.agentCmd.Process.Pid)
    err := syscall.Kill(h.agentCmd.Process.Pid, syscall.SIGKILL)
    require.NoError(t, err, "SIGKILL failed")

    // Reap the process
    _ = h.agentCmd.Wait()

    // Disconnect Ergo node (the remote node is gone)
    if h.ergoNode != nil {
        h.ergoNode.Stop()
        h.ergoNode = nil
    }

    h.agentCmd = nil
}
```

**Step 2: Implement RestartAgent**

```go
// RestartAgent starts a new agent subprocess with the same config (same SQLite DB).
// Waits for health + plugin readiness, reconnects Ergo, and opens the gate.
func (h *TestHarness) RestartAgent(t *testing.T, timeout time.Duration) {
    t.Helper()
    h.startAgent(t, timeout)
}
```

This reuses `startAgent` which already handles:
1. Starting the subprocess
2. Creating the API client
3. Polling health
4. Polling stats for plugin
5. Starting Ergo node
6. Opening gate

**Step 3: Implement ReinjjectState**

After restart, the plugin has lost all cloud state and response queue. The harness must re-program them:

```go
// ReinjectState sends the full cloud state and all programmed response
// sequences to the newly started plugin's TestController.
func (h *TestHarness) ReinjectState(t *testing.T, model *StateModel, pendingSequences []testcontrol.PluginOpSequence) {
    t.Helper()

    // Re-inject cloud state from the state model
    for _, stack := range model.Stacks {
        for _, res := range stack.Resources {
            if res.State == StateExists && res.NativeID != "" {
                h.PutCloudState(t, res.NativeID, res.ResourceType, res.Properties)
            }
        }
    }
    // Also re-inject OOB cloud resources tracked by the model
    for _, entry := range model.CloudOnlyResources {
        h.PutCloudState(t, entry.NativeID, entry.ResourceType, entry.Properties)
    }

    // Re-program all response sequences for pending commands
    if len(pendingSequences) > 0 {
        h.ProgramResponses(t, pendingSequences)
    }
}
```

Note: The exact shape of this depends on what the StateModel tracks. The cloud state entries need NativeID, ResourceType, and Properties. Check the StateModel to see if it already tracks enough info. If not, the harness needs to maintain a separate cloud state mirror. This will need to be adapted during implementation based on what's available.

**Step 4: Run existing tests (no crash injection yet, just ensure Kill/Restart compile)**

Run: `go test -C ./tests/blackbox -tags=property -run TestProperty_SequentialHappyPath -rapid.steps=5 -count=1 -v`
Expected: PASS (Kill/Restart not called yet, just compiled)

**Step 5: Commit**

```
feat(blackbox): add KillAgent and RestartAgent methods to harness
```

---

### Task 4: Add OpCrashAgent to the Generator

Add the crash injection operation kind, the config flag, and the generator logic.

**Files:**
- Modify: `tests/blackbox/operations.go`
- Modify: `tests/blackbox/generators.go`

**Step 1: Add OpCrashAgent to operations.go**

In the `OperationKind` const block, add after `OpVerifyState`:

```go
// Crash injection

OpCrashAgent // kill the agent with SIGKILL and restart it
```

Add `EnableCrashInjection bool` to `PropertyTestConfig`.

**Step 2: Add generator logic in generators.go**

In `allowedKinds`, add:

```go
if config.EnableCrashInjection {
    kinds = append(kinds, OpCrashAgent)
}
```

The design says OpCrashAgent should be drawn at most once per iteration and not as the first or last operation. This constraint can be enforced in `OperationSequenceGen` after drawing the ops:

```go
// Post-process: ensure OpCrashAgent appears at most once and not at edges
crashCount := 0
for i := range ops {
    if ops[i].Kind == OpCrashAgent {
        crashCount++
        if crashCount > 1 || i == 0 || i == len(ops)-1 {
            // Replace excess/edge crashes with OpVerifyState
            ops[i].Kind = OpVerifyState
        }
    }
}
```

**Step 3: Run tests (no executor for OpCrashAgent yet)**

Run: `go test -C ./tests/blackbox -tags=property -run TestProperty_SequentialHappyPath -rapid.steps=5 -count=1 -v`
Expected: PASS (EnableCrashInjection is false so OpCrashAgent won't be drawn)

**Step 4: Commit**

```
feat(blackbox): add OpCrashAgent operation kind and generator support
```

---

### Task 5: Implement OpCrashAgent Executor

Wire up the executor to kill the agent, restart it, and re-inject state.

**Files:**
- Modify: `tests/blackbox/executor.go`

**Step 1: Add case for OpCrashAgent in ExecuteOperation**

In the `ExecuteOperation` method's switch on `op.Kind`, add:

```go
case OpCrashAgent:
    h.executeCrashAgent(t, model)
```

**Step 2: Implement executeCrashAgent**

```go
func (h *TestHarness) executeCrashAgent(t *testing.T, model *StateModel) {
    t.Helper()
    t.Logf(">>> OpCrashAgent: killing agent")

    // Kill the agent (SIGKILL)
    h.KillAgent(t)

    // Restart with same config (same SQLite DB persists state)
    h.RestartAgent(t, 30*time.Second)

    // Re-inject cloud state from model
    h.reinjectCloudState(t, model)

    // Re-program response sequences for all pending commands
    h.reinjectResponseSequences(t, model)

    t.Logf(">>> OpCrashAgent: agent restarted and state re-injected")
}
```

**Step 3: Implement reinjectCloudState**

This needs to iterate over the state model and call PutCloudState for every resource that should exist in the cloud. The exact implementation depends on how the StateModel tracks cloud-side state. Look at:
- How `OpCloudCreate`/`OpCloudModify` update the model
- How `AssertAllInvariants` reads cloud state
- What the state model tracks about native IDs and properties

```go
func (h *TestHarness) reinjectCloudState(t *testing.T, model *StateModel) {
    t.Helper()
    // Iterate model's expected cloud state and re-inject
    // Implementation depends on StateModel's cloud state tracking
}
```

**Step 4: Implement reinjectResponseSequences**

For pending commands, re-program all drawn outcomes from the start. Completed commands won't be re-executed by `ReRunIncompleteCommands`, so extra responses go unconsumed (harmless).

The model's `PendingCommands` (or equivalent) tracks accepted commands. Each command's `DrawnOutcomes` map was converted to `[]testcontrol.PluginOpSequence` when programming. We need to re-send all of them.

The simplest approach: the harness maintains a running list of all programmed sequences (appended on each command submission). On crash, re-send the entire list. The ResponseQueue in the new plugin starts empty, so this effectively replays all programming.

Add a field to TestHarness:

```go
// allProgrammedSequences accumulates every sequence programmed during this iteration.
// On crash, the entire list is re-sent to the new plugin.
allProgrammedSequences []testcontrol.PluginOpSequence
```

Modify `ProgramResponses` to also append to this list (when sequences is non-nil).

On crash:

```go
func (h *TestHarness) reinjectResponseSequences(t *testing.T, model *StateModel) {
    t.Helper()
    if len(h.allProgrammedSequences) > 0 {
        h.ProgramResponses(t, h.allProgrammedSequences)
    }
}
```

Note: `ProgramResponses(t, nil)` clears the queue (used in ResetAgentState). The accumulator should also be cleared there.

**Step 5: Run tests without crash injection to confirm no regression**

Run: `go test -C ./tests/blackbox -tags=property -run TestProperty_SequentialHappyPath -rapid.steps=5 -count=1 -v`
Expected: PASS

**Step 6: Commit**

```
feat(blackbox): implement OpCrashAgent executor with state re-injection
```

---

### Task 6: Add Crash Injection Property Test

Add a new property test that enables crash injection.

**Files:**
- Modify: `tests/blackbox/property_test.go`

**Step 1: Add TestProperty_CrashRecovery**

```go
func TestProperty_CrashRecovery(t *testing.T) {
    testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
        h := NewTestHarness(t, 30*time.Second)
        defer h.Cleanup()

        rapid.Check(t, func(rt *rapid.T) {
            config := PropertyTestConfig{
                ResourceCount:        10,
                OperationCount:       Range{Min: 5, Max: 15},
                StackCount:           2,
                EnableFailures:       true,
                EnableCrashInjection: true,
            }

            h.ResetAgentState(t)
            model := NewStateModel(config.StackCount, config.ResourceCount)
            ops := OperationSequenceGen(config).Draw(rt, "ops")

            for i, op := range ops {
                op.SequenceNum = i
                h.ExecuteOperation(t, &op, model)
            }

            h.DrainPendingCommands(t, model, 30*time.Second)
            h.AssertAllInvariants(t, model)
        })
    })
}
```

Note the longer timeouts: crash recovery adds restart overhead.

**Step 2: Run the crash recovery test**

Run: `go test -C ./tests/blackbox -tags=property -run TestProperty_CrashRecovery -rapid.steps=3 -count=1 -v -timeout=120s`

Expected: PASS (but may need debugging — see Task 7)

**Step 3: Commit**

```
feat(blackbox): add crash recovery property test
```

---

### Task 7: Debug and Stabilize

This is a placeholder task for issues that will likely surface during implementation. Expected areas:

1. **PKL config field names**: The PKL config schema may use different field names than the Go struct. Check `formae:/Config.pkl` or an existing config file for exact syntax. The e2e test at `tests/e2e/go/agent.go` has a working example.

2. **Plugin node name formula**: The formula `{nodename}-{namespace}-plugin@{hostname}` must exactly match what the agent's plugin process supervisor uses. Verify by checking `internal/metastructure/` for the plugin spawn code.

3. **Ergo node reconnection**: After killing the agent, the harness Ergo node may have stale connection state. Stopping and restarting the harness Ergo node (as done in `KillAgent` + `startAgent`) should handle this, but may need connection cleanup.

4. **ResetAgentState after crash**: `ResetAgentState` calls `ProgramResponses(t, nil)` to clear queues. After switching to external process, ensure this still works (it communicates via Ergo, not internal state). Also clear `allProgrammedSequences`.

5. **Timeout tuning**: Agent startup with PKL parsing + plugin build may take longer than the current 10s timeout. The e2e tests use 30s. Adjust `NewTestHarness` timeout and the restart timeout accordingly.

6. **Process group / child reaping**: SIGKILL to the agent should also kill the plugin (child process). Verify this on Darwin. If not, may need to kill the process group: `syscall.Kill(-pid, SIGKILL)`. The agent likely sets the plugin as a child process that gets SIGKILL'd when the parent dies (via `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` or prctl on Linux). On macOS, child processes don't automatically die with the parent — check if the test plugin monitors its parent and exits, or if we need `Setpgid` + group kill.

7. **DrainPendingCommands tolerance**: After a crash, some commands may have been lost (submitted but not yet persisted). The drain phase needs to handle the case where a command ID tracked by the model no longer exists in the agent's database. Check if `WaitForCommandDone` handles 404s gracefully.

No commit for this task — fixes are committed as they're made.

---

### Task 8: Update ResetAgentState for External Process

`ResetAgentState` currently uses `ProgramResponses(nil)` to clear queues and `ExtractResources` + `DestroyForma` via the API client to clean up. This should still work with an external agent. However, we need to:

**Files:**
- Modify: `tests/blackbox/executor.go`

**Step 1: Clear the programmed sequences accumulator**

In `ResetAgentState`, at the top (alongside `ProgramResponses(t, nil)`):

```go
h.allProgrammedSequences = nil
```

**Step 2: Handle crash state in ResetAgentState**

If the agent was killed and not restarted (shouldn't happen in normal flow, but defensive), restart it before cleanup:

```go
if h.agentCmd == nil {
    h.RestartAgent(t, 30*time.Second)
}
```

**Step 3: Run full test suite**

Run: `go test -C ./tests/blackbox -tags=property -rapid.steps=5 -count=1 -v -timeout=300s`
Expected: All tests PASS

**Step 4: Commit**

```
fix(blackbox): handle crash state in ResetAgentState
```
