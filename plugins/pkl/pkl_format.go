// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"bufio"
	"bytes"
	"strings"
	"unicode/utf8"
)

func Format(src string) string {
	lines := splitLinesPreserve(src)
	unit := detectIndentUnit(lines)
	if unit == "" {
		unit = "  "
	}

	var out bytes.Buffer
	indent := 0
	inBlockComment := false

	for i, raw := range lines {
		// Preserve pure blank lines
		if strings.TrimSpace(raw) == "" {
			out.WriteString("\n")
			continue
		}

		// Special-case: line comments keep current indent, don't change it
		leftTrim := strings.TrimLeft(raw, " \t")
		if strings.HasPrefix(leftTrim, "//") {
			writeIndent(&out, indent, unit)
			out.WriteString(leftTrim)
			if i < len(lines)-1 {
				out.WriteString("\n")
			}
			continue
		}

		preDedent, postDeltaExclPre, newInBlock := scanBraceDeltas(leftTrim, inBlockComment)

		// apply dedent from leading '}' before writing this line
		if preDedent > indent {
			indent = 0
		} else {
			indent -= preDedent
		}

		writeIndent(&out, indent, unit)
		out.WriteString(leftTrim)

		// update indent for next line using the *remaining* braces on this line
		indent += postDeltaExclPre
		if indent < 0 {
			indent = 0
		}

		inBlockComment = newInBlock

		if i < len(lines)-1 {
			out.WriteString("\n")
		}
	}

	return out.String()
}

// scanBraceDeltas examines a single line (already left-trimmed) and returns:
//
//	preDedent: number of leading '}' before any other token (outside strings/comments)
//	postDeltaExclPre: (opens - closes) for the rest of the line, excluding those leading closers
//	outInBlockComment: whether we end inside a block comment after this line
func scanBraceDeltas(line string, inBlockComment bool) (preDedent, postDeltaExclPre int, outInBlockComment bool) {
	outInBlockComment = inBlockComment

	inString := false
	var quote rune
	escaped := false

	seenNonSpaceToken := false
	countingLeadingClosers := true

	rAt := func(s string, i int) (r rune, sz int) { return utf8.DecodeRuneInString(s[i:]) }

	for i := 0; i < len(line); {
		r, sz := rAt(line, i)

		// Inside block comment: ignore everything until */
		if outInBlockComment {
			if r == '*' && i+1 < len(line) && line[i+1] == '/' {
				outInBlockComment = false
				i += 2
				continue
			}
			i += sz
			continue
		}

		// Strings (don't count braces)
		if inString {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == quote {
				inString = false
			}
			i += sz
			continue
		}

		// Start of comments?
		if r == '/' && i+1 < len(line) {
			n := line[i+1]
			if n == '/' { // line comment: stop scanning
				break
			}
			if n == '*' {
				outInBlockComment = true
				i += 2
				continue
			}
		}

		// Track first non-space token
		if !seenNonSpaceToken && (r == ' ' || r == '\t') {
			i += sz
			continue
		}
		if !seenNonSpaceToken {
			seenNonSpaceToken = true
		}

		// Strings start?
		if r == '"' || r == '\'' {
			inString = true
			quote = r
			escaped = false
			i += sz
			// past first token now; no longer counting leading closers
			countingLeadingClosers = false
			continue
		}

		// Braces
		if r == '{' {
			postDeltaExclPre++ // opening always affects post
			countingLeadingClosers = false
		} else if r == '}' {
			if countingLeadingClosers {
				preDedent++ // consume as leading dedent
			} else {
				postDeltaExclPre-- // a later closer affects post
			}
		} else {
			// Any other non-space token ends the "leading closers" phase
			countingLeadingClosers = false
		}

		i += sz
	}

	return preDedent, postDeltaExclPre, outInBlockComment
}

func writeIndent(out *bytes.Buffer, level int, unit string) {
	for i := 0; i < level; i++ {
		out.WriteString(unit)
	}
}

func splitLinesPreserve(s string) []string {
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Split(scanLinesKeepNewlines)
	var lines []string
	for sc.Scan() {
		ln := sc.Text()
		if strings.HasSuffix(ln, "\n") {
			ln = strings.TrimSuffix(ln, "\n")
		}
		lines = append(lines, ln)
	}
	if err := sc.Err(); err != nil {
		return strings.Split(s, "\n")
	}
	return lines
}

func scanLinesKeepNewlines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func detectIndentUnit(lines []string) string {
	// Prefer tabs if any line begins with a tab
	for _, l := range lines {
		for _, r := range l {
			if r == '\t' {
				return "\t"
			}
			if r == ' ' {
				break
			}
			if r != ' ' && r != '\t' {
				break
			}
		}
	}
	// Smallest positive leading space width
	min := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := leadingSpaces(l)
		if n > 0 && (min == 0 || n < min) {
			min = n
		}
	}
	if min > 0 {
		return strings.Repeat(" ", min)
	}
	return ""
}

func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}
