package resource_update

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// panickingPath parses cleanly but blows up during evaluation: the filter
// selector binds @ to each of the root object's member values, so @.member
// asks a string for a field and the match() extension receives nothing.
const panickingPath = `$[?match(@.member, ".*formae.*")]`

func TestShouldFilterByMatchFilterExcludesNothingWhenEvaluationFails(t *testing.T) {
	filter := pkgmodel.MatchFilter{
		Conditions: []pkgmodel.FilterCondition{{PropertyPath: panickingPath}},
	}

	got := ShouldFilterByMatchFilter(&filter, json.RawMessage(`{"member":"formae-ai"}`))

	assert.False(t, got, "an expression that cannot be evaluated must exclude nothing")
}

func TestShouldFilterByMatchFilterLogsAnExpressionItCannotEvaluate(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	filter := pkgmodel.MatchFilter{
		Conditions: []pkgmodel.FilterCondition{{PropertyPath: panickingPath}},
	}

	ShouldFilterByMatchFilter(&filter, json.RawMessage(`{"member":"formae-ai"}`))

	require.NotEmpty(t, logs.String(), "a filter that silently excludes nothing would leave substrate exposed")
	// The handler escapes the quotes inside the expression, so match on a
	// fragment that survives encoding rather than the literal path.
	assert.Contains(t, logs.String(), "@.member", "the log must name the expression that failed")
	assert.Contains(t, logs.String(), "nil pointer dereference", "the log must carry the underlying cause")
}

// A condition that evaluates normally must keep working, so the guard cannot be
// hiding ordinary failures.
func TestShouldFilterByMatchFilterStillEvaluatesSoundExpressions(t *testing.T) {
	filter := pkgmodel.MatchFilter{
		Conditions: []pkgmodel.FilterCondition{{
			PropertyPath: `$[?search(@, "workloadIdentityPools/formae-ai/subject/fai:")]`,
		}},
	}

	ours := json.RawMessage(`{"project":"p","role":"roles/viewer","member":"principalSet://iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/formae-ai/subject/fai:t/i"}`)
	theirs := json.RawMessage(`{"project":"p","role":"roles/viewer","member":"serviceAccount:someone@example.iam.gserviceaccount.com"}`)

	assert.True(t, ShouldFilterByMatchFilter(&filter, ours))
	assert.False(t, ShouldFilterByMatchFilter(&filter, theirs))
}
