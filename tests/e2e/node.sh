#!/bin/bash

# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

set -euo pipefail

# Load configuration
source "$(dirname "$0")/config/e2e-config.sh"

SERVER_PID_FILE="/tmp/formae-e2e-server.pid"

start_server() {
    echo "Starting Formae server..."

    # Ensure log directory exists
    mkdir -p "$(dirname "$E2E_SERVER_LOG")"

    echo "Logging server output to $E2E_SERVER_LOG"

    # Start server in background with e2e test config
    #TODO once override is implemented we want to start the server in log level debug
    #$E2E_FORMAE_BINARY agent start --console-log-level "$E2E_CONSOLE_LOG_LEVEL" > "$E2E_SERVER_LOG" 2>&1 &
    $E2E_FORMAE_BINARY agent start --config tests/e2e/config/formae.conf.pkl > "$E2E_SERVER_LOG" 2>&1 &
    local server_pid=$!

    # Save PID for cleanup
    echo $server_pid > "$SERVER_PID_FILE"

    # Wait for server to start
    sleep "$E2E_SERVER_START_DELAY"

    # Verify server is running
    if ! kill -0 $server_pid 2>/dev/null; then
        echo "Failed to start Formae server"
        exit 1
    fi

    echo "Formae server started with PID: $server_pid"

    # Wait for HTTP server to be ready (health check)
    echo "Waiting for HTTP server to be ready..."
    local max_attempts=30
    local attempt=0
    while [[ $attempt -lt $max_attempts ]]; do
        if curl -s http://localhost:49684/api/v1/health > /dev/null 2>&1; then
            echo "HTTP server is ready"
            return 0
        fi
        attempt=$((attempt + 1))
        sleep 1
    done

    echo "HTTP server failed to become ready after $max_attempts seconds"
    exit 1
}

stop_server() {
    if [[ -f "$SERVER_PID_FILE" ]]; then
        local server_pid=$(cat "$SERVER_PID_FILE")
        echo "Stopping Formae server (PID: $server_pid)..."

        if kill -0 $server_pid 2>/dev/null; then
            kill $server_pid || true
            # Wait for graceful shutdown
            sleep 2
            # Force kill if still running
            kill -9 $server_pid 2>/dev/null || true
        fi

        rm -f "$SERVER_PID_FILE"
        echo "Formae server stopped"
    else
        echo "No server PID file found"
    fi
}

get_server_status() {
    if [[ -f "$SERVER_PID_FILE" ]]; then
        local server_pid=$(cat "$SERVER_PID_FILE")
        if kill -0 $server_pid 2>/dev/null; then
            echo "running"
        else
            echo "stopped"
        fi
    else
        echo "stopped"
    fi
}

case "${1:-}" in
    start)
        start_server
        ;;
    stop)
        stop_server
        ;;
    status)
        get_server_status
        ;;
    restart)
        stop_server
        start_server
        ;;
    *)
        echo "Usage: $0 {start|stop|status|restart}"
        exit 1
        ;;
esac