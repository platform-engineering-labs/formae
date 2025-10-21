// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cfres

import (
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestGetProvisioner(t *testing.T) {
	assert.NotNil(t, GetProvisionerForOperation("AWS::Route53::RecordSet", resource.OperationRead, &config.Config{Region: "us-east-1"}))
}

func TestRolePolicyRegistration(t *testing.T) {
	assert.NotNil(t, GetProvisionerForOperation("AWS::IAM::RolePolicy", resource.OperationList, &config.Config{Region: "us-east-1"}))
}
