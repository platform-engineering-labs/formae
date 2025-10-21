#!/usr/bin/env python3

# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

import json
import sys
import os
from collections import defaultdict

def main():
    if len(sys.argv) != 2:
        print("Usage: python3 group_resources.py <input-json-file>")
        sys.exit(1)

    input_file = sys.argv[1]
    output_dir = "grouped_resources"

    # Check if input file exists
    if not os.path.exists(input_file):
        print(f"Error: File {input_file} not found")
        sys.exit(1)

    # Load the JSON data
    try:
        with open(input_file, 'r') as f:
            resources = json.load(f)
    except json.JSONDecodeError as e:
        print(f"Error parsing JSON: {e}")
        sys.exit(1)

    # Create output directory
    os.makedirs(output_dir, exist_ok=True)

    # Group resources by type
    grouped = defaultdict(list)
    for resource in resources:
        resource_type = resource.get('Type')
        if resource_type:
            grouped[resource_type].append(resource)

    print(f"Found {len(grouped)} unique resource types")
    print("Grouping resources...")

    # Write each group to a separate file
    for resource_type, type_resources in grouped.items():
        # Create safe filename
        filename = resource_type.replace('::', '_').replace('/', '_').replace(' ', '_')
        # Remove any other special characters
        filename = ''.join(c for c in filename if c.isalnum() or c in ('_', '-'))

        output_file = os.path.join(output_dir, f"{filename}.json")

        with open(output_file, 'w') as f:
            json.dump(type_resources, f, indent=2)

        print(f"  {resource_type}: {len(type_resources)} resources -> {filename}.json")

    print(f"\nTotal files created: {len(grouped)}")
    print(f"Files are located in: {output_dir}/")

if __name__ == "__main__":
    main()