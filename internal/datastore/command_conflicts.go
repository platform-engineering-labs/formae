// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// HasConflictingCommandForStacks checks command state rather than resource
// state, so the last-resource-terminal/command-nonterminal interval remains
// protected. Read-only synchronization and discovery commands are excluded.
func (s AdmissionStore) HasConflictingCommandForStacks(stackLabels []string) (bool, error) {
	labels := append([]string(nil), stackLabels...)
	sort.Strings(labels)
	labels = slices.Compact(labels)
	if len(labels) == 0 {
		return false, nil
	}
	if len(labels) > MaxAdmissionGuards {
		return false, fmt.Errorf("%w: too many command stack labels", ErrInvalidAdmission)
	}
	tx, err := s.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	const labelsPerQuery = 400
	for start := 0; start < len(labels); start += labelsPerQuery {
		end := min(start+labelsPerQuery, len(labels))
		chunk := labels[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		query := s.first(`SELECT CAST(1 AS VARCHAR(1)) FROM forma_commands fc
WHERE (fc.state IS NULL OR fc.state NOT IN ('Success','Failed','Canceled'))
AND NOT (fc.command='sync' AND COALESCE(fc.source,'') IN ('synchronizer','discovery'))
AND (EXISTS (SELECT 1 FROM command_stacks cs WHERE cs.command_id=fc.command_id AND cs.stack_label IN (` + placeholders + `))
 OR EXISTS (SELECT 1 FROM resource_updates ru WHERE ru.command_id=fc.command_id AND ru.stack_label IN (` + placeholders + `)))`)
		args := make([]any, 0, len(chunk)*2)
		for _, label := range chunk {
			args = append(args, label)
		}
		for _, label := range chunk {
			args = append(args, label)
		}
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
