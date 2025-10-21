#!/bin/bash

# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

set -euo pipefail

SCRIPT_DIR="$(dirname "$0")"
NODE_SCRIPT="$SCRIPT_DIR/node.sh"
TEST_RUNNER="$SCRIPT_DIR/test-runner.sh"

cleanup() {
    echo "Cleaning up..."
    #$NODE_SCRIPT stop
}
trap cleanup EXIT

main() {
    echo "Starting E2E test suite..."

    # Start server
    #$NODE_SCRIPT start

    # Run tests
    $TEST_RUNNER "${1:-all}"

    echo "E2E test suite completed."
}

# Run main function with all arguments
main "$@"