package adapter

import "github.com/seu-usuario/project-sentinel/internal/platform/sanitize"

func sanitizeInternalTraceText(text string) string {
	return sanitize.InternalTraceText(text)
}
