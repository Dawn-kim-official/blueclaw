package agentcontract

import "strings"

func RedactUnsafeText(value string) string {
	lines := []string{}
	for _, line := range strings.Split(value, "\n") {
		lowerLine := strings.ToLower(line)
		if strings.Contains(lowerLine, "cookie") || strings.Contains(lowerLine, "authorization") || strings.Contains(lowerLine, "cdp") || strings.Contains(lowerLine, "profile") || strings.Contains(lowerLine, "devicepath") || strings.Contains(lowerLine, "localpath") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
