#!/bin/bash

# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

# E2E Test Configuration
export E2E_EXAMPLES_DIR="${E2E_EXAMPLES_DIR:-examples}"
export E2E_EXPECTED_DIR="${E2E_EXPECTED_DIR:-tests/e2e/expected}"
export E2E_SERVER_LOG="${E2E_SERVER_LOG:-tests/e2e/formae-server.log}"
export E2E_STATE_DIR="${E2E_STATE_DIR:-$HOME/.pel/data/formae-repo/formae/resources}"
export E2E_COMPARE_SCRIPT="${E2E_COMPARE_SCRIPT:-tests/e2e/utils/compare_json.py}"
export E2E_COMPARE_RESOURCES_SCRIPT="${E2E_COMPARE_RESOURCES_SCRIPT:-tests/e2e/utils/compare_resources.py}"
export E2E_SERVER_START_DELAY="${E2E_SERVER_START_DELAY:-5}"
export E2E_POLL_INTERVAL="${E2E_POLL_INTERVAL:-10}"
export E2E_CONSOLE_LOG_LEVEL="${E2E_CONSOLE_LOG_LEVEL:-debug}"
export E2E_FORMAE_BINARY="${E2E_FORMAE_BINARY:-./formae}"

# AWS Configuration (if needed)
export AWS_REGION="${AWS_REGION:-us-east-1}"
export AWS_PROFILE="${AWS_PROFILE:-default}"

# Test timeout settings
export E2E_APPLY_TIMEOUT="${E2E_APPLY_TIMEOUT:-1800}"  # 30 minutes
export E2E_DESTROY_TIMEOUT="${E2E_DESTROY_TIMEOUT:-1200}"  # 20 minutes