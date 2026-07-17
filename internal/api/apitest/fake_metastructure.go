// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apitest

import (
	formae "github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Compile-time guard: FakeMetastructure must implement MetastructureAPI.
var _ metastructure.MetastructureAPI = (*FakeMetastructure)(nil)

type WrappedCommandResponse struct {
	SubmitCommandResponse *apimodel.SubmitCommandResponse
	Error                 error
}

type WrappedExtractResponse struct {
	Forma *pkgmodel.Forma
	Error error
}

type WrappedListResponse struct {
	ListCommandStatusResponse *apimodel.ListCommandStatusResponse
	Error                     error
}

type WrappedCancelResponse struct {
	CancelCommandResponse *apimodel.CancelCommandResponse
	Error                 error
}

type WrappedTargetResponse struct {
	Targets []*pkgmodel.Target
	Error   error
}

type WrappedDriftResponse struct {
	Drift apimodel.ModifiedStack
	Error error
}

type WrappedReconcileResponse struct {
	Response *apimodel.ForceReconcileResponse
	Error    error
}

type WrappedCheckTTLResponse struct {
	Response *apimodel.ForceCheckTTLResponse
	Error    error
}

type WrappedStackResponse struct {
	Stacks []*pkgmodel.Stack
	Error  error
}

type WrappedPolicyResponse struct {
	Policies []apimodel.PolicyInventoryItem
	Error    error
}

type FakeMetastructure struct {
	ApplyResponses          []WrappedCommandResponse
	DestroyResponses        []WrappedCommandResponse
	ExtractResponses        []WrappedExtractResponse
	TargetResponses         []WrappedTargetResponse
	ListResponses           []WrappedListResponse
	CancelResponses         []WrappedCancelResponse
	DriftResponses          []WrappedDriftResponse
	ReconcileResponses      []WrappedReconcileResponse
	CheckTTLResponses       []WrappedCheckTTLResponse
	StackResponses          []WrappedStackResponse
	PolicyResponses         []WrappedPolicyResponse
	RecordedCancelQueries   []string
	RecordedExtractQueries  []string
}

func (m *FakeMetastructure) ApplyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	nextResponse := m.ApplyResponses[0]
	m.ApplyResponses = m.ApplyResponses[1:]

	return nextResponse.SubmitCommandResponse, nextResponse.Error
}

func (m *FakeMetastructure) DestroyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	nextResponse := m.DestroyResponses[0]
	m.DestroyResponses = m.DestroyResponses[1:]

	return nextResponse.SubmitCommandResponse, nextResponse.Error
}

func (m *FakeMetastructure) DestroyByQuery(query string, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	nextResponse := m.DestroyResponses[0]
	m.DestroyResponses = m.DestroyResponses[1:]

	return nextResponse.SubmitCommandResponse, nextResponse.Error
}

func (m *FakeMetastructure) CancelCommand(commandID string, force bool, clientID string) (*changeset.CancelResponse, error) {
	return nil, nil
}

func (m *FakeMetastructure) CancelCommandsByQuery(query string, force bool, clientID string) (*apimodel.CancelCommandResponse, error) {
	m.RecordedCancelQueries = append(m.RecordedCancelQueries, query)
	nextResponse := m.CancelResponses[0]
	m.CancelResponses = m.CancelResponses[1:]

	return nextResponse.CancelCommandResponse, nextResponse.Error
}

func (m *FakeMetastructure) ListFormaCommandStatus(commandID string, clientID string, n int) (*apimodel.ListCommandStatusResponse, error) {
	// Handle empty queue: return nil response + nil error (safe zero behavior).
	if len(m.ListResponses) == 0 {
		return nil, nil
	}

	nextResponse := m.ListResponses[0]

	// Pop the response if there's more than one in the queue (FIFO for multi-response tests).
	// If this is the last one, keep it (sticky tail) so subsequent polls don't panic.
	if len(m.ListResponses) > 1 {
		m.ListResponses = m.ListResponses[1:]
	}

	return nextResponse.ListCommandStatusResponse, nextResponse.Error
}

func (m *FakeMetastructure) ExtractResources(query string) (*pkgmodel.Forma, error) {
	m.RecordedExtractQueries = append(m.RecordedExtractQueries, query)
	if len(m.ExtractResponses) == 0 {
		return &pkgmodel.Forma{}, nil
	}
	next := m.ExtractResponses[0]
	if len(m.ExtractResponses) > 1 {
		m.ExtractResponses = m.ExtractResponses[1:]
	}
	return next.Forma, next.Error
}

func (m *FakeMetastructure) ExtractTargets(query string) ([]*pkgmodel.Target, error) {
	if len(m.TargetResponses) == 0 {
		return []*pkgmodel.Target{}, nil
	}
	next := m.TargetResponses[0]
	if len(m.TargetResponses) > 1 {
		m.TargetResponses = m.TargetResponses[1:]
	}
	return next.Targets, next.Error
}

func (m *FakeMetastructure) ExtractStacks() ([]*pkgmodel.Stack, error) {
	if len(m.StackResponses) == 0 {
		return []*pkgmodel.Stack{}, nil
	}
	next := m.StackResponses[0]
	if len(m.StackResponses) > 1 {
		m.StackResponses = m.StackResponses[1:]
	}
	return next.Stacks, next.Error
}

func (m *FakeMetastructure) ExtractPolicies() ([]apimodel.PolicyInventoryItem, error) {
	if len(m.PolicyResponses) == 0 {
		return []apimodel.PolicyInventoryItem{}, nil
	}
	next := m.PolicyResponses[0]
	if len(m.PolicyResponses) > 1 {
		m.PolicyResponses = m.PolicyResponses[1:]
	}
	return next.Policies, next.Error
}

func (m *FakeMetastructure) ForceSync() error {
	return nil
}

func (m *FakeMetastructure) ForceDiscovery() error {
	return nil
}

func (m *FakeMetastructure) ForceAutoReconcile(stackLabel string) (*apimodel.ForceReconcileResponse, error) {
	nextResponse := m.ReconcileResponses[0]
	m.ReconcileResponses = m.ReconcileResponses[1:]

	return nextResponse.Response, nextResponse.Error
}

func (m *FakeMetastructure) ForceCheckTTL() (*apimodel.ForceCheckTTLResponse, error) {
	nextResponse := m.CheckTTLResponses[0]
	m.CheckTTLResponses = m.CheckTTLResponses[1:]

	return nextResponse.Response, nextResponse.Error
}

func (m *FakeMetastructure) ListDrift(stack string) (*apimodel.ModifiedStack, error) {
	nextResponse := m.DriftResponses[0]
	m.DriftResponses = m.DriftResponses[1:]
	return &nextResponse.Drift, nextResponse.Error
}

func (m *FakeMetastructure) Stats() (*apimodel.Stats, error) {
	return &apimodel.Stats{
		Version: formae.Version,
		AgentID: "test-agent",
	}, nil
}

func (m *FakeMetastructure) RegisteredPlugins() ([]messages.RegisteredPluginInfo, error) {
	return nil, nil
}
