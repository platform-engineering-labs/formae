// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// nonTerminalCommandPredicate matches a command that has not reached a final
// state. The column is NOT NULL on every backend; a NULL state is still read as
// non-terminal rather than trusted. qualifier is the table alias with its dot,
// or "".
func nonTerminalCommandPredicate(qualifier string) string {
	return fmt.Sprintf("(%[1]sstate IS NULL OR %[1]sstate NOT IN ('%[2]s','%[3]s','%[4]s'))", qualifier,
		forma_command.CommandStateSuccess, forma_command.CommandStateFailed, forma_command.CommandStateCanceled)
}

// ResumableCommandPredicate is the single definition of a command that startup
// recovery owns: LoadIncompleteFormaCommands selects with it on every backend,
// and FailOrphanedCommands finalizes the non-terminal commands it does not
// match. Making another state resumable here therefore also takes it out of
// the sweep. qualifier is the table alias with its dot, or "".
func ResumableCommandPredicate(qualifier string) string {
	return fmt.Sprintf("%[1]scommand != '%[2]s' AND %[1]sstate IN ('%[3]s','%[4]s')", qualifier,
		pkgmodel.CommandSync, forma_command.CommandStateNotStarted, forma_command.CommandStateInProgress)
}

// conflictingCommandPredicate is the single definition of a command that
// blocks admission: any non-terminal command except read-only synchronization
// and discovery.
var conflictingCommandPredicate = nonTerminalCommandPredicate("fc.") + `
AND NOT (fc.command='sync' AND COALESCE(fc.source,'') IN ('synchronizer','discovery'))`

// orphanedCommandPredicate selects every non-terminal command that startup
// recovery never resumes or finalizes. The terminal guard stays separate from
// the negated resumability rule so that widening the rule can never reach a
// finished command, and a NULL state is named explicitly because it makes the
// negation NULL rather than true.
var orphanedCommandPredicate = nonTerminalCommandPredicate("") + `
AND (state IS NULL OR NOT (` + ResumableCommandPredicate("") + `))`

// MaxListedConflictingCommands bounds ConflictingCommandIDsForStacks, and so
// how many commands a rejection names.
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

// ConflictingCommandIDsForStacks is the admission check: it returns the
// commands that block a new command on these stacks, at most
// MaxListedConflictingCommands of them, sorted by ID. It checks command state
// rather than resource state, so the last-resource-terminal/command-nonterminal
// interval remains protected. Read-only synchronization and discovery commands
// are excluded. The caller rejects on a non-empty answer and reports these same
// IDs, so the rejection always names what it observed.
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
	for start := 0; start < len(labels) && len(seen) < MaxListedConflictingCommands; start += conflictLabelsPerQuery {
		end := min(start+conflictLabelsPerQuery, len(labels))
		clause, args := conflictStackClause(labels[start:end])
		rows, err := tx.QueryAll(s.limit(`SELECT fc.command_id FROM forma_commands fc
WHERE `+conflictingCommandPredicate+`
AND `+clause+`
ORDER BY fc.command_id`, MaxListedConflictingCommands), args...)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			seen[row[0]] = true
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
	// Resource updates first: the second statement changes what the predicate
	// in the first one selects.
	if _, err = tx.ExecAffected(`UPDATE resource_updates SET state='Failed'
WHERE (state IS NULL OR state NOT IN ('Success','Failed','Rejected','Canceled'))
AND command_id IN (SELECT command_id FROM forma_commands WHERE ` + orphanedCommandPredicate + `)`); err != nil {
		return 0, err
	}
	count, err := tx.ExecAffected(`UPDATE forma_commands SET state='Failed' WHERE ` + orphanedCommandPredicate)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int(count), nil
}
