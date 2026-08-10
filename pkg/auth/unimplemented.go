// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

// UnimplementedAuthPlugin can be embedded in an AuthPlugin implementation to
// satisfy verbs the plugin does not support. Every stub returns a nil error
// with ErrorCode set to ErrorCodeUnsupported, letting existing plugins adopt
// the widened AuthPlugin interface without implementing every verb.
//
// Init is deliberately absent: every plugin implements it.
type UnimplementedAuthPlugin struct{}

// Validate reports the request as unsupported.
func (UnimplementedAuthPlugin) Validate(req *ValidateRequest, resp *ValidateResponse) error {
	resp.Valid = false
	resp.ErrorCode = ErrorCodeUnsupported
	return nil
}

// GetAuthHeader reports the verb as unsupported.
func (UnimplementedAuthPlugin) GetAuthHeader(req *GetAuthHeaderRequest, resp *GetAuthHeaderResponse) error {
	resp.ErrorCode = ErrorCodeUnsupported
	return nil
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
