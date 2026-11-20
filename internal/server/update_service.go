package server

import "strings"

func validSystemdService(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '@' {
			continue
		}
		return false
	}
	return true
}

// ValidUpdateService exposes the same strict service-name contract to the
// server entrypoint before it starts serving update tasks.
func ValidUpdateService(value string) bool {
	return validSystemdService(value)
}
