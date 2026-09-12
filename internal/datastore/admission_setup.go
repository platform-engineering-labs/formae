// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package datastore

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Metadata identity reads are certified by the mandatory coarse domain guards.
// Their write locks are already held before this helper is entered; ordinary
// metadata writers acquire the same locks through database triggers.
func (s AdmissionStore) setupCommand(tx AdmissionTransaction, c *forma_command.FormaCommand, guards []RevisionGuard) error {
	n := len(c.StackUpdates) + len(c.PolicyUpdates) + len(c.GeneratorUpdates)
	if n > MaxAdmissionMetadataOperations {
		return fmt.Errorf("%w: too many setup operations", ErrInvalidAdmission)
	}
	if n > 0 {
		have := map[string]bool{}
		for _, g := range guards {
			have[g.Key] = true
		}
		for _, key := range []string{AdmissionStackMappingGuard, AdmissionPolicyGuard, AdmissionGeneratorGuard} {
			if !have[key] {
				return fmt.Errorf("%w: metadata setup requires guard %s", ErrInvalidAdmission, key)
			}
		}
	}
	stacks := map[string]string{}
	deletedStacks := map[string]bool{}
	for _, st := range c.Stacks {
		if prev, ok := stacks[st.Label]; ok && prev != st.ID {
			return fmt.Errorf("%w: ambiguous stack identity", ErrAdmissionConflict)
		}
		stacks[st.Label] = st.ID
	}
	// Verify unchanged memberships only when metadata uses them. Empty metadata
	// commands may legitimately cover historical/deleted inventory memberships.
	stackID := func(label, pinned string) (string, error) {
		id := stacks[label]
		if id == "" || (pinned != "" && pinned != id) {
			return "", fmt.Errorf("%w: missing admitted stack identity for %s", ErrAdmissionConflict, label)
		}
		if deletedStacks[label] {
			return id, nil
		}
		row, err := tx.Query(s.first("SELECT id,operation FROM stacks WHERE label=? ORDER BY version DESC"), label)
		if err != nil {
			return "", err
		}
		if row == nil || row[0] != id || row[1] == "delete" {
			return "", fmt.Errorf("%w: stack incarnation changed for %s", ErrAdmissionConflict, label)
		}
		return id, nil
	}
	for i := range c.StackUpdates {
		u := &c.StackUpdates[i]
		row, err := tx.Query(s.first("SELECT id,version,operation FROM stacks WHERE label=? ORDER BY version DESC"), u.Stack.Label)
		if err != nil {
			return err
		}
		previous := ""
		if row != nil {
			previous = row[1]
		}
		if u.Operation == stack_update.StackOperationCreate {
			if row != nil && row[2] != "delete" {
				return fmt.Errorf("%w: stack exists", ErrAdmissionConflict)
			}
			if u.Stack.ID == "" {
				u.Stack.ID = stacks[u.Stack.Label]
			}
			if u.Stack.ID == "" {
				u.Stack.ID = mksuid.New().String()
			}
			if pinned := stacks[u.Stack.Label]; pinned != "" && pinned != u.Stack.ID {
				return fmt.Errorf("%w: stack identity mismatch", ErrAdmissionConflict)
			}
			history, err := tx.Query(s.first("SELECT id FROM stacks WHERE id=?"), u.Stack.ID)
			if err != nil {
				return err
			}
			if history != nil {
				return fmt.Errorf("%w: stack ID already used", ErrAdmissionConflict)
			}
			stacks[u.Stack.Label] = u.Stack.ID
			found := false
			for j := range c.Stacks {
				if c.Stacks[j].Label == u.Stack.Label {
					c.Stacks[j].ID = u.Stack.ID
					found = true
				}
			}
			if !found {
				c.Stacks = append(c.Stacks, forma_command.CommandStack{ID: u.Stack.ID, Label: u.Stack.Label})
			}
		} else {
			if u.Operation != stack_update.StackOperationUpdate && u.Operation != stack_update.StackOperationDelete {
				return fmt.Errorf("%w: unsupported stack operation", ErrInvalidAdmission)
			}
			if _, err := stackID(u.Stack.Label, u.Stack.ID); err != nil {
				return err
			}
			if u.Stack.ID == "" || (u.ExistingStack != nil && u.ExistingStack.ID != u.Stack.ID) {
				return fmt.Errorf("%w: stack identity required", ErrAdmissionConflict)
			}
		}
		if err := setupVersion(&u.Version, previous); err != nil {
			return err
		}
		if u.Operation == stack_update.StackOperationDelete {
			if err := s.deleteSetupPolicies(tx, u.Stack.ID, c); err != nil {
				return err
			}
		}
		if err := tx.Exec("INSERT INTO stacks(id,version,command_id,operation,label,description) VALUES (?,?,?,?,?,?)", u.Stack.ID, u.Version, c.ID, string(u.Operation), u.Stack.Label, u.Stack.Description); err != nil {
			return err
		}
		if u.Operation == stack_update.StackOperationDelete {
			deletedStacks[u.Stack.Label] = true
		}
		u.State = stack_update.StackUpdateStateSuccess
		u.ErrorMessage = ""
		u.ModifiedTs = util.TimeNow()
	}
	createdPolicies := map[string]string{}
	for i := range c.PolicyUpdates {
		u := &c.PolicyUpdates[i]
		if u.Operation == policy_update.PolicyOperationSkip {
			u.State = policy_update.PolicyUpdateStateSuccess
			u.ModifiedTs = util.TimeNow()
			continue
		}
		if u.StackLabel == "" && u.StackID != "" {
			return fmt.Errorf("%w: inline policy requires admitted stack label", ErrAdmissionConflict)
		}
		if u.StackLabel != "" {
			id, err := stackID(u.StackLabel, u.StackID)
			if err != nil {
				return err
			}
			u.StackID = id
		}
		if deletedStacks[u.StackLabel] && u.Operation != policy_update.PolicyOperationDelete && u.Operation != policy_update.PolicyOperationDetach {
			return fmt.Errorf("%w: policy mutation on deleted stack", ErrAdmissionConflict)
		}
		label := u.PolicyRef
		if u.Policy != nil {
			label = u.Policy.GetLabel()
		}
		scope := u.StackID
		junction := u.Operation == policy_update.PolicyOperationAttach || u.Operation == policy_update.PolicyOperationDetach
		if junction {
			scope = ""
			label = u.PolicyRef
			if u.PolicyID == "" {
				u.PolicyID = createdPolicies[label]
			}
		}
		row, err := tx.Query(s.first(`SELECT id,version,policy_type FROM (SELECT id,version,label,policy_type,operation,ROW_NUMBER() OVER(PARTITION BY id ORDER BY version DESC) rn FROM policies WHERE COALESCE(stack_id,'')=?) p WHERE rn=1 AND operation!='delete' AND label=? ORDER BY version DESC`), scope, label)
		if err != nil {
			return err
		}
		previous := ""
		if row == nil && u.Operation == policy_update.PolicyOperationDelete && u.PolicyID != "" && deletedStacks[u.StackLabel] {
			tombstone, e := tx.Query(s.first("SELECT version,command_id,operation FROM policies WHERE id=? AND stack_id=? ORDER BY version DESC"), u.PolicyID, u.StackID)
			if e != nil {
				return e
			}
			if tombstone != nil && tombstone[1] == c.ID && tombstone[2] == "delete" {
				if u.Version != "" && u.Version != tombstone[0] {
					return fmt.Errorf("%w: cascade result version mismatch", ErrAdmissionConflict)
				}
				u.Version = tombstone[0]
				u.State = policy_update.PolicyUpdateStateSuccess
				u.ModifiedTs = util.TimeNow()
				continue
			}
		}
		if u.Operation == policy_update.PolicyOperationCreate {
			if row != nil || u.Policy == nil {
				return fmt.Errorf("%w: policy already exists or missing", ErrAdmissionConflict)
			}
			if u.PolicyID == "" {
				u.PolicyID = mksuid.New().String()
			}
			history, err := tx.Query(s.first("SELECT id FROM policies WHERE id=?"), u.PolicyID)
			if err != nil {
				return err
			}
			if history != nil {
				return fmt.Errorf("%w: policy ID already used", ErrAdmissionConflict)
			}
		} else {
			if row == nil || u.PolicyID == "" || row[0] != u.PolicyID || (u.ExpectedVersion != "" && row[1] != u.ExpectedVersion) {
				return fmt.Errorf("%w: policy incarnation changed", ErrAdmissionConflict)
			}
			previous = row[1]
		}
		if junction {
			if u.Version != "" && u.Version != previous {
				return fmt.Errorf("%w: pinned attachment policy version changed", ErrAdmissionConflict)
			}
			if u.StackID == "" {
				return fmt.Errorf("%w: attachment stack required", ErrInvalidAdmission)
			}
			if u.Operation == policy_update.PolicyOperationDetach {
				err = tx.Exec("DELETE FROM stack_policies WHERE stack_id=? AND policy_id=?", u.StackID, u.PolicyID)
			} else {
				existing, e := tx.Query("SELECT policy_id FROM stack_policies WHERE stack_id=? AND policy_id=?", u.StackID, u.PolicyID)
				if e != nil {
					return e
				}
				if existing == nil {
					err = tx.Exec("INSERT INTO stack_policies(stack_id,policy_id) VALUES (?,?)", u.StackID, u.PolicyID)
				}
			}
			if err != nil {
				return err
			}
			u.Version = previous
		} else {
			if u.Operation != policy_update.PolicyOperationCreate && u.Operation != policy_update.PolicyOperationUpdate && u.Operation != policy_update.PolicyOperationDelete {
				return fmt.Errorf("%w: unsupported policy operation", ErrInvalidAdmission)
			}
			if err := setupVersion(&u.Version, previous); err != nil {
				return err
			}
			data := []byte(`{}`)
			typ := ""
			if row != nil {
				typ = row[2]
			}
			if u.Operation != policy_update.PolicyOperationDelete {
				if u.Policy == nil {
					return fmt.Errorf("%w: policy required", ErrInvalidAdmission)
				}
				u.Policy.SetStackID(scope)
				typ = u.Policy.GetType()
				switch p := u.Policy.(type) {
				case *pkgmodel.TTLPolicy:
					data, err = json.Marshal(TTLPolicyData(p))
				case *pkgmodel.AutoReconcilePolicy:
					data, err = json.Marshal(map[string]any{"IntervalSeconds": p.IntervalSeconds})
				default:
					return fmt.Errorf("%w: unsupported policy type", ErrInvalidAdmission)
				}
				if err != nil {
					return err
				}
			}
			if err := tx.Exec("INSERT INTO policies(id,version,command_id,operation,label,policy_type,stack_id,policy_data) VALUES (?,?,?,?,?,?,?,?)", u.PolicyID, u.Version, c.ID, string(u.Operation), label, typ, scope, string(data)); err != nil {
				return err
			}
			if scope == "" && u.Operation == policy_update.PolicyOperationCreate {
				createdPolicies[label] = u.PolicyID
			}
		}
		u.State = policy_update.PolicyUpdateStateSuccess
		u.ErrorMessage = ""
		u.ModifiedTs = util.TimeNow()
	}
	for i := range c.GeneratorUpdates {
		u := &c.GeneratorUpdates[i]
		if deletedStacks[u.StackLabel] && u.Operation != generator_update.GeneratorOperationDelete {
			return fmt.Errorf("%w: generator mutation on deleted stack", ErrAdmissionConflict)
		}
		g := u.Generator
		if g == nil {
			g = u.ExistingGenerator
			u.Generator = g
		}
		if g == nil {
			return fmt.Errorf("%w: generator required", ErrInvalidAdmission)
		}
		id, err := stackID(u.StackLabel, g.GetStackID())
		if err != nil {
			return err
		}
		g.SetStackID(id)
		label := g.GetLabel()
		if u.ExistingGenerator != nil {
			label = u.ExistingGenerator.GetLabel()
		}
		row, err := tx.Query(s.first(`SELECT id,version,generation_id,generation_spec FROM (SELECT id,version,label,operation,generation_id,generation_spec,ROW_NUMBER() OVER(PARTITION BY id ORDER BY version DESC) rn FROM generators WHERE stack_id=?) g WHERE rn=1 AND operation!='delete' AND label=? ORDER BY version DESC`), id, label)
		if err != nil {
			return err
		}
		previous, generation, spec := "", "", "{}"
		if u.Operation == generator_update.GeneratorOperationCreate {
			if row != nil {
				return fmt.Errorf("%w: generator exists", ErrAdmissionConflict)
			}
			if g.GetID() == "" {
				g.SetID(mksuid.New().String())
			}
			history, err := tx.Query(s.first("SELECT id FROM generators WHERE id=?"), g.GetID())
			if err != nil {
				return err
			}
			if history != nil {
				return fmt.Errorf("%w: generator ID already used", ErrAdmissionConflict)
			}
		} else {
			if u.Operation != generator_update.GeneratorOperationUpdate && u.Operation != generator_update.GeneratorOperationDelete {
				return fmt.Errorf("%w: unsupported generator operation", ErrInvalidAdmission)
			}
			if row == nil || g.GetID() == "" || row[0] != g.GetID() {
				return fmt.Errorf("%w: generator incarnation changed", ErrAdmissionConflict)
			}
			if u.ExistingGenerator != nil && (u.ExistingGenerator.GetID() != g.GetID() || u.ExistingGenerator.GetStackID() != id) {
				return fmt.Errorf("%w: existing generator identity mismatch", ErrAdmissionConflict)
			}
			previous, generation, spec = row[1], row[2], row[3]
		}
		if label != g.GetLabel() {
			collision, err := tx.Query(s.first(`SELECT id FROM (SELECT id,label,operation,ROW_NUMBER() OVER(PARTITION BY id ORDER BY version DESC) rn FROM generators WHERE stack_id=?) g WHERE rn=1 AND operation!='delete' AND label=?`), id, g.GetLabel())
			if err != nil {
				return err
			}
			if collision != nil && collision[0] != g.GetID() {
				return fmt.Errorf("%w: generator rename collision", ErrAdmissionConflict)
			}
		}
		if err := setupVersion(&u.Version, previous); err != nil {
			return err
		}
		data, err := GeneratorData(g)
		if err != nil {
			return err
		}
		if u.Operation == generator_update.GeneratorOperationDelete {
			data = []byte(`{}`)
		}
		if err := tx.Exec("INSERT INTO generators(id,version,command_id,operation,label,generator_type,stack_id,generator_data,generation_id,generation_spec) VALUES (?,?,?,?,?,?,?,?,?,?)", g.GetID(), u.Version, c.ID, string(u.Operation), g.GetLabel(), g.GetType(), id, string(data), generation, spec); err != nil {
			return err
		}
		u.State = generator_update.GeneratorUpdateStateSuccess
		u.ErrorMessage = ""
		u.ModifiedTs = util.TimeNow()
	}
	c.Setup = &forma_command.SetupBoundary{Version: 1, Committed: true}
	if !c.HasExecutableChanges() {
		c.State = forma_command.CommandStateSuccess
	}
	return nil
}

func (s AdmissionStore) first(q string) string {
	// Match authoritative readers and Go KSUID ordering, regardless of locale.
	if s.Dialect == "postgres" {
		q = strings.ReplaceAll(q, "ORDER BY version", `ORDER BY version COLLATE "C"`)
	}
	if s.Dialect == "mssql" {
		q = strings.ReplaceAll(q, "ORDER BY version", "ORDER BY version COLLATE Latin1_General_BIN2")
	}
	if s.Dialect == "mssql" {
		return "SELECT TOP (1) " + q[len("SELECT "):]
	}
	return q + " LIMIT 1"
}
func setupVersion(version *string, previous string) error {
	if *version == "" {
		*version = mksuid.New().String()
	}
	if *version <= previous {
		return fmt.Errorf("%w: setup version must follow current version", ErrAdmissionConflict)
	}
	return nil
}
func (s AdmissionStore) deleteSetupPolicies(tx AdmissionTransaction, stackID string, command *forma_command.FormaCommand) error {
	commandID := command.ID
	if err := tx.Exec("DELETE FROM stack_policies WHERE stack_id=?", stackID); err != nil {
		return err
	}
	for i := 0; i <= MaxAdmissionMetadataOperations; i++ {
		row, err := tx.Query(s.first(`SELECT id,version,label,policy_type FROM (SELECT id,version,label,policy_type,operation,ROW_NUMBER() OVER(PARTITION BY id ORDER BY version DESC) rn FROM policies WHERE stack_id=?) p WHERE rn=1 AND operation!='delete' ORDER BY id`), stackID)
		if err != nil {
			return err
		}
		if row == nil {
			return nil
		}
		if i == MaxAdmissionMetadataOperations {
			return fmt.Errorf("%w: too many cascade policies", ErrInvalidAdmission)
		}
		version := ""
		for _, u := range command.PolicyUpdates {
			if u.Operation == policy_update.PolicyOperationDelete && u.PolicyID == row[0] {
				if u.ExpectedVersion != "" && u.ExpectedVersion != row[1] {
					return fmt.Errorf("%w: cascade policy version changed", ErrAdmissionConflict)
				}
				version = u.Version
			}
		}
		if err := setupVersion(&version, row[1]); err != nil {
			return err
		}
		if err := tx.Exec("INSERT INTO policies(id,version,command_id,operation,label,policy_type,stack_id,policy_data) VALUES (?,?,?,'delete',?,?,?,'{}')", row[0], version, commandID, row[2], row[3], stackID); err != nil {
			return err
		}
	}
	return nil
}
