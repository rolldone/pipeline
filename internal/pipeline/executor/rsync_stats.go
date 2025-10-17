package executor

import (
	"regexp"
	"strconv"
	"strings"
)

// RSyncStats holds parsed numeric information from rsync --stats output.
type RSyncStats struct {
	FilesTransferred int
	BytesTransferred int
	DurationSeconds  int
	StdoutTail       string
	StderrTail       string
}

// parseRsyncStats parses a piece of rsync output (stdout) and returns RSyncStats with numeric fields when found.
// The function is defensive and returns zero-values if it cannot parse fields.
func parseRsyncStats(stdout string, stderr string, tailLen int) RSyncStats {
	stats := RSyncStats{}
	stats.StdoutTail = tailString(stdout, tailLen)
	stats.StderrTail = tailString(stderr, tailLen)

	// Number of files transferred
	reFiles := regexp.MustCompile(`Number of files transferred:\s*(\d+)`)
	if m := reFiles.FindStringSubmatch(stdout); len(m) == 2 {
		if v, err := strconv.Atoi(m[1]); err == nil {
			stats.FilesTransferred = v
		}
	}

	// Total transferred file size (may contain commas)
	reBytes := regexp.MustCompile(`Total transferred file size:\s*([\d,]+)`)
	if m := reBytes.FindStringSubmatch(stdout); len(m) == 2 {
		s := strings.ReplaceAll(m[1], ",", "")
		if v, err := strconv.Atoi(s); err == nil {
			stats.BytesTransferred = v
		}
	}

	// Try to get transfer time from several possible formats:
	// - 'total time: 0.123 seconds'
	// - '0.123 secs' (number before 'secs')
	// - trailing '... 0.123 secs' after sent/received fields
	// We'll try multiple regexes in order.
	// 1) total time: <float>
	reTotalTime := regexp.MustCompile(`total time:\s*([0-9]+\.?[0-9]*)`)
	if m := reTotalTime.FindStringSubmatch(stdout); len(m) == 2 {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			stats.DurationSeconds = int(f + 0.5)
			return stats
		}
	}
	// 2) number followed by 'secs' or 'seconds'
	reSecsAfter := regexp.MustCompile(`([0-9]+\.?[0-9]*)\s*(secs|seconds)`)
	if m := reSecsAfter.FindStringSubmatch(stdout); len(m) >= 2 {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			stats.DurationSeconds = int(f + 0.5)
			return stats
		}
	}
	// 3) 'secs' followed by number (less common) e.g. 'secs 0.123'
	reSecsBefore := regexp.MustCompile(`secs\s*([0-9]+\.?[0-9]*)`)
	if m := reSecsBefore.FindStringSubmatch(stdout); len(m) == 2 {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			stats.DurationSeconds = int(f + 0.5)
			return stats
		}
	}

	return stats
}

// tailString returns the last n characters of s (or entire string if shorter).
func tailString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
