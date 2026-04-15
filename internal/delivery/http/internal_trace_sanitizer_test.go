package httpdelivery

import "testing"

func TestResponsesSanitizerRemovesInternalToolLines(t *testing.T) {
	raw := "inicio\n\"tool_uses\": [{\"recipient_name\":\"functions.run_in_terminal\",\"parameters\":{\"command\":\"x\",\"explanation\":\"y\"}}]\nfim"
	got := sanitizeInternalTraceText(raw)

	if got != "inicio\nfim" {
		t.Fatalf("sanitizeInternalTraceText() = %q, want %q", got, "inicio\\nfim")
	}
}

func TestResponsesSanitizerKeepsRegularText(t *testing.T) {
	raw := "resultado final com instrucoes para o usuario"
	got := sanitizeInternalTraceText(raw)

	if got != raw {
		t.Fatalf("sanitizeInternalTraceText() changed regular text: %q", got)
	}
}

func TestResponsesSanitizerRemovesInlineLegacyExecCommandTrace(t *testing.T) {
	raw := "Vou checar as notas\nAgora vou ler a orientacao do repo. to=functions.exec_command\n{\"cmd\":\"pwd\"}\nOi! Como posso ajudar no projeto?"
	got := sanitizeInternalTraceText(raw)

	want := "Vou checar as notas\nAgora vou ler a orientacao do repo.\nOi! Como posso ajudar no projeto?"
	if got != want {
		t.Fatalf("sanitizeInternalTraceText() = %q, want %q", got, want)
	}
}