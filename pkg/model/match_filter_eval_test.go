// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panickingPath parses cleanly but blows up during evaluation: the filter
// selector binds @ to each of the root object's member values, so @.member
// asks a string for a field and the match() extension receives nothing.
const panickingPath = `$[?match(@.member, ".*formae.*")]`

func TestMatchFilterExcludesNothingWhenEvaluationFails(t *testing.T) {
	filter := MatchFilter{
		Conditions: []FilterCondition{{PropertyPath: panickingPath}},
	}

	got := filter.Excludes(json.RawMessage(`{"member":"formae-ai"}`))

	assert.False(t, got, "an expression that cannot be evaluated must exclude nothing")
}

func TestMatchFilterLogsAnExpressionItCannotEvaluate(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	filter := MatchFilter{
		Conditions: []FilterCondition{{PropertyPath: panickingPath}},
	}

	filter.Excludes(json.RawMessage(`{"member":"formae-ai"}`))

	require.NotEmpty(t, logs.String(), "a filter that silently excludes nothing would leave substrate exposed")
	// The handler escapes the quotes inside the expression, so match on a
	// fragment that survives encoding rather than the literal path.
	assert.Contains(t, logs.String(), "@.member", "the log must name the expression that failed")
	assert.Contains(t, logs.String(), "nil pointer dereference", "the log must carry the underlying cause")
}

func TestMatchFilterWithNoConditionsExcludesNothing(t *testing.T) {
	filter := MatchFilter{ResourceTypes: []string{"AWS::EC2::Instance"}}

	assert.False(t, filter.Excludes(json.RawMessage(`{"Name":"anything"}`)),
		"a filter naming no conditions must not exclude every resource")
}

func TestMatchFilterRequiresEveryConditionToMatch(t *testing.T) {
	filter := MatchFilter{
		Conditions: []FilterCondition{
			{PropertyPath: "$.tags.app", PropertyValue: "formae-agent"},
			{PropertyPath: "$.tags.tier", PropertyValue: "control"},
		},
	}

	assert.True(t, filter.Excludes(json.RawMessage(`{"tags":{"app":"formae-agent","tier":"control"}}`)))
	assert.False(t, filter.Excludes(json.RawMessage(`{"tags":{"app":"formae-agent","tier":"data"}}`)))
}

// An empty PropertyValue is an existence check, which is what the substring
// conditions on untaggable resources rely on.
func TestMatchFilterTreatsAnEmptyValueAsAnExistenceCheck(t *testing.T) {
	filter := MatchFilter{
		Conditions: []FilterCondition{{
			PropertyPath: `$[?search(@, "workloadIdentityPools/formae-ai/subject/fai:")]`,
		}},
	}

	ours := json.RawMessage(`{"project":"p","role":"roles/viewer","member":"principalSet://iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/formae-ai/subject/fai:t/i"}`)
	theirs := json.RawMessage(`{"project":"p","role":"roles/viewer","member":"serviceAccount:someone@example.iam.gserviceaccount.com"}`)

	assert.True(t, filter.Excludes(ours))
	assert.False(t, filter.Excludes(theirs))
}
