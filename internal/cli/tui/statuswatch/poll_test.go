// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

type fakeClient struct {
	resp    *apimodel.ListCommandStatusResponse
	err     error
	queries []string
}

func (f *fakeClient) GetCommandsStatus(query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
	f.queries = append(f.queries, query)
	return f.resp, nil, f.err
}

func TestFetchCommands_ReturnsCommands(t *testing.T) {
	fc := &fakeClient{resp: &apimodel.ListCommandStatusResponse{
		Commands: []apimodel.Command{{CommandID: "cmd-1"}},
	}}
	msg := fetchCommands(fc, "state:InProgress", 10)()
	cm, ok := msg.(commandsMsg)
	require.True(t, ok)
	require.NoError(t, cm.err)
	assert.Len(t, cm.commands, 1)
	assert.Equal(t, []string{"state:InProgress"}, fc.queries)
}

func TestFetchCommands_PropagatesError(t *testing.T) {
	fc := &fakeClient{err: errors.New("boom")}
	msg := fetchCommands(fc, "", 10)()
	cm := msg.(commandsMsg)
	assert.Error(t, cm.err)
}

func TestFetchCommands_NilResponse(t *testing.T) {
	fc := &fakeClient{}
	cm := fetchCommands(fc, "", 10)().(commandsMsg)
	require.NoError(t, cm.err)
	assert.Empty(t, cm.commands)
}
