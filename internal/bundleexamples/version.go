// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bundleexamples

import (
	"regexp"
	"strconv"
	"strings"
)

var stableTag = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// LatestStable returns the highest X.Y.Z tag (no prefix or suffix), or "".
func LatestStable(tags []string) string {
	best := ""
	var bp [3]int
	for _, t := range tags {
		if !stableTag.MatchString(t) {
			continue
		}
		var p [3]int
		for i, s := range strings.SplitN(t, ".", 3) {
			p[i], _ = strconv.Atoi(s)
		}
		if best == "" || p[0] > bp[0] ||
			(p[0] == bp[0] && p[1] > bp[1]) ||
			(p[0] == bp[0] && p[1] == bp[1] && p[2] > bp[2]) {
			best, bp = t, p
		}
	}
	return best
}
