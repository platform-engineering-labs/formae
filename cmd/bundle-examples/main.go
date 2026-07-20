// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/platform-engineering-labs/formae/internal/bundleexamples"
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

var ghURL = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$`)

// ManifestEntry records what was resolved and staged for one standard-bundle
// member: the resolved release tag, the commit that tag points at, and whether
// examples were staged. The post-build opkg assertion reads this back.
type ManifestEntry struct {
	Member          string `json:"member"`
	Tag             string `json:"tag"`
	CommitSHA       string `json:"commit_sha"`
	ExamplesPresent bool   `json:"examples_present"`
}

// Manifest is the checked intent of a stage run: this build's formae version and
// the per-member resolution. The expected set for the resolve gate and the opkg
// assertion is the checked-in policy (RequiredMembers), recorded here.
type Manifest struct {
	FormaeVersion string          `json:"formae_version"`
	Members       []ManifestEntry `json:"members"`
}

func main() {
	dist := flag.String("dist", "dist/pel/formae/examples", "stage dir")
	manifest := flag.String("manifest", ".out/examples-manifest.json", "staged-member manifest (JSON)")
	formaeVer := flag.String("formae-version", "", "REQUIRED: this build's formae X.Y.Z (Makefile passes $(VERSION))")
	opkgGlob := flag.String("opkg", "", "opkg path or glob for the contents assertion")
	skipStage := flag.Bool("skip-stage", false, "only assert opkg contents against the manifest")
	stdRepo := flag.String("standard-repo", "https://github.com/platform-engineering-labs/formae-plugin-standard.git", "")
	hub := flag.String("hub", "https://hub.platform.engineering", "")
	flag.Parse()

	var err error
	if *skipStage {
		err = assertOpkg(*opkgGlob, *manifest)
	} else {
		err = stage(*dist, *manifest, *formaeVer, *stdRepo, *hub)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "bundle-examples: %v\n", err)
		os.Exit(1)
	}
}

func stage(dist, manifestPath, formaeVer, stdRepo, hub string) error {
	// formaeVer is REQUIRED (the Makefile passes formae's own $(VERSION)); the
	// examples ship in this formae binary, so they pin its version, not "latest".
	if formaeVer == "" {
		return fmt.Errorf("--formae-version is required (this build's formae X.Y.Z)")
	}

	stdURL, err := validateGH(stdRepo)
	if err != nil {
		return fmt.Errorf("standard repo url: %w", err)
	}
	stdTag, _, err := latestTag(stdRepo)
	if err != nil {
		return fmt.Errorf("standard tag: %w", err)
	}
	if stdTag == "" {
		return fmt.Errorf("no stable tag on standard bundle")
	}
	opkgfile, err := fetchBytes(fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/Opkgfile", stdURL, stdTag))
	if err != nil {
		return fmt.Errorf("fetch standard Opkgfile: %w", err)
	}
	members := bundleexamples.Members(string(opkgfile))
	if len(members) == 0 {
		return fmt.Errorf("standard Opkgfile parsed to zero members")
	}
	// Fail closed on any standard-bundle member that is not classified in the
	// examples policy: a new member must be a deliberate decision, not a silent
	// skip.
	if err := bundleexamples.CheckMembers(members); err != nil {
		return err
	}

	// Fail-closed Hub presence check for formae@<VERSION>, wrapped in a bounded
	// retry with exponential backoff (~30s) as insurance against CDN/cache lag
	// right after the schema S3 sync. A genuine 404 after retries still fails.
	// The pkg_pkl->pkg_opkg ordering in release.yml is what makes it resolvable.
	formaeURL := fmt.Sprintf("%s/plugins/pkl/schema/pkl/formae/formae@%s", hub, formaeVer)
	if err := probeGETWithRetry(formaeURL, 5, 2*time.Second); err != nil {
		return fmt.Errorf("formae@%s not on hub (is pkg_pkl ordered before pkg_opkg?): %w", formaeVer, err)
	}

	m := Manifest{FormaeVersion: formaeVer}
	staged := map[string]bool{}
	for _, member := range members {
		switch bundleexamples.PolicyFor(member) {
		case bundleexamples.ExamplesNone:
			fmt.Printf("bundle-examples: %s policy=none, skipping\n", member)
			continue
		case bundleexamples.ExamplesUnknown:
			// Unreachable: CheckMembers already failed. Belt and suspenders.
			return fmt.Errorf("member %q not in examples policy", member)
		}

		repo, err := hubRepo(hub, member)
		if err != nil {
			return fmt.Errorf("resolve repo for %q: %w", member, err)
		}
		tag, sha, err := latestTag(repo)
		if err != nil {
			return fmt.Errorf("tag for %q: %w", member, err)
		}
		if tag == "" {
			return fmt.Errorf("no stable tag for %q (%s)", member, repo)
		}
		tmp, err := os.MkdirTemp("", "ex-"+member+"-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()

		hasDir, files, err := fetchExamples(repo, tag, tmp)
		if err != nil {
			return fmt.Errorf("fetch %q@%s: %w", member, tag, err)
		}
		// Policy says this member is REQUIRED to ship examples; an absent dir or
		// an empty dir is a build failure, not a skip.
		if !hasDir {
			return fmt.Errorf("%s@%s is required to ship examples but has no examples/ dir", member, tag)
		}
		if files == 0 {
			return fmt.Errorf("%s@%s has an examples/ dir but no files were staged", member, tag)
		}
		if err := rewriteTree(tmp, member, tag, formaeVer); err != nil {
			return fmt.Errorf("rewrite %q: %w", member, err)
		}
		if err := resolveTree(tmp); err != nil {
			return fmt.Errorf("resolve %q: %w", member, err)
		}
		dest := filepath.Join(dist, member)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			return fmt.Errorf("swap %q into place: %w", member, err)
		}
		fmt.Printf("bundle-examples: staged %s@%s (%s, %d files)\n", member, tag, shortSHA(sha), files)
		staged[member] = true
		m.Members = append(m.Members, ManifestEntry{
			Member:          member,
			Tag:             tag,
			CommitSHA:       sha,
			ExamplesPresent: true,
		})
	}

	// Guard against intent, not outcome: every REQUIRED member must have staged.
	if missing := bundleexamples.MissingRequired(staged); len(missing) > 0 {
		return fmt.Errorf("required members produced no examples: %s", strings.Join(missing, ", "))
	}

	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath, append(blob, '\n'), 0o644)
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func validateGH(u string) (string, error) {
	m := ghURL.FindStringSubmatch(u)
	if m == nil {
		return "", fmt.Errorf("not a public https github url: %q", u)
	}
	return m[1] + "/" + m[2], nil
}

func hubRepo(hub, member string) (string, error) {
	body, err := fetchBytes(fmt.Sprintf("%s/api/v1/plugins/%s", hub, member))
	if err != nil {
		return "", err
	}
	var v struct {
		GithubRepoURL string `json:"github_repo_url"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", err
	}
	if _, err := validateGH(v.GithubRepoURL); err != nil {
		return "", fmt.Errorf("hub returned %w", err)
	}
	return v.GithubRepoURL, nil
}

// latestTag returns the highest stable X.Y.Z tag on a remote and the commit SHA
// that tag points at (the dereferenced commit for annotated tags).
func latestTag(gitURL string) (tag, sha string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "ls-remote", "--tags", gitURL).Output()
	if err != nil {
		return "", "", fmt.Errorf("git ls-remote %s: %w", gitURL, err)
	}
	plain := map[string]string{}
	deref := map[string]string{}
	seen := map[string]bool{}
	var tags []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		refSHA, ref := fields[0], fields[1]
		i := strings.LastIndex(ref, "refs/tags/")
		if i < 0 {
			continue
		}
		raw := ref[i+len("refs/tags/"):]
		if strings.HasSuffix(raw, "^{}") {
			deref[strings.TrimSuffix(raw, "^{}")] = refSHA
			continue
		}
		plain[raw] = refSHA
		if !seen[raw] {
			seen[raw] = true
			tags = append(tags, raw)
		}
	}
	best := bundleexamples.LatestStable(tags)
	if best == "" {
		return "", "", nil
	}
	commit := deref[best]
	if commit == "" {
		commit = plain[best]
	}
	return best, commit, nil
}

func fetchBytes(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func probeGET(url string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s", resp.Status)
	}
	return nil
}

// probeGETWithRetry probes url up to attempts times with exponential backoff
// (base, 2*base, 4*base, ...), returning nil on the first success and the last
// error if all attempts fail.
func probeGETWithRetry(url string, attempts int, base time.Duration) error {
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			delay := base * time.Duration(1<<uint(i-1))
			fmt.Printf("bundle-examples: waiting %s before retrying %s (attempt %d/%d)\n", delay, url, i+1, attempts)
			time.Sleep(delay)
		}
		last = probeGET(url)
		if last == nil {
			return nil
		}
	}
	return last
}

// fetchExamples downloads <repo>@<tag> and extracts the examples/ subtree into
// dst. Returns (hasExamplesDir, filesWritten). Unsupported tar entry types fail.
func fetchExamples(gitURL, tag, dst string) (bool, int, error) {
	or, err := validateGH(gitURL)
	if err != nil {
		return false, 0, err
	}
	url := fmt.Sprintf("https://github.com/%s/archive/refs/tags/%s.tar.gz", or, tag)
	resp, err := httpClient.Get(url)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return false, 0, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return false, 0, err
	}
	tr := tar.NewReader(gz)
	cleanDst := filepath.Clean(dst)
	hasDir, files := false, 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, 0, err
		}
		parts := strings.SplitN(h.Name, "/", 2)
		if len(parts) != 2 || (parts[1] != "examples" && !strings.HasPrefix(parts[1], "examples/")) {
			continue
		}
		hasDir = true
		rel := strings.TrimPrefix(strings.TrimPrefix(parts[1], "examples"), "/")
		if rel == "" {
			continue
		}
		target := filepath.Join(cleanDst, rel)
		if target != cleanDst && !strings.HasPrefix(target, cleanDst+string(os.PathSeparator)) {
			return false, 0, fmt.Errorf("tar path escape: %s", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return false, 0, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return false, 0, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, h.FileInfo().Mode().Perm())
			if err != nil {
				return false, 0, err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return false, 0, err
			}
			if err := f.Close(); err != nil {
				return false, 0, err
			}
			files++
		default:
			return false, 0, fmt.Errorf("unsupported tar entry %q (type %d) under examples/", h.Name, h.Typeflag)
		}
	}
	return hasDir, files, nil
}

func rewriteTree(root, member, tag, formaeVer string) error {
	escape := regexp.MustCompile(`import\(\s*"\.\.[^"]*schema/pkl`)
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		base := filepath.Base(p)
		if base == "PklProject.deps.json" {
			return os.Remove(p)
		}
		if info.IsDir() || base != "PklProject" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := bundleexamples.RewritePklProject(string(b), member, tag, formaeVer)
		if escape.MatchString(out) {
			return fmt.Errorf("self-schema escape survived in %s", p)
		}
		return os.WriteFile(p, []byte(out), info.Mode())
	})
}

func resolveTree(root string) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Base(p) != "PklProject" {
			return nil
		}
		dir := filepath.Dir(p)
		cmd := exec.Command("pkl", "project", "resolve", dir)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pkl project resolve %s: %w", dir, err)
		}
		return nil
	})
}

// resolveOpkg picks the opkg to assert against. An exact existing file path is
// used directly (preferred); otherwise the arg is treated as a glob and the
// newest match by modtime wins.
func resolveOpkg(arg string) (string, error) {
	if fi, err := os.Stat(arg); err == nil && !fi.IsDir() {
		return arg, nil
	}
	matches, _ := filepath.Glob(arg)
	if len(matches) == 0 {
		return "", fmt.Errorf("no opkg matched %q", arg)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	newest, newestT := "", time.Time{}
	for _, mm := range matches {
		fi, err := os.Stat(mm)
		if err != nil {
			return "", err
		}
		if fi.ModTime().After(newestT) {
			newest, newestT = mm, fi.ModTime()
		}
	}
	return newest, nil
}

func assertOpkg(opkgArg, manifestPath string) error {
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return fmt.Errorf("parse manifest %s: %w", manifestPath, err)
	}
	opkg, err := resolveOpkg(opkgArg)
	if err != nil {
		return err
	}
	out, err := exec.Command("ops", "opkg", "contents", opkg).Output()
	if err != nil {
		return fmt.Errorf("ops opkg contents %s: %w", opkg, err)
	}
	// The expected set is the checked-in policy, recorded in the manifest as the
	// members with examples present - not "whatever staged".
	checked := 0
	for _, e := range m.Members {
		if !e.ExamplesPresent {
			continue
		}
		want := "formae/examples/" + e.Member + "/"
		if !strings.Contains(string(out), want) {
			return fmt.Errorf("opkg %s missing %s", opkg, want)
		}
		checked++
	}
	if checked == 0 {
		return fmt.Errorf("manifest %s records no members with examples", manifestPath)
	}
	fmt.Printf("bundle-examples: opkg %s contains examples for %d member(s)\n", opkg, checked)
	return nil
}
