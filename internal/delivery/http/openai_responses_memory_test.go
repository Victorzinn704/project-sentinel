package httpdelivery

import (
	"bytes"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestWriteResponsesSSEFromChatSSEDoesNotRetainHeapAfterGC(t *testing.T) {
	raw := syntheticChatSSE(240, strings.Repeat("x", 4096))

	runtime.GC()
	debug.FreeOSMemory()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < 20; i++ {
		if err := writeResponsesSSEFromChatSSE(ioDiscardWriter{}, raw, "sentinel-router"); err != nil {
			t.Fatalf("writeResponsesSSEFromChatSSE run %d: %v", i, err)
		}
	}

	runtime.GC()
	debug.FreeOSMemory()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	const maxExpectedGrowth = int64(28 << 20)
	if growth > maxExpectedGrowth {
		t.Fatalf("heap growth after GC too high: got %d bytes, want <= %d bytes", growth, maxExpectedGrowth)
	}
}

func BenchmarkWriteResponsesSSEFromChatSSEDeep(b *testing.B) {
	b.ReportAllocs()
	raw := syntheticChatSSE(240, strings.Repeat("bench", 1024))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := writeResponsesSSEFromChatSSE(ioDiscardWriter{}, raw, "sentinel-router"); err != nil {
			b.Fatalf("writeResponsesSSEFromChatSSE: %v", err)
		}
	}
}

type ioDiscardWriter struct{}

func (ioDiscardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func syntheticChatSSE(chunks int, piece string) []byte {
	var raw bytes.Buffer
	for i := 0; i < chunks; i++ {
		fmt.Fprintf(&raw, "data: {\"id\":\"chatcmpl_mem\",\"model\":\"sentinel-router\",\"created\":1713110400,\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", piece)
	}
	raw.WriteString("data: [DONE]\n\n")
	return raw.Bytes()
}