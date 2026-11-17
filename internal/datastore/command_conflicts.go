// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// conflictingCommandPredicate is the single definition of a command that
// blocks admission: any non-terminal command except read-only synchronization
// and discovery. The check that rejects a command and the query that names the
// commands in the rejection both use it, so they cannot disagree.
const conflictingCommandPredicate = `(fc.state IS NULL OR fc.state NOT IN ('Success','Failed','Canceled'))
AND NOT (fc.command='sync' AND COALESCE(fc.source,'') IN ('synchronizer','discovery'))`

// orphanedCommandPredicate selects every non-terminal command that
// LoadIncompleteFormaCommands does not return, and that startup recovery
// therefore never resumes or finalizes. It must stay the exact complement of
// that method's filter among non-terminal commands.
const orphanedCommandPredicate = `(state IS NULL OR state NOT IN ('Success','Failed','Canceled'))
AND (command='sync' OR state IS NULL OR state NOT IN ('NotStarted','InProgress'))`

// MaxListedConflictingCommands bounds ConflictingCommandIDsForStacks.
const MaxListedConflictingCommands = 20

const conflictLabelsPerQuery = 400

func normalizeConflictLabels(stackLabels []string) ([]string, error) {
	labels := append([]string(nil), stackLabels...)
	sort.Strings(labels)
	labels = slices.Compact(labels)
	if len(labels) > MaxAdmissionGuards {
		return nil, fmt.Errorf("%w: too many command stack labels", ErrInvalidAdmission)
	}
	return labels, nil
}

// conflictStackClause matches commands that touch one of the chunk's stacks
// through membership or resource updates. Its placeholders bind the chunk twice.
func conflictStackClause(chunk []string) (string, []any) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
	clause := `(EXISTS (SELECT 1 FROM command_stacks cs WHERE cs.command_id=fc.command_id AND cs.stack_label IN (` + placeholders + `))
 OR EXISTS (SELECT 1 FROM resource_updates ru WHERE ru.command_id=fc.command_id AND ru.stack_label IN (` + placeholders + `)))`
	args := make([]any, 0, len(chunk)*2)
	for range 2 {
		for _, label := range chunk {
			args = append(args, label)
		}
	}
	return clause, args
}

// HasConflictingCommandForStacks checks command state rather than resource
// state, so the last-resource-terminal/command-nonterminal interval remains
// protected. Read-only synchronization and discovery commands are excluded.
func (s AdmissionStore) HasConflictingCommandForStacks(stackLabels []string) (bool, error) {
	labels, err := normalizeConflictLabels(stackLabels)
	if err != nil {
		return false, err
	}
	if len(labels) == 0 {
		return false, nil
	}
	tx, err := s.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	for start := 0; start < len(labels); start += conflictLabelsPerQuery {
		end := min(start+conflictLabelsPerQuery, len(labels))
		clause, args := conflictStackClause(labels[start:end])
		query := s.first(`SELECT CAST(1 AS VARCHAR(1)) FROM forma_commands fc
WHERE ` + conflictingCommandPredicate + `
AND ` + clause)
		row, err := tx.Query(query, args...)
		if err != nil {
			return false, err
		}
		if row != nil {
			if err = tx.Commit(); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

// ConflictingCommandIDsForStacks names the commands that make
// HasConflictingCommandForStacks true, at most MaxListedConflictingCommands of
// them, sorted by ID.
func (s AdmissionStore) ConflictingCommandIDsForStacks(stackLabels []string) ([]string, error) {
	labels, err := normalizeConflictLabels(stackLabels)
	if err != nil {
		return nil, err
	}
	if len(labels) == 0 {
		return nil, nil
	}
	tx, err := s.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	seen := make(map[string]bool)
	for start := 0; start < len(labels); start += conflictLabelsPerQuery {
		end := min(start+conflictLabelsPerQuery, len(labels))
		clause, stackArgs := conflictStackClause(labels[start:end])
		// AdmissionTransaction.Query answers one row, so page by command ID.
		query := s.first(`SELECT fc.command_id FROM forma_commands fc
WHERE ` + conflictingCommandPredicate + `
AND fc.command_id > ?
AND ` + clause + `
ORDER BY fc.command_id`)
		cursor := ""
		for range MaxListedConflictingCommands {
			row, err := tx.Query(query, append([]any{cursor}, stackArgs...)...)
			if err != nil {
				return nil, err
			}
			if row == nil {
				break
			}
			cursor = row[0]
			seen[cursor] = true
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > MaxListedConflictingCommands {
		ids = ids[:MaxListedConflictingCommands]
	}
	return ids, nil
}

// FailOrphanedCommands marks Failed every non-terminal command that startup
// recovery does not own, together with its unfinished resource updates, and
// returns how many commands it finalized. Nothing else ever finishes these
// rows: an interrupted sync is never resumed, and legacy states are never
// loaded. Call it only while no command can be executing, i.e. at startup
// before the actors run.
func (s AdmissionStore) FailOrphanedCommands() (int, error) {
	tx, err := s.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := tx.Query(`SELECT CAST(COUNT(*) AS VARCHAR(20)) FROM forma_commands WHERE ` + orphanedCommandPredicate)
	if err != nil {
		return 0, err
	}
	count := 0
	if row != nil {
		if count, err = strconv.Atoi(row[0]); err != nil {
			return 0, err
		}
	}
	if count > 0 {
		if err = tx.Exec(`UPDATE resource_updates SET state='Failed'
WHERE (state IS NULL OR state NOT IN ('Success','Failed','Rejected','Canceled'))
AND command_id IN (SELECT command_id FROM forma_commands WHERE ` + orphanedCommandPredicate + `)`); err != nil {
			return 0, err
		}
		if err = tx.Exec(`UPDATE forma_commands SET state='Failed' WHERE ` + orphanedCommandPredicate); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
