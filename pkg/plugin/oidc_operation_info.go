// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"time"

	"ergo.services/ergo/gen"
)

// OperationCallTimeout bounds a watched resource operation call. The agent
// watcher and the OIDC operation metadata use this same bound.
const OperationCallTimeout = 60 * time.Second

// OidcOperationInfo describes the trusted broker binding and effective operator
// timing. It contains no credentials and grants no authority to mint tokens.
// BindingID is opaque and stable across identical broker restarts and ordinary
// signing-key rotation; identity-changing broker configuration changes it.
type OidcOperationInfo struct {
	BindingID        string
	PollInterval     time.Duration
	CallTimeout      time.Duration
	RetryDelay       time.Duration
	ThrottleMaxDelay time.Duration
}

type oidcOperationInfoKey struct{}

// OidcOperationMetadata returns coordinator-supplied metadata for this operation.
// The bool is false on older agents, untrusted broker launches, or incomplete or
// invalid metadata. Ordinary OIDC token minting does not require this metadata.
func OidcOperationMetadata(ctx context.Context) (OidcOperationInfo, bool) {
	info, ok := ctx.Value(oidcOperationInfoKey{}).(OidcOperationInfo)
	if !ok || info.BindingID == "" || info.PollInterval < 0 || info.CallTimeout <= 0 || info.RetryDelay < 0 || info.ThrottleMaxDelay < 0 {
		return OidcOperationInfo{}, false
	}
	return info, true
}

// Read only process env: node defaults cannot establish a trusted binding.
// Each value crossing EDF is a string or the already registered time.Duration.
func oidcOperationInfoFromEnv(proc gen.Process) (OidcOperationInfo, bool) {
	var info OidcOperationInfo
	v, _ := proc.Env("OidcOperationBindingID")
	var ok bool
	if info.BindingID, ok = v.(string); !ok {
		return OidcOperationInfo{}, false
	}
	for key, dest := range map[gen.Env]*time.Duration{
		"OidcOperationPollInterval":     &info.PollInterval,
		"OidcOperationCallTimeout":      &info.CallTimeout,
		"OidcOperationRetryDelay":       &info.RetryDelay,
		"OidcOperationThrottleMaxDelay": &info.ThrottleMaxDelay,
	} {
		v, _ = proc.Env(key)
		if *dest, ok = v.(time.Duration); !ok {
			return OidcOperationInfo{}, false
		}
	}
	return OidcOperationMetadata(context.WithValue(context.Background(), oidcOperationInfoKey{}, info))
}
