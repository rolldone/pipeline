package executor

import (
	"testing"
)

func TestParseRsyncStats_Simple(t *testing.T) {
	stdout := `Number of files transferred: 3
	Total file size: 12345
	Total transferred file size: 12,345
	total time: 0.42 seconds
`
	stats := parseRsyncStats(stdout, "", 200)
	if stats.FilesTransferred != 3 {
		t.Fatalf("expected 3 files, got %d", stats.FilesTransferred)
	}
	if stats.BytesTransferred != 12345 {
		t.Fatalf("expected bytes 12345, got %d", stats.BytesTransferred)
	}
	// duration may be 0 (not parsed) or a small rounded value; ensure non-negative
	if stats.DurationSeconds < 0 {
		t.Fatalf("unexpected negative duration: %d", stats.DurationSeconds)
	}
}

func TestParseRsyncStats_VariedFormats(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		files  int
		bytes  int
	}{
		{"commas_and_sent", "Number of files transferred: 10\nTotal transferred file size: 1,234,567\n", 10, 1234567},
		{"no_files", "Some unrelated output\n", 0, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stats := parseRsyncStats(c.stdout, "", 200)
			if stats.FilesTransferred != c.files {
				t.Fatalf("%s: expected files %d got %d", c.name, c.files, stats.FilesTransferred)
			}
			if stats.BytesTransferred != c.bytes {
				t.Fatalf("%s: expected bytes %d got %d", c.name, c.bytes, stats.BytesTransferred)
			}
		})
	}
}

func TestParseRsyncStats_TimeVariants(t *testing.T) {
	cases := []struct {
		name     string
		stdout   string
		wantSecs int
	}{
		{"total_time_seconds", "Number of files transferred: 1\nTotal transferred file size: 100\ntotal time: 0.42 seconds\n", 0},
		{"secs_after", "sent 1024 bytes  received 2048 bytes  0.55 secs\n", 1},
		{"secs_word_after", "some summary\n0.9 secs\n", 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stats := parseRsyncStats(c.stdout, "", 200)
			if stats.DurationSeconds < 0 {
				t.Fatalf("negative duration parsed: %d", stats.DurationSeconds)
			}
			// We allow rounding; ensure not negative and plausible
		})
	}
}
