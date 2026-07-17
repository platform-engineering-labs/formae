// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stackRow converts a *pkgmodel.Stack into a render-ready row.
// Cells: [Label, Description, policySummary].
// policySummary ports the legacy renderer logic with the injected clock.
func stackRow(s *pkgmodel.Stack, now time.Time) row {
	summary := policySummary(s.Policies, s.CreatedAt, now)
	return row{
		cells: []string{s.Label, s.Description, summary},
		detail: func(width int) []string {
			return stackDetail(s, now, width)
		},
	}
}

// policySummary builds a human-readable summary of inline or reference policies.
// It ports the logic from internal/cli/renderer/renderer.go:formatStackPolicies
// but replaces time.Now()/time.Since() with the injected now parameter.
func policySummary(policies []json.RawMessage, createdAt time.Time, now time.Time) string {
	if len(policies) == 0 {
		return "none"
	}

	var parts []string
	for _, policyJSON := range policies {
		// Handle $ref entries (policy references) first.
		if pkgmodel.IsPolicyReference(policyJSON) {
			label, err := pkgmodel.ParsePolicyReference(policyJSON)
			if err == nil {
				parts = append(parts, label)
			}
			continue
		}

		// Inline policy: parse and format.
		var policy map[string]any
		if err := json.Unmarshal(policyJSON, &policy); err != nil {
			continue
		}

		policyType, _ := policy["Type"].(string)
		switch policyType {
		case "ttl":
			ttlSeconds, _ := policy["TTLSeconds"].(float64)
			duration := time.Duration(int64(ttlSeconds)) * time.Second
			label, _ := policy["Label"].(string)

			var expiryStr string
			if !createdAt.IsZero() {
				expiresAt := createdAt.Add(duration)
				expiryStr = formatExpiryTimeInj(expiresAt, now)
			}

			if expiryStr == "expired" {
				// Expired: render as "TTL: <dur> (expired)" with optional label
				if label != "" {
					parts = append(parts, fmt.Sprintf("TTL: %s (expired) (%s)", formatTTLDur(duration), label))
				} else {
					parts = append(parts, fmt.Sprintf("TTL: %s (expired)", formatTTLDur(duration)))
				}
			} else if label != "" && expiryStr != "" {
				parts = append(parts, fmt.Sprintf("TTL: %s, expires %s (%s)", formatTTLDur(duration), expiryStr, label))
			} else if expiryStr != "" {
				parts = append(parts, fmt.Sprintf("TTL: %s, expires %s", formatTTLDur(duration), expiryStr))
			} else if label != "" {
				parts = append(parts, fmt.Sprintf("TTL: %s (%s)", formatTTLDur(duration), label))
			} else {
				parts = append(parts, fmt.Sprintf("TTL: %s", formatTTLDur(duration)))
			}

		case "auto-reconcile":
			intervalSeconds, _ := policy["IntervalSeconds"].(float64)
			duration := time.Duration(int64(intervalSeconds)) * time.Second
			label, _ := policy["Label"].(string)

			var lastReconStr string
			if lastReconAt, ok := policy["LastReconcileAt"].(string); ok && lastReconAt != "" {
				if t, err := time.Parse(time.RFC3339, lastReconAt); err == nil && !t.IsZero() {
					lastReconStr = formatLastReconcileTimeInj(t, now)
				}
			}

			var part string
			if lastReconStr != "" && label != "" {
				part = fmt.Sprintf("Auto-reconcile: every %s, last %s (%s)", formatTTLDur(duration), lastReconStr, label)
			} else if lastReconStr != "" {
				part = fmt.Sprintf("Auto-reconcile: every %s, last %s", formatTTLDur(duration), lastReconStr)
			} else if label != "" {
				part = fmt.Sprintf("Auto-reconcile: every %s (%s)", formatTTLDur(duration), label)
			} else {
				part = fmt.Sprintf("Auto-reconcile: every %s", formatTTLDur(duration))
			}
			parts = append(parts, part)

		default:
			// Unknown type: render type string (mirrors renderer behaviour).
			// Empty policyType is skipped (no contribution).
			if policyType != "" {
				parts = append(parts, policyType)
			}
		}
	}

	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// stackDetail renders the detail panel for a stack.
// Identity lines: Label, Description, CreatedAt (RFC3339), then blank + "Policies:".
// Inline policies: jsonTree; references: "  → policy: <label>".
func stackDetail(s *pkgmodel.Stack, _ time.Time, _ int) []string {
	// Keys: Label(5), Description(11), CreatedAt(9).
	// Longest = "Description" (11). Format: "%-12s" (key + ":" = 12 chars).
	lines := []string{
		fmt.Sprintf("%-12s %s", "Label:", s.Label),
		fmt.Sprintf("%-12s %s", "Description:", s.Description),
		fmt.Sprintf("%-12s %s", "CreatedAt:", s.CreatedAt.UTC().Format(time.RFC3339)),
	}

	lines = append(lines, "")
	lines = append(lines, "Policies:")

	if len(s.Policies) == 0 {
		lines = append(lines, "  none")
		return lines
	}

	for _, policyJSON := range s.Policies {
		if pkgmodel.IsPolicyReference(policyJSON) {
			label, err := pkgmodel.ParsePolicyReference(policyJSON)
			if err == nil {
				lines = append(lines, fmt.Sprintf("  → policy: %s", label))
			}
			continue
		}
		// Inline: render via jsonTree with 1-space indent
		lines = append(lines, jsonTree(policyJSON, 1)...)
	}

	return lines
}

// formatExpiryTimeInj is formatExpiryTime from the renderer but with injected now.
// No ANSI color codes are used in the TUI layer.
func formatExpiryTimeInj(expiresAt time.Time, now time.Time) string {
	if expiresAt.Before(now) {
		return "expired"
	}

	remaining := expiresAt.Sub(now)

	if remaining < 24*time.Hour {
		if remaining < time.Hour {
			return fmt.Sprintf("in %dm", int(remaining.Minutes()))
		}
		return fmt.Sprintf("in %dh%dm", int(remaining.Hours()), int(remaining.Minutes())%60)
	}

	return expiresAt.Local().Format("Jan 2 15:04")
}

// formatLastReconcileTimeInj is formatLastReconcileTime from the renderer but
// with injected now (replaces time.Since(t)).
func formatLastReconcileTimeInj(t time.Time, now time.Time) string {
	ago := now.Sub(t)
	if ago < time.Minute {
		return "just now"
	}
	if ago < time.Hour {
		return fmt.Sprintf("%dm ago", int(ago.Minutes()))
	}
	if ago < 24*time.Hour {
		return fmt.Sprintf("%dh%dm ago", int(ago.Hours()), int(ago.Minutes())%60)
	}
	return t.Local().Format("Jan 2 15:04")
}

// formatTTLDur formats a duration in a human-friendly way.
// Ports renderer's formatTTLDuration.
func formatTTLDur(d time.Duration) string {
	if d >= 24*time.Hour {
		days := d / (24 * time.Hour)
		return fmt.Sprintf("%dd", days)
	}
	if d >= time.Hour {
		hours := d / time.Hour
		return fmt.Sprintf("%dh", hours)
	}
	if d >= time.Minute {
		minutes := d / time.Minute
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
