// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cfres

import (
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/prov"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/registry"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"

	_ "github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/apigateway"
	_ "github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/ec2"
	_ "github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/iam"
	_ "github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/route53"
	_ "github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/secretsmanager"
)

func GetProvisionerForOperation(resourceType string, operation resource.Operation, cfg *config.Config) prov.Provisioner {
	return registry.Get(resourceType, operation, cfg)
}
