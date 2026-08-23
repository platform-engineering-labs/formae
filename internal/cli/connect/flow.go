// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package connect connects a cloud account to a hosted installation: it
// establishes trust on the cloud side (or is told trust exists) and registers
// the resulting role with the control plane.
package connect

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// options is everything `connect aws` decides from, read once off the flags.
type options struct {
	Account     string
	QuickCreate bool
	// ProviderExists flips the link's CreateProvider parameter for an account
	// that was connected before: the shared OIDC provider already exists and
	// IAM allows only one per issuer.
	ProviderExists bool
	ProfileAWS     string
	RoleArn        string
	Region         string
	NoInput        bool

	// ConfigFlag and ProfileFlag are carried verbatim into the resume hint:
	// a fresh shell may have a different active profile.
	ConfigFlag  string
	ProfileFlag string
}

// mode is the path a connect run takes.
type mode int

const (
	modeForm mode = iota
	modeQuickCreate
	modeLocal
	modeRegisterOnly
)

// decideMode validates the flag set and picks the path. Combining trust flags
// is an error, never a precedence.
func decideMode(opts options, tty bool) (mode, error) {
	set := 0
	for _, on := range []bool{opts.QuickCreate, opts.ProfileAWS != "", opts.RoleArn != ""} {
		if on {
			set++
		}
	}
	if set > 1 {
		return 0, cmd.FlagErrorf("--quick-create, --profile-aws, and --role-arn are mutually exclusive; pass exactly one")
	}
	if opts.Region != "" && opts.ProfileAWS == "" {
		return 0, cmd.FlagErrorf("--region applies only to the local path; pass it with --profile-aws")
	}
	if opts.ProviderExists && !opts.QuickCreate {
		return 0, cmd.FlagErrorf("--provider-exists answers a question only quick-create asks; pass it with --quick-create")
	}
	if opts.NoInput {
		var missing []string
		if opts.Account == "" {
			missing = append(missing, "--account")
		}
		if set == 0 {
			missing = append(missing, "one of --quick-create, --profile-aws, --role-arn")
		}
		if len(missing) > 0 {
			return 0, cmd.FlagErrorf("--no-input requires %s", strings.Join(missing, " and "))
		}
	}
	if opts.Account != "" {
		if err := validateAccount(opts.Account); err != nil {
			return 0, err
		}
	}
	switch {
	case opts.QuickCreate:
		return modeQuickCreate, requireAccount(opts)
	case opts.ProfileAWS != "":
		return modeLocal, requireAccount(opts)
	case opts.RoleArn != "":
		if err := requireAccount(opts); err != nil {
			return 0, err
		}
		_, err := parseRoleArn(opts.RoleArn, opts.Account)
		return modeRegisterOnly, err
	case !tty:
		return 0, errors.New("no trust flag was given and there is no TTY for the interactive form; " +
			"use --no-input with --account and one of --quick-create, --profile-aws, --role-arn")
	default:
		return modeForm, nil
	}
}

var accountRE = regexp.MustCompile(`^[0-9]{12}$`)

func validateAccount(account string) error {
	if !accountRE.MatchString(account) {
		return cmd.FlagErrorf("--account must be exactly 12 digits")
	}
	return nil
}

func requireAccount(opts options) error {
	if opts.Account == "" && opts.NoInput {
		return cmd.FlagErrorf("--no-input requires --account")
	}
	return nil
}

// parsedRoleArn is a validated commercial-partition IAM role ARN.
type parsedRoleArn struct {
	Account  string
	RoleName string // final path component; compared against cloudRoleName
	Arn      string
}

// parseRoleArn is hard on shape and account; the soft name-vs-cloudRoleName
// comparison happens later, once setup has been read.
func parseRoleArn(arn, statedAccount string) (parsedRoleArn, error) {
	rest, ok := strings.CutPrefix(arn, "arn:")
	if !ok {
		return parsedRoleArn{}, printer.Fail(printer.CodeUnsupportedPartition,
			"the role ARN is not a well-formed IAM role ARN", nil)
	}
	parts := strings.SplitN(rest, ":", 5)
	if len(parts) != 5 || parts[1] != "iam" || parts[2] != "" ||
		!strings.HasPrefix(parts[4], "role/") || len(parts[4]) <= len("role/") {
		return parsedRoleArn{}, printer.Fail(printer.CodeUnsupportedPartition,
			"the role ARN is not a well-formed IAM role ARN (arn:aws:iam::<account>:role/<name>)", nil)
	}
	if parts[0] != "aws" {
		return parsedRoleArn{}, printer.Fail(printer.CodeUnsupportedPartition,
			"only the commercial aws partition is supported; GovCloud and China ARNs are refused", nil)
	}
	if parts[3] != statedAccount {
		return parsedRoleArn{}, printer.Fail(printer.CodeAccountMismatch,
			"the role ARN names a different account than --account", nil)
	}
	segments := strings.Split(strings.TrimPrefix(parts[4], "role/"), "/")
	return parsedRoleArn{Account: parts[3], RoleName: segments[len(segments)-1], Arn: arn}, nil
}

// warnOnNameMismatch returns a warning naming both role names when they
// differ, and nothing when they match. A mismatch is a warning rather than a
// refusal: the human said this is the role, and the CLI registers what it was
// told while saying what it noticed.
func warnOnNameMismatch(actual, expected string) string {
	if actual == expected {
		return ""
	}
	return fmt.Sprintf("the role is named %q where this installation's expected role name is %q; "+
		"registering the role you named", actual, expected)
}

// accountInHint reports whether any aws hint entry names the account — on
// any installation, including the one being connected. A known account means
// the shared provider very likely exists, so the interactive flow asks
// instead of assuming a first connection.
func accountInHint(hint []cloudapi.ConnectedAccount, account string) bool {
	for _, entry := range hint {
		if entry.Cloud == "aws" && entry.Account == account {
			return true
		}
	}
	return false
}

// connectedElsewhere returns the hint entries naming the stated AWS account on
// an installation other than the one being connected. Non-empty means: warn
// loudly and confirm interactively; in --no-input the warning rides the
// machine document and the run proceeds. Only cloud "aws" entries compare: an
// account string from another cloud that happens to look like a 12-digit AWS
// id is not this account.
func connectedElsewhere(hint []cloudapi.ConnectedAccount, account, installationID string) []cloudapi.ConnectedAccount {
	var elsewhere []cloudapi.ConnectedAccount
	for _, entry := range hint {
		if entry.Cloud == "aws" && entry.Account == account && entry.InstallationID != installationID {
			elsewhere = append(elsewhere, entry)
		}
	}
	return elsewhere
}
