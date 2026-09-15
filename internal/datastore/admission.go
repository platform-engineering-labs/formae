// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
)

// MaxAdmissionReceiptBytes leaves metadata headroom below Aurora Data API
// 64 KiB returned-row limit. Keep the same replay-safe contract on all backends.
const MaxAdmissionReceiptBytes = 48 * 1024

// MaxAdmissionGuards accommodates the supported 20,000-resource inventory even
// when every resource has a distinct stack and target: label + incarnation +
// resource identity + target inventory = 80,000 guards, plus domain guards and
// 20,000 scopes of headroom. This is a unique-scope memory/work budget, not a SQL
// statement size: AdmissionStore locks each key in sorted order with <=3 binds
// and reads one small row at a time, including through the Aurora Data API.
const MaxAdmissionGuards = 100_000

// MaxAdmissionMetadataOperations counts metadata separately from resource work.
// Twenty thousand stacks,
// policies and generators fit without restricting the existing inventory scale.
const MaxAdmissionMetadataOperations = 100_000

var (
	ErrInvalidAdmission  = errors.New("invalid command admission")
	ErrStaleAdmission    = errors.New("command admission revisions changed")
	ErrAdmissionConflict = errors.New("idempotency key already used for a different request")
)

type RevisionGuard struct {
	Key      string
	Revision int64
}
type CommandAdmission struct {
	Guards                                        []RevisionGuard
	PrincipalScope, IdempotencyKey, RequestDigest string
	Receipt                                       json.RawMessage
}
type StoredAdmission struct {
	CommandID, RequestDigest string
	Receipt                  json.RawMessage
}
type AdmissionResult struct {
	StoredAdmission
	Replayed bool
	// Command is the authoritative committed snapshot, present only for a new admission.
	Command *forma_command.FormaCommand
}

// CommandAdmitter is an internal protocol under construction. Do not integrate
// runtime callers until every authoritative writer invalidates its guards.
// PrincipalScope must come from trusted authentication, never client metadata.
type CommandAdmitter interface {
	ReadAdmissionRevisions(keys []string) ([]RevisionGuard, error)
	LookupCommandAdmission(principalScope, key string) (*StoredAdmission, error)
	AdmitFormaCommand(command *forma_command.FormaCommand, admission CommandAdmission) (AdmissionResult, error)
}

func validateAdmissionIdentity(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && strings.TrimSpace(value) == value
}

// CanonicalAdmissionGuards copies, sorts and deduplicates equal revisions.
// Identity is case sensitive. Limits are UTF-8 bytes, conservative for SQL Server
// NVARCHAR indexes. Keys must not contain NUL or surrounding whitespace.
func CanonicalAdmissionGuards(guards []RevisionGuard) ([]RevisionGuard, error) {
	out := append([]RevisionGuard(nil), guards...)
	for _, g := range out {
		if !validateAdmissionIdentity(g.Key, 450) || g.Revision < 0 {
			return nil, fmt.Errorf("%w: invalid revision guard", ErrInvalidAdmission)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	n := 0
	for _, g := range out {
		if n > 0 && out[n-1].Key == g.Key {
			if out[n-1].Revision != g.Revision {
				return nil, fmt.Errorf("%w: contradictory revisions for %q", ErrInvalidAdmission, g.Key)
			}
			continue
		}
		out[n] = g
		n++
	}
	return out[:n], nil
}

// NormalizeCommandAdmission owns its returned slices. Digests are lowercase
// SHA-256 hex; receipt must be a non-null JSON object, at most 48 KiB. Callers
// choose the receipt schema and must not include secrets. Guard count is bounded
// by MaxAdmissionGuards after deduplication; admission requires at least one guard.
func NormalizeCommandAdmission(a CommandAdmission) (CommandAdmission, error) {
	if !validateAdmissionIdentity(a.PrincipalScope, 200) || !validateAdmissionIdentity(a.IdempotencyKey, 200) {
		return a, fmt.Errorf("%w: principal and key must be 1..200 bytes without surrounding whitespace or NUL", ErrInvalidAdmission)
	}
	digest, err := hex.DecodeString(a.RequestDigest)
	if err != nil || len(digest) != 32 || strings.ToLower(a.RequestDigest) != a.RequestDigest {
		return a, fmt.Errorf("%w: digest must be lowercase SHA-256 hex", ErrInvalidAdmission)
	}
	var receipt map[string]json.RawMessage
	if len(a.Receipt) > MaxAdmissionReceiptBytes || json.Unmarshal(a.Receipt, &receipt) != nil || receipt == nil {
		return a, fmt.Errorf("%w: receipt must be a JSON object of at most 48 KiB", ErrInvalidAdmission)
	}
	a.Guards, err = CanonicalAdmissionGuards(a.Guards)
	if err != nil {
		return a, err
	}
	if len(a.Guards) == 0 || len(a.Guards) > MaxAdmissionGuards {
		return a, fmt.Errorf("%w: require 1..%d unique guards", ErrInvalidAdmission, MaxAdmissionGuards)
	}
	a.Receipt = append(json.RawMessage(nil), a.Receipt...)
	return a, err
}
