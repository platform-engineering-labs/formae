#!/usr/bin/env python3

# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

import os
import sys
import glob
import subprocess
import json
import shutil
import re
from pathlib import Path

# Configuration
PKL_FILE = "runPklGenerator.pkl"
OUTPUT_DIR = "generated_pkl"
TMP_DIR = "tmp"
ERROR_LOG = "pkl_generation_errors.log"

# ANSI color codes
class Colors:
    RED = '\033[0;31m'
    GREEN = '\033[0;32m'
    YELLOW = '\033[1;33m'
    BLUE = '\033[0;34m'
    CYAN = '\033[0;36m'
    MAGENTA = '\033[0;35m'
    NC = '\033[0m'  # No Color

def extract_types_from_json(json_file_path):
    """Extract AWS resource types from JSON file"""
    try:
        with open(json_file_path, 'r') as f:
            data = json.load(f)

        types = set()
        if 'Resources' in data:
            for resource in data['Resources']:
                if 'Type' in resource:
                    types.add(resource['Type'])

        return sorted(list(types))
    except Exception:
        return []

def extract_pkl_from_json(json_file_path):
    """Extract PKL content from JSON file using the PKL generator"""
    try:
        # Read the JSON file
        with open(json_file_path, 'r') as f:
            json_content = f.read()

        # Run pkl eval command to extract PKL
        cmd = [
            'pkl', 'eval', PKL_FILE,
            '-p', f'Json={json_content}'
        ]

        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=60  # 60 second timeout
        )

        if result.returncode == 0:
            return True, result.stdout
        else:
            # Return error message
            error_msg = result.stderr if result.stderr else result.stdout
            return False, error_msg

    except subprocess.TimeoutExpired:
        return False, "Command timed out after 60 seconds"
    except Exception as e:
        return False, f"Exception occurred: {str(e)}"

def fix_pkl_content(pkl_content):
    """Fix PKL content by replacing // label = "" with label = "test" """
    # Replace commented label with actual label assignment
    fixed_content = re.sub(r'//\s*label\s*=\s*""', 'label = "test"', pkl_content)

    # Also handle cases where there might be single quotes
    fixed_content = re.sub(r"//\s*label\s*=\s*''", 'label = "test"', fixed_content)

    # Handle cases where label is just commented out without assignment
    fixed_content = re.sub(r'//\s*label', 'label = "test"', fixed_content)

    return fixed_content

def evaluate_pkl_content(pkl_content, filename):
    """Evaluate PKL content using pkl eval tmp/file.pkl"""
    try:
        # Fix the PKL content before writing to file
        fixed_pkl_content = fix_pkl_content(pkl_content)

        # Create the PKL file in the tmp directory
        tmp_file_path = os.path.join(TMP_DIR, f"{filename}.pkl")

        with open(tmp_file_path, 'w') as f:
            f.write(fixed_pkl_content)

        # Run pkl eval on the file in tmp directory
        cmd = ['pkl', 'eval', tmp_file_path]

        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=60  # 60 second timeout
        )

        if result.returncode == 0:
            return True, result.stdout
        else:
            error_msg = result.stderr if result.stderr else result.stdout
            return False, error_msg

    except subprocess.TimeoutExpired:
        return False, "PKL evaluation timed out after 60 seconds"
    except Exception as e:
        return False, f"Exception during PKL evaluation: {str(e)}"

def setup_tmp_directory():
    """Create and setup tmp directory"""
    try:
        # Remove tmp directory if it exists
        if os.path.exists(TMP_DIR):
            shutil.rmtree(TMP_DIR)

        # Create tmp directory
        Path(TMP_DIR).mkdir(exist_ok=True)

        return True
    except Exception as e:
        print(f"{Colors.RED}Error setting up tmp directory: {str(e)}{Colors.NC}")
        return False

def main():
    # Check if input directory is provided
    if len(sys.argv) < 2:
        print("Usage: python3 run_generator.py <input_directory> [--no-eval]")
        print("Example: python3 run_generator.py extract-fixes/grouped_resources")
        print("Example: python3 run_generator.py extract-fixes/grouped_resources --no-eval")
        sys.exit(1)

    # Get input directory from command line argument
    INPUT_DIR = sys.argv[1]

    # Check for evaluation flag
    run_evaluation = True
    if len(sys.argv) > 2 and sys.argv[2] == "--no-eval":
        run_evaluation = False

    # Check if input directory exists
    if not os.path.exists(INPUT_DIR):
        print(f"Error: Input directory '{INPUT_DIR}' does not exist")
        sys.exit(1)

    if not os.path.isdir(INPUT_DIR):
        print(f"Error: '{INPUT_DIR}' is not a directory")
        sys.exit(1)

    print(f"Running PKL generator on all JSON files in {INPUT_DIR}")
    if not run_evaluation:
        print(f"{Colors.YELLOW}Evaluation disabled - only extracting PKL content{Colors.NC}")
    print("=" * 50)

    # Setup tmp directory
    if not setup_tmp_directory():
        sys.exit(1)

    print(f"Using tmp directory: {TMP_DIR}")


    # Clear error log
    with open(ERROR_LOG, 'w') as f:
        f.write("")

    # Find all JSON files and sort them alphabetically
    json_files = glob.glob(os.path.join(INPUT_DIR, "*.json"))
    json_files.sort()  # Sort alphabetically

    if not json_files:
        print(f"No JSON files found in {INPUT_DIR}")
        sys.exit(1)

    # Initialize counters and tracking
    total_files = len(json_files)
    extract_success_count = 0
    eval_success_count = 0
    failed_count = 0
    failed_files = []

    try:
        # Process each file
        for json_file in json_files:
            filename = Path(json_file).stem  # filename without extension
            print(f"Processing {filename}...")

            # Step 1: Extract PKL from JSON
            print(f"  Extract... ", end="", flush=True)
            extract_success, extract_result = extract_pkl_from_json(json_file)

            if extract_success:
                print(f"{Colors.GREEN}SUCCESS{Colors.NC}")
                extract_success_count += 1

                # Extract and display types found in this file
                types = extract_types_from_json(json_file)
                if types:
                    for type_name in types:
                        print(f"  {Colors.CYAN}- {type_name}{Colors.NC}")
                else:
                    print(f"  {Colors.YELLOW}- No types found{Colors.NC}")

                # Step 2: Conditionally evaluate PKL content
                if run_evaluation:
                    print(f"  Evaluate... ", end="", flush=True)
                    eval_success, eval_result = evaluate_pkl_content(extract_result, filename)

                    if eval_success:
                        print(f"{Colors.GREEN}SUCCESS{Colors.NC}")
                        eval_success_count += 1

                    else:
                        print(f"{Colors.RED}FAILED{Colors.NC}")
                        failed_count += 1
                        failed_files.append(filename)

                        # Log the evaluation error
                        with open(ERROR_LOG, 'a') as f:
                            f.write("=" * 40 + "\n")
                            f.write(f"Failed file: {filename} (Evaluation)\n")
                            f.write("PKL Evaluation Error:\n")
                            f.write(eval_result + "\n")
                            f.write("\n")

                        # Show brief error in console
                        error_line = eval_result.split('\n')[0] if eval_result else "Unknown evaluation error"
                        print(f"  {Colors.YELLOW}Error: {error_line}{Colors.NC}")
                else:
                    # Just save the extracted PKL content without evaluation
                    print(f"  {Colors.BLUE}Evaluation skipped{Colors.NC}")
                    eval_success_count += 1  # Count as success since we're not evaluating

                    # Apply fixes and save to tmp directory
                    fixed_pkl_content = fix_pkl_content(extract_result)
                    tmp_file_path = os.path.join(TMP_DIR, f"{filename}.pkl")
                    with open(tmp_file_path, 'w') as f:
                        f.write(fixed_pkl_content)
            else:
                print(f"{Colors.RED}FAILED{Colors.NC}")
                failed_count += 1
                failed_files.append(filename)

                # Log the extraction error
                with open(ERROR_LOG, 'a') as f:
                    f.write("=" * 40 + "\n")
                    f.write(f"Failed file: {filename} (Extraction)\n")
                    f.write("PKL Extraction Error:\n")
                    f.write(extract_result + "\n")
                    f.write("\n")

                # Show brief error in console
                error_line = extract_result.split('\n')[0] if extract_result else "Unknown extraction error"
                print(f"  {Colors.YELLOW}Error: {error_line}{Colors.NC}")

            print()  # Add blank line between files

    finally:
        # Optionally clean up tmp directory (comment out if you want to keep files for debugging)
        # print(f"Cleaning up tmp directory: {TMP_DIR}")
        # shutil.rmtree(TMP_DIR)
        print(f"PKL files saved in tmp directory: {TMP_DIR}")

    # Print summary
    print("=" * 50)
    print("Summary:")
    print(f"Total files processed: {total_files}")
    print(f"Extract successful: {Colors.GREEN}{extract_success_count}{Colors.NC}")
    if run_evaluation:
        print(f"Evaluate successful: {Colors.GREEN}{eval_success_count}{Colors.NC}")
    else:
        print(f"Evaluation skipped: {Colors.BLUE}{eval_success_count}{Colors.NC}")
    print(f"Failed: {Colors.RED}{failed_count}{Colors.NC}")

    # Report failed files
    if failed_files:
        print(f"\n{Colors.RED}Failed files:{Colors.NC}")
        for failed_file in failed_files:
            print(f"  - {failed_file}")

        # Write failed files to a log
        with open("failed_files.log", 'w') as f:
            for failed_file in failed_files:
                f.write(failed_file + "\n")

        print(f"\nFailed file list saved to: failed_files.log")
        print(f"Detailed error messages saved to: {ERROR_LOG}")
        print(f"PKL files available for debugging in: {TMP_DIR}/")
        print(f"\n{Colors.BLUE}To see errors for a specific file:{Colors.NC}")
        print(f"grep -A 20 'Failed file: FILENAME' {ERROR_LOG}")

        sys.exit(1)
    else:
        print(f"\n{Colors.GREEN}All files processed successfully!{Colors.NC}")
        print(f"Source PKL files available in: {TMP_DIR}/")
        sys.exit(0)

if __name__ == "__main__":
    main()