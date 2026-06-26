// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEmbedResolvable exercises an embedded resolvable inside a String-typed
// field (CloudFront Function.functionCode) end-to-end against real AWS:
//
//  1. single-apply resolution — the KeyValueStore's post-create short Id is
//     substituted into the JS literal and shipped to CloudFront as a String;
//  2. extract regenerates the field as formae.embed("…\(…)…"), not a raw
//     $embed envelope nor a baked-in literal;
//  3. editing the literal text around the ref and reapplying lands an update;
//  4. a forced sync preserves the structured embed (re-extract still emits it).
//
// Requires the dev-channel AWS plugin (functionCode widened to
// String|formae.Embedded) — the e2e workflow installs aws@dev for this entry.
func TestEmbedResolvable(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "embed_resolvable_aws.pkl")
	const stackQuery = "stack:e2e-embed-resolvable-aws"
	commandTimeout := 5 * time.Minute

	// Step 1: Apply — KVS + Function are created in dependency order; the KVS
	// short Id is resolved and embedded into functionCode at apply time.
	cmdID := cli.Apply(t, "reconcile", fixture)
	RequireCommandSuccess(t, cli.WaitForCommand(t, cmdID, commandTimeout))

	resources := cli.Inventory(t, "--query", stackQuery)
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources after apply, got %d", len(resources))
	}
	RequireResource(t, resources, "e2e-embed-kvs")
	RequireResource(t, resources, "e2e-embed-fn")

	// Step 2: Extract — the regenerated PKL must carry the embed as
	// formae.embed("…\(kvStore.res.id)…"), with no raw envelope leaking.
	extracted := filepath.Join(t.TempDir(), "extracted.pkl")
	cli.ExtractToFile(t, stackQuery, extracted)
	body, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatalf("failed to read extracted PKL: %v", err)
	}
	src := string(body)
	for _, want := range []string{"formae.embed(", ".res.id", `\(`} {
		if !strings.Contains(src, want) {
			t.Fatalf("extracted PKL missing %q; got:\n%s", want, src)
		}
	}
	for _, bad := range []string{"$embed", "$template"} {
		if strings.Contains(src, bad) {
			t.Fatalf("extracted PKL leaked raw embed envelope (%q); got:\n%s", bad, src)
		}
	}

	// Step 3: Edit literal text around the ref and reapply — the update lands
	// without the embedded resolvable getting in the way.
	edited := strings.Replace(src, "async function handler", "async function handlerV2", 1)
	if edited == src {
		t.Fatalf("expected to edit the function literal; marker not found in:\n%s", src)
	}
	if err := os.WriteFile(extracted, []byte(edited), 0o644); err != nil {
		t.Fatalf("failed to write edited PKL: %v", err)
	}
	editID := cli.Apply(t, "reconcile", extracted)
	RequireCommandSuccess(t, cli.WaitForCommand(t, editID, commandTimeout))

	// Step 4: Sync preservation — ForceSync, then re-extract and confirm the
	// embed survived a read-back from the cloud (envelope kept structured).
	cli.ForceSync(t)
	reextracted := filepath.Join(t.TempDir(), "extracted2.pkl")
	cli.ExtractToFile(t, stackQuery, reextracted)
	body2, err := os.ReadFile(reextracted)
	if err != nil {
		t.Fatalf("failed to read re-extracted PKL: %v", err)
	}
	if !strings.Contains(string(body2), "formae.embed(") {
		t.Fatalf("embed not preserved after sync; got:\n%s", string(body2))
	}

	// Step 5: Destroy via the extracted PKL; the stack must be empty.
	destroyID := cli.Destroy(t, extracted)
	RequireCommandSuccess(t, cli.WaitForCommand(t, destroyID, commandTimeout))
	if remaining := cli.Inventory(t, "--query", stackQuery); len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
