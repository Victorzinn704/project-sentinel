package adapter

import "testing"

func TestSanitizeInternalTraceTextRemovesInternalToolLines(t *testing.T) {
	raw := "Resposta\nto=functions.run_in_terminal\n{\"recipient_name\":\"functions.apply_patch\",\"parameters\":{}}\nFinal"
	got := sanitizeInternalTraceText(raw)

	if got != "Resposta\nFinal" {
		t.Fatalf("sanitizeInternalTraceText() = %q, want %q", got, "Resposta\\nFinal")
	}
}

func TestSanitizeInternalTraceTextPreservesRegularAnswer(t *testing.T) {
	raw := "Passo 1: execute o comando\nPasso 2: valide o retorno"
	got := sanitizeInternalTraceText(raw)

	if got != raw {
		t.Fatalf("sanitizeInternalTraceText() changed regular text: %q", got)
	}
}

func TestSanitizeInternalTraceTextRemovesInlineLegacyExecCommandTrace(t *testing.T) {
	raw := "Vou checar as notas\nAgora vou ler a orientacao do repo. to=functions.exec_command\n{\"cmd\":\"Get-Content x\",\"yield_time_ms\":10000}\nOi! Como posso ajudar?"
	got := sanitizeInternalTraceText(raw)

	want := "Vou checar as notas\nAgora vou ler a orientacao do repo.\nOi! Como posso ajudar?"
	if got != want {
		t.Fatalf("sanitizeInternalTraceText() = %q, want %q", got, want)
	}
}