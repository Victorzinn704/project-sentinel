package sanitize

import "strings"

var strongMarkers = []string{
	"to=functions.",
	"to=multi_tool_use.",
	"to=multi_tool_use.parallel",
	"to=update_plan",
	"to=shell ",
	"to=shell\n",
	"recipient_name\":\"functions.",
	"recipient_name\":\"multi_tool_use.",
	"*** begin patch",
	"functions.exec_command",
	"asypay_json_string",
}

var weakMarkers = []string{
	"\"tool_uses\"",
	"\"recipient_name\"",
	"\"parameters\"",
	"\"command\"",
	"\"cmd\"",
	"\"yield_time_ms\"",
	"\"explanation\"",
	"\"goal\"",
	"functions.run_in_terminal",
	"functions.apply_patch",
	"functions.task_complete",
	"functions.exec_command",
	"to=shell",
}

var inlineMarkers = []string{
	"to=functions.",
	"to=multi_tool_use.",
	"to=update_plan",
	"to=shell ",
	"to=shell\n",
	"recipient_name\":\"functions.",
	"recipient_name\":\"multi_tool_use.",
	"\"tool_uses\"",
	"*** begin patch",
	"*** end patch",
	"asypay_json_string",
}

func InternalTraceText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	lower := strings.ToLower(text)
	if !looksLikeInternalTrace(lower) {
		return text
	}

	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			out = append(out, line)
			continue
		}

		lowerTrimmed := strings.ToLower(trimmed)
		if isInternalTraceLine(lowerTrimmed) {
			continue
		}
		if clipped, ok := stripInlineInternalTraceSuffix(line); ok {
			if strings.TrimSpace(clipped) == "" {
				continue
			}
			out = append(out, clipped)
			continue
		}
		out = append(out, line)
	}

	cleaned := strings.TrimSpace(strings.Join(out, "\n"))
	if cleaned == "" {
		return ""
	}
	return cleaned
}

func looksLikeInternalTrace(lower string) bool {
	for _, marker := range strongMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	hits := 0
	for _, marker := range weakMarkers {
		if strings.Contains(lower, marker) {
			hits++
		}
	}
	return hits >= 3
}

func isInternalTraceLine(lowerLine string) bool {
	if strings.HasPrefix(lowerLine, "to=functions.") ||
		strings.HasPrefix(lowerLine, "to=multi_tool_use.") ||
		strings.HasPrefix(lowerLine, "to=shell") {
		return true
	}
	if (strings.Contains(lowerLine, "\"recipient_name\":\"functions.") ||
		strings.Contains(lowerLine, "\"recipient_name\":\"multi_tool_use.")) &&
		(strings.HasPrefix(lowerLine, "{") || strings.HasPrefix(lowerLine, "\"tool_uses\"")) {
		return true
	}
	if strings.Contains(lowerLine, "functions.run_in_terminal") ||
		strings.Contains(lowerLine, "functions.apply_patch") ||
		strings.Contains(lowerLine, "functions.task_complete") ||
		strings.Contains(lowerLine, "functions.exec_command") {
		if strings.HasPrefix(lowerLine, "{") || strings.HasPrefix(lowerLine, "\"") || strings.HasPrefix(lowerLine, "to=") {
			return true
		}
	}
	if strings.Contains(lowerLine, "*** begin patch") ||
		strings.Contains(lowerLine, "*** end patch") {
		return true
	}
	if strings.Contains(lowerLine, "\"tool_uses\"") && strings.Contains(lowerLine, "recipient_name") {
		return true
	}
	if strings.HasPrefix(lowerLine, "{\"cmd\"") {
		return true
	}
	if strings.Contains(lowerLine, "\"cmd\"") && strings.Contains(lowerLine, "\"yield_time_ms\"") {
		return true
	}
	if strings.Contains(lowerLine, "\"command\"") && strings.Contains(lowerLine, "\"explanation\"") {
		return true
	}
	return false
}

func stripInlineInternalTraceSuffix(line string) (string, bool) {
	lowerLine := strings.ToLower(line)
	first := -1
	for _, marker := range inlineMarkers {
		idx := strings.Index(lowerLine, marker)
		if idx <= 0 {
			continue
		}
		if first == -1 || idx < first {
			first = idx
		}
	}
	if first == -1 {
		return "", false
	}
	return strings.TrimRight(line[:first], " \t"), true
}
