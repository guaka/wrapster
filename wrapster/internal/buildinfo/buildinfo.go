package buildinfo

import (
	"strings"
	"time"
)

var BuildTime = "unknown"

func DisplayBuildTime() string {
	if BuildTime == "" || strings.EqualFold(BuildTime, "unknown") {
		return "unknown"
	}
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04",
		"2006-01-02 15:04",
	}
	for _, format := range formats {
		if parsed, err := time.Parse(format, BuildTime); err == nil {
			return parsed.Format("2006-01-02 15:04")
		}
	}
	return strings.TrimSpace(BuildTime)
}
