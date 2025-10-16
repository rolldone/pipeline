package util

import "strings"

// Dedent removes common leading indentation from a multiline string and
// trims leading/trailing blank lines.
func Dedent(s string) string {
	lines := strings.Split(s, "\n")

	// trim leading/trailing blank lines
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}

	// find minimum indent (count of leading spaces/tabs) among non-empty lines
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return strings.Join(lines, "\n") + "\n"
	}

	// remove minIndent from each line
	for i, line := range lines {
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		} else {
			lines[i] = strings.TrimLeft(line, " \t")
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
