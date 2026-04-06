#!/usr/bin/env bash
# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2
#
# Sends telemetry events to PostHog for tracking CI/dev binary downloads and repo clones.
# Gated on POSTHOG_API_KEY — if unset, silently does nothing.
# Usage: source this file, then call formae_track_event "event_name" "key=value" ...

formae_track_event() {
  local api_key="${POSTHOG_API_KEY:-}"
  if [[ -z "$api_key" ]]; then return; fi

  local event="$1"; shift
  local repo
  repo=$(basename "$(git remote get-url origin 2>/dev/null)" .git 2>/dev/null || echo "unknown")

  local payload
  payload=$(jq -n \
    --arg api_key "$api_key" \
    --arg event "$event" \
    --arg repo "$repo" \
    --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg run_id "${GITHUB_RUN_ID:-}" \
    '{
      api_key: $api_key,
      distinct_id: "formae-ci",
      event: $event,
      timestamp: $ts,
      properties: {
        "$process_person_profile": false,
        repo: $repo,
        ci_run_id: $run_id
      }
    }')

  for kv in "$@"; do
    local key="${kv%%=*}" val="${kv#*=}"
    payload=$(echo "$payload" | jq --arg k "$key" --arg v "$val" '.properties[$k] = $v')
  done

  curl -sf -o /dev/null https://k.platform.engineering/capture/ \
    -H "Content-Type: application/json" \
    -d "$payload" || echo "[telemetry] event send failed (non-critical)" >&2 &
}
