// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import "errors"

// UnimplementedAuthPlugin can be embedded in an AuthPlugin implementation to
// satisfy verbs the plugin does not support. Most stubs return a nil error
// with ErrorCode set to ErrorCodeUnsupported, letting existing plugins adopt
// the widened AuthPlugin interface without implementing every verb.
//
// GetAuthHeader is the exception: it returns a Go error instead. A host
// built against the pre-widening GetAuthHeaderResponse (Headers only, no
// Error or ErrorCode) cannot observe those fields, so a nil-error response
// with empty Headers reads as successful, unauthenticated access. Signaling
// through the RPC error instead makes an agent-only plugin fail closed on
// every host version, old or new.
//
// Init is deliberately absent: every plugin implements it.
type UnimplementedAuthPlugin struct{}

// Validate reports the request as unsupported.
func (UnimplementedAuthPlugin) Validate(req *ValidateRequest, resp *ValidateResponse) error {
	resp.Valid = false
	resp.ErrorCode = ErrorCodeUnsupported
	return nil
}

// GetAuthHeader returns an RPC error rather than a typed response field: a
// host running against the older GetAuthHeaderResponse shape (Headers only)
// cannot see ErrorCode, and a nil error there is indistinguishable from a
// successful, empty-headers response.
func (UnimplementedAuthPlugin) GetAuthHeader(req *GetAuthHeaderRequest, resp *GetAuthHeaderResponse) error {
	return errors.New("auth plugin does not support GetAuthHeader")
}

// LoginStart reports the verb as unsupported.
func (UnimplementedAuthPlugin) LoginStart(req *LoginStartRequest, resp *LoginStartResponse) error {
	resp.ErrorCode = ErrorCodeUnsupported
	return nil
}

// LoginWait reports the verb as unsupported.
func (UnimplementedAuthPlugin) LoginWait(req *LoginWaitRequest, resp *LoginWaitResponse) error {
	resp.ErrorCode = ErrorCodeUnsupported
	return nil
}

// Logout reports the verb as unsupported.
func (UnimplementedAuthPlugin) Logout(req *LogoutRequest, resp *LogoutResponse) error {
	resp.ErrorCode = ErrorCodeUnsupported
	return nil
}
