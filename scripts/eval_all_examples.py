# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

"""
Formae Examples Evaluation Script

This script runs the `formae eval` command on all example .pkl files in the examples directory.
It automatically discovers and evaluates .pkl files that contain Forma { } blocks, which
indicate they are complete Formae configurations that can be evaluated.

The script:
- Recursively finds all .pkl files in the examples directory
- Filters out vars.pkl files (which typically contain only variables/imports)
- Filters out files that don't contain 'Forma {' blocks
- Runs 'go run cmd/formae/main.go eval <file>' on each valid file
- Reports success/failure status for each evaluation
- Provides a comprehensive summary at the end

Usage:
    python scripts/eval_all_examples.py

The script uses relative paths based on its own location, so it should work
regardless of where the formae repository is located on the filesystem.
"""

import subprocess
import sys
from pathlib import Path

def has_forma_block(pkl_file_path):
    """Check if a .pkl file contains 'Forma {' block."""
    try:
        with open(pkl_file_path, 'r', encoding='utf-8') as file:
            content = file.read()
            return 'Forma {' in content
    except Exception:
        return False

def run_eval_on_pkl_file(pkl_file_path, base_dir):
    """Run eval on a single .pkl file and return success status."""
    try:
        # Convert absolute path to relative path from base_dir
        relative_path = pkl_file_path.relative_to(base_dir)

        # Run the eval command
        cmd = ["go", "run", "cmd/formae/main.go", "eval", str(relative_path)]
        result = subprocess.run(
            cmd,
            cwd=base_dir,
            capture_output=True,
            text=True,
            timeout=30  # 30 second timeout
        )

        if result.returncode == 0:
            return True, None
        else:
            return False, result.stderr.strip() or result.stdout.strip()

    except subprocess.TimeoutExpired:
        return False, "Command timed out after 30 seconds"
    except Exception as e:
        return False, str(e)

def eval_all_pkl_files():
    """Find and evaluate all .pkl files in the examples directory."""
    # Get paths relative to this script's location
    script_dir = Path(__file__).parent
    base_dir = script_dir.parent  # formae root directory
    examples_dir = base_dir / "examples"

    if not examples_dir.exists():
        print(f"Examples directory does not exist: {examples_dir}")
        return

    if not base_dir.exists():
        print(f"Base directory does not exist: {base_dir}")
        return

    # Find all .pkl files recursively
    all_pkl_files = list(examples_dir.rglob("*.pkl"))

    # Filter out vars.pkl files and files without Forma blocks
    vars_pkl_files = [f for f in all_pkl_files if f.name == "vars.pkl"]
    non_vars_files = [f for f in all_pkl_files if f.name != "vars.pkl"]

    # Filter files that contain Forma blocks
    pkl_files = [f for f in non_vars_files if has_forma_block(f)]

    vars_skipped = len(vars_pkl_files)
    no_forma_skipped = len(non_vars_files) - len(pkl_files)
    total_skipped = vars_skipped + no_forma_skipped

    if not pkl_files:
        print("No .pkl files found in the examples directory (after filtering)")
        return

    print(f"Found {len(all_pkl_files)} .pkl files")
    print(f"  - {vars_skipped} vars.pkl files skipped")
    print(f"  - {no_forma_skipped} files without 'Forma {{' skipped")
    print(f"  - {len(pkl_files)} files to evaluate")
    print("=" * 60)

    successful = []
    failed = []

    for pkl_file in sorted(pkl_files):
        relative_path = pkl_file.relative_to(examples_dir)
        print(f"Evaluating: {relative_path}")

        success, error_msg = run_eval_on_pkl_file(pkl_file, base_dir)

        if success:
            successful.append(str(relative_path))
            print(f"  ✓ SUCCESS")
        else:
            failed.append((str(relative_path), error_msg))
            print(f"  ✗ FAILED: {error_msg}")

        print()

    # Print summary
    print("=" * 60)
    print("SUMMARY")
    print("=" * 60)
    print(f"Total files found: {len(all_pkl_files)}")
    print(f"Files skipped (vars.pkl): {vars_skipped}")
    print(f"Files skipped (no Forma block): {no_forma_skipped}")
    print(f"Files evaluated: {len(pkl_files)}")
    print(f"Successful: {len(successful)}")
    print(f"Failed: {len(failed)}")

    if successful:
        print(f"\nSuccessful evaluations ({len(successful)}):")
        for file_path in successful:
            print(f"  ✓ {file_path}")

    if failed:
        print(f"\nFailed evaluations ({len(failed)}):")
        for file_path, error in failed:
            print(f"  ✗ {file_path}")
            if error:
                # Indent error message
                error_lines = error.split('\n')
                for line in error_lines:
                    print(f"    {line}")
                print()

    # Exit with error code if any failed
    if failed:
        sys.exit(1)
    else:
        print("\nAll evaluations completed successfully! 🎉")

if __name__ == "__main__":
    eval_all_pkl_files()