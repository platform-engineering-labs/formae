# RFC: Recovery for Orphaned InProgress Commands

## Problem

When a ResourceUpdater actor crashes (e.g., due to an unhandled message), the command remains stuck in `InProgress` state indefinitely. The user has no way to recover:

- `formae cancel` fails because it tries to message a dead actor
- `formae apply` is rejected because the command appears to still be running
- The only workaround is direct SQL manipulation

## Options

### 1. Force Cancel CLI Flag

Add `formae cancel --force` that bypasses the actor system and directly updates the database.

**Pros:** Explicit user action, no magic, user decides when to intervene.

**Cons:** Requires user to recognize the situation and know about the flag.

### 2. Supervisor Health Check

Periodically verify that actors exist for all InProgress updates. Mark orphaned ones as Failed.

**Pros:** Automatic recovery without user intervention.

**Cons:** May mask underlying bugs. Jeroen raised concerns about this approach hiding systematic issues that should be investigated.

### 3. Command Timeout

Commands in InProgress state beyond a threshold (e.g., 24 hours) automatically transition to Failed.

**Pros:** Simple to implement, provides a ceiling on stuck commands.

**Cons:** Arbitrary threshold. Legitimate long-running operations could be falsely terminated.

### 4. Startup Recovery

On agent restart, scan for orphaned InProgress commands and transition them to a Retryable or Failed state.

**Pros:** Natural recovery point, no background polling, clear audit moment.

**Cons:** Requires agent restart to recover. Doesn't help if agent stays running.

## Recommendation

Option 1 (Force Cancel) as the immediate solution. It's explicit, gives users an escape hatch, and doesn't mask bugs.

Option 4 (Startup Recovery) as a complementary measure for cases where the agent restarts anyway.

## Implementation Notes

For `--force` cancel:

```go
if forceFlag {
    datastore.UpdateCommandState(commandID, "Canceled")
    datastore.UpdateResourceUpdatesForCommand(commandID, "InProgress", "Canceled")
} else {
    // existing actor-based cancel
}
```
