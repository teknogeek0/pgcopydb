package metrics

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseLSN converts a PostgreSQL LSN string ("X/Y") to a uint64.
func ParseLSN(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, nil
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid LSN format: %q", s)
	}
	hi, err := strconv.ParseUint(parts[0], 16, 32)
	if err != nil {
		return 0, fmt.Errorf("parse LSN high: %w", err)
	}
	lo, err := strconv.ParseUint(parts[1], 16, 32)
	if err != nil {
		return 0, fmt.Errorf("parse LSN low: %w", err)
	}
	return (hi << 32) | lo, nil
}

// LSNDiff returns the byte difference between two LSN values.
func LSNDiff(a, b uint64) int64 {
	return int64(a) - int64(b)
}

// FormatLSNDiff formats a byte difference as a human-readable string.
func FormatLSNDiff(bytes int64) string {
	if bytes < 0 {
		bytes = -bytes
	}
	return FormatBytes(uint64(bytes))
}

// WALRetentionTime estimates time to generate the given bytes of WAL
// based on the current write rate.
func WALRetentionTime(retainedBytes int64, walWriteRate float64) string {
	if walWriteRate <= 0 || retainedBytes <= 0 {
		return "N/A"
	}
	seconds := float64(retainedBytes) / walWriteRate
	d := time.Duration(seconds * float64(time.Second))

	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// FormatBytes formats bytes as a human-readable string.
func FormatBytes(b uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// FormatBytesRate formats bytes per second as a human-readable string.
func FormatBytesRate(bytesPerSec float64) string {
	if bytesPerSec < 1024 {
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
	if bytesPerSec < 1024*1024 {
		return fmt.Sprintf("%.1f KB/s", bytesPerSec/1024)
	}
	if bytesPerSec < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB/s", bytesPerSec/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB/s", bytesPerSec/(1024*1024*1024))
}
