#!/bin/bash

# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

set -euo pipefail

# Load configuration
source "$(dirname "$0")/config/e2e-config.sh"

SCRIPT_DIR="$(dirname "$0")"
NODE_SCRIPT="$SCRIPT_DIR/node.sh"
TEST_DEFINITIONS="$SCRIPT_DIR/config/e2e-test-definitions.yaml"
NODE_LOG_FILE=tests/e2e/formae-server.log
TEST_LOGS_DIR="tests/e2e/test-logs"

# Ensure test logs directory exists
mkdir -p "$TEST_LOGS_DIR"

print_test_log() {
    local test_name="$1"
    local test_log_file="$TEST_LOGS_DIR/${test_name}-server.log"

    echo ""
    echo "=== Server Log for failed test: $test_name ==="
    if [[ -f "$test_log_file" ]]; then
        cat "$test_log_file"
    else
        echo "Test log file $test_log_file not found"
    fi
    echo "=== End Server Log for failed test: $test_name ==="
    echo ""
}

print_test_status() {
    local test_name="$1"
    local stage="${2:-unknown}"
    local command_id="${3:-}"

    echo ""
    echo "=== Test Status for: $test_name (stage: $stage) ==="
    if [[ -n "$command_id" ]]; then
        $E2E_FORMAE_BINARY status command --query="id:$command_id" --output-consumer machine || echo "Failed to get status for command $command_id"
    else
        $E2E_FORMAE_BINARY status command --query="id:$command_id" --output-layout detailed || echo "Failed to get detailed status"
    fi
    echo "=== End Test Status for: $test_name ==="
    echo ""
}

start_node_for_test() {
    local test_name="$1"

    echo "Starting node for test: $test_name"
    if ! "$NODE_SCRIPT" start; then
        echo "Failed to start node for test: $test_name"
        return 1
    fi

    # Give the node a moment to fully initialize
    sleep 2

    return 0
}

stop_node_for_test() {
    local test_name="$1"
    local test_log_file="$TEST_LOGS_DIR/${test_name}-server.log"

    echo "Stopping node for test: $test_name"

    # Stop the node
    "$NODE_SCRIPT" stop

    # Move the log file to test-specific location
    if [[ -f "$NODE_LOG_FILE" ]]; then
        mv "$NODE_LOG_FILE" "$test_log_file"
        echo "Moved server log to: $test_log_file"
    else
        echo "Warning: No server log file found at $NODE_LOG_FILE"
    fi

    return 0
}

wait_for_operation() {
    local operation_name="$1"
    local command_id="$2"
    local timeout="${3:-1800}"
    local start_time=$(date +%s)
    local iteration=0
    local detailed_interval="${E2E_DETAILED_INTERVAL:-5}"  # Print detailed output every 5 iterations

    echo "Waiting for $operation_name (Command ID: $command_id) to complete..."

    while true; do
        local current_time=$(date +%s)
        local elapsed=$((current_time - start_time))

        if [[ $elapsed -gt $timeout ]]; then
            echo "$operation_name timed out after $timeout seconds"
            return 1
        fi

        # Query specific command by ID and capture raw output
        local status_output
        if ! status_output=$($E2E_FORMAE_BINARY status command --query="id:$command_id" --output-consumer machine 2>&1); then
            echo "Failed to query command status: $status_output"
            sleep "$E2E_POLL_INTERVAL"
            continue
        fi

        # Validate JSON before parsing
        if ! echo "$status_output" | jq empty 2>/dev/null; then
            echo "WARNING: Invalid JSON received, retrying..."
            # echo "DEBUG: Raw status output:"
            # echo "$status_output"
            # echo "DEBUG: End raw output"
            sleep "$E2E_POLL_INTERVAL"
            continue
        fi

        local state=$(echo "$status_output" | jq -r '.Commands[0].State // "Unknown"')

        echo "Current state: $state (elapsed: ${elapsed}s)"

        # Print detailed output every N iterations
        iteration=$((iteration + 1))
        if [[ $((iteration % detailed_interval)) -eq 0 ]]; then
            echo "--- Detailed status (iteration $iteration) ---"
            local status_detailed
            if status_detailed=$($E2E_FORMAE_BINARY status command --query="id:$command_id" --output-layout detailed 2>&1); then
                echo "$status_detailed"
            else
                echo "Failed to get detailed status: $status_detailed"
            fi
            echo "--- End detailed status ---"
        fi

        case "$state" in
            "Success")
                echo "$operation_name completed successfully"
                return 0
                ;;
            "Failed")
                echo "$operation_name failed!"
                # Print detailed output on failure
                echo "--- Final detailed status on failure ---"
                if echo "$status_output" | jq empty 2>/dev/null; then
                    echo "$status_output" | jq '.'
                else
                    echo "Raw output (invalid JSON): $status_output"
                fi
                echo "--- End detailed status ---"
                return 1
                ;;
            "Unknown")
                echo "Unknown state for command $command_id"
                return 1
                ;;
            *)
                sleep "$E2E_POLL_INTERVAL"
                ;;
        esac
    done
}


compare_state() {
    local test_name="$1"
    local expected_dir="$2"

    echo "Extracting state using inventory for $test_name..."

    # Create temp directory and extracted file path
    local temp_dir=$(mktemp -d)
    local extracted_file_output="$temp_dir/$test_name.json"
    local resources_only_output="$temp_dir/$test_name-resources.json"

    echo "Writing extracted state to: $extracted_file_output"

    # Capture inventory command output and errors
    local inventory_cmd_output
    local inventory_cmd_exit_code=0

    # Use inventory resources command instead of extract
    if ! inventory_cmd_output=$($E2E_FORMAE_BINARY inventory resources --output-consumer machine 2>&1); then
        inventory_cmd_exit_code=$?
        echo "ERROR: Inventory command failed with exit code $inventory_cmd_exit_code"
        echo "=== Inventory Command Output ==="
        echo "$inventory_cmd_output"
        echo "=== End Inventory Command Output ==="
        rm -rf "$temp_dir"
        return 1
    fi

    # Write the inventory output to file
    echo "$inventory_cmd_output" > "$extracted_file_output"

    # Check if the extracted file exists and is not empty
    if [[ ! -f "$extracted_file_output" ]]; then
        echo "ERROR: Inventory command did not create output file: $extracted_file_output"
        echo "=== Inventory Command Output ==="
        echo "$inventory_cmd_output"
        echo "=== End Inventory Command Output ==="
        rm -rf "$temp_dir"
        return 1
    fi

    if [[ ! -s "$extracted_file_output" ]]; then
        echo "ERROR: Inventory output is empty for $test_name"
        echo "=== Inventory Command Output ==="
        echo "$inventory_cmd_output"
        echo "=== End Inventory Command Output ==="
        rm -rf "$temp_dir"
        return 1
    fi

    # Validate that the output is valid JSON
    if ! jq empty "$extracted_file_output" 2>/dev/null; then
        echo "ERROR: Inventory output is not valid JSON for $test_name"
        echo "=== Inventory Command Output ==="
        echo "$inventory_cmd_output"
        echo "=== End Inventory Command Output ==="
        rm -rf "$temp_dir"
        return 1
    fi

    # Extract only the Resources using jq
    echo "Extracting Resources only from inventory output..."
    if ! jq '.Resources' "$extracted_file_output" > "$resources_only_output"; then
        echo "ERROR: Failed to extract Resources from JSON for $test_name"
        echo "=== File Content ==="
        cat "$extracted_file_output"
        echo "=== End File Content ==="
        rm -rf "$temp_dir"
        return 1
    fi

    # Validate Resources and check if it contains meaningful data
    local content
    if ! content=$(jq -r 'if type == "array" then length else if type == "object" then keys | length else 1 end end' "$resources_only_output" 2>/dev/null); then
        echo "ERROR: Resources section contains invalid JSON for $test_name"
        echo "=== Inventory Command Output ==="
        echo "$inventory_cmd_output"
        echo "=== End Inventory Command Output ==="
        echo "=== Resources Content ==="
        cat "$resources_only_output"
        echo "=== End Resources Content ==="
        rm -rf "$temp_dir"
        return 1
    fi

    if [[ "$content" == "0" || "$content" == "null" ]]; then
        echo "ERROR: Inventory Resources section contains no resources for $test_name"
        echo "=== Inventory Command Output ==="
        echo "$inventory_cmd_output"
        echo "=== End Inventory Command Output ==="
        echo "=== Resources Content ==="
        cat "$resources_only_output"
        echo "=== End Resources Content ==="
        rm -rf "$temp_dir"
        return 1
    fi

    echo "Extracted Resources contains $content items"

    if [[ -d "$expected_dir" ]]; then
        echo "Comparing state for $test_name..."
        # Use the resources-only file for comparison
        if python3 "$E2E_COMPARE_RESOURCES_SCRIPT" "$resources_only_output" "$expected_dir"; then
            echo "State comparison passed for $test_name"
            rm -rf "$temp_dir"
            return 0
        else
            # Don't remove temp_dir on failure for debugging
            echo "State comparison failed for $test_name"
            echo "=== Inventory Command Output (for debugging) ==="
            echo "$inventory_cmd_output"
            echo "=== End Inventory Command Output ==="
            echo "Full inventory output preserved at: $extracted_file_output"
            echo "Resources-only file preserved at: $resources_only_output"
            echo "Temp directory preserved at: $temp_dir"
            return 1
        fi
    else
        echo "Expected state directory $expected_dir not found, skipping comparison."
        rm -rf "$temp_dir"
        return 1
    fi
}


run_single_test() {
    local test_name="$1"
    local source_file="$2"
    local expected_dir="$3"
    local apply_timeout="${4:-1800}"
    local destroy_timeout="${5:-1200}"

    echo "=== Running E2E test: $test_name ==="

    # Start node for this test
    if ! start_node_for_test "$test_name"; then
        echo "Failed to start node for test: $test_name"
        return 1
    fi

    local test_failed=0

    # Apply
    echo "Applying configuration..."
    local apply_output=$($E2E_FORMAE_BINARY apply --output-consumer machine --mode reconcile "$source_file")
    local apply_command_id=$(echo "$apply_output" | jq -r '.CommandId // empty')

    if [[ -z "$apply_command_id" ]]; then
        echo "Failed to get CommandId from apply operation"
        test_failed=1
    else
        echo "Apply CommandId: $apply_command_id"

        # Wait for apply
        if ! wait_for_operation "apply" "$apply_command_id" "$apply_timeout"; then
            echo "Apply failed for $test_name"
            print_test_status "$test_name" "apply-failed" "$apply_command_id"
            test_failed=1
        else
            # Compare state using directory-based comparison
            if ! compare_state "$test_name" "$expected_dir"; then
                echo "State comparison failed for $test_name"
                print_test_status "$test_name" "state-comparison-failed" "$apply_command_id"
                test_failed=1
            fi
        fi

        # Destroy (attempt even if apply/comparison failed)
        echo "Destroying configuration..."
        local destroy_output=$($E2E_FORMAE_BINARY destroy --output-consumer machine "$source_file" )
        local destroy_command_id=$(echo "$destroy_output" | jq -r '.CommandId // empty')

        if [[ -z "$destroy_command_id" ]]; then
            echo "Failed to get CommandId from destroy operation"
            test_failed=1
        else
            echo "Destroy CommandId: $destroy_command_id"

            # Wait for destroy
            if ! wait_for_operation "destroy" "$destroy_command_id" "$destroy_timeout"; then
                echo "Destroy failed for $test_name"
                print_test_status "$test_name" "destroy-failed" "$destroy_command_id"
                test_failed=1
            fi
        fi
    fi

    # Stop node and move logs
    stop_node_for_test "$test_name"

    # If test failed, print the test-specific log
    if [[ $test_failed -eq 1 ]]; then
        print_test_log "$test_name"
        echo "=== Failed E2E test: $test_name ==="
        return 1
    fi

    echo "=== Completed E2E test: $test_name ==="
    return 0
}

print_agent_id() {
    local when="$1"
    local agent_id_file="$HOME/.pel/formae/agent_id"

    echo ""
    echo "=== Agent ID ($when) ==="
    if [[ -f "$agent_id_file" ]]; then
        echo "Agent ID: $(cat "$agent_id_file")"
    else
        echo "Agent ID file not found at: $agent_id_file"
    fi
    echo "========================="
    echo ""
}

run_all_tests() {
    print_agent_id "start"
    # Parse YAML and run tests (requires yq)
    if ! command -v yq &> /dev/null; then
        echo "yq is required to parse test definitions. Install with: brew install yq"
        exit 1
    fi

    local test_names=$(yq eval '.tests | keys | .[]' "$TEST_DEFINITIONS")
    local failed_tests=()

    for test_name in $test_names; do
        local source=$(yq eval ".tests.$test_name.source" "$TEST_DEFINITIONS")
        local expected_dir=$(yq eval ".tests.$test_name.expected_dir" "$TEST_DEFINITIONS")
        local apply_timeout=$(yq eval ".tests.$test_name.timeout.apply // 1800" "$TEST_DEFINITIONS")
        local destroy_timeout=$(yq eval ".tests.$test_name.timeout.destroy // 1200" "$TEST_DEFINITIONS")

        if ! run_single_test "$test_name" "$source" "$expected_dir" "$apply_timeout" "$destroy_timeout"; then
            failed_tests+=("$test_name")
        fi
    done



    if [[ ${#failed_tests[@]} -gt 0 ]]; then
        echo ""
        echo "=== SUMMARY ==="
        echo "Failed tests: ${failed_tests[*]}"
        echo "Test logs available in: $TEST_LOGS_DIR"
        for failed_test in "${failed_tests[@]}"; do
            echo "  - ${failed_test}: $TEST_LOGS_DIR/${failed_test}-server.log"
        done
        return 1
    fi

    echo ""
    echo "=== SUMMARY ==="
    echo "All E2E tests completed successfully!"
    echo "Test logs available in: $TEST_LOGS_DIR"
    print_agent_id "end"
    return 0
}

# Cleanup function for unexpected exits
cleanup() {
    echo "Cleaning up..."
    "$NODE_SCRIPT" stop 2>/dev/null || true
}

# Set trap for cleanup
trap cleanup EXIT

# Main execution
case "${1:-all}" in
    all)
        run_all_tests
        ;;
    single)
        if [[ -z "${2:-}" ]]; then
            echo "Usage: $0 single <test_name>"
            exit 1
        fi
        local test_name="$2"
        local source=$(yq eval ".tests.$test_name.source" "$TEST_DEFINITIONS")
        local expected_dir=$(yq eval ".tests.$test_name.expected_dir" "$TEST_DEFINITIONS")
        run_single_test "$test_name" "$source" "$expected_dir"
        ;;
    *)
        echo "Usage: $0 {all|single <test_name>}"
        exit 1
        ;;
esac