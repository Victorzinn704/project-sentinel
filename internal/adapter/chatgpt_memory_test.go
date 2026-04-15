package adapter

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestTranslateNonStreamingResponseDoesNotRetainHeapAfterGC(t *testing.T) {
	deepText := strings.Repeat("mem-check ", 150000)
	raw := `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"` + deepText + `"}]}]}}`

	write := func([]byte) error { return nil }

	runtime.GC()
	debug.FreeOSMemory()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < 20; i++ {
		_, err := translateNonStreamingResponse(strings.NewReader(raw), "memcheck_req", "sentinel-router", write)
		if err != nil {
			t.Fatalf("translateNonStreamingResponse run %d: %v", i, err)
		}
	}

	runtime.GC()
	debug.FreeOSMemory()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	const maxExpectedGrowth = int64(24 << 20)
	if growth > maxExpectedGrowth {
		t.Fatalf("heap growth after GC too high: got %d bytes, want <= %d bytes", growth, maxExpectedGrowth)
	}
}

func BenchmarkTranslateNonStreamingResponseDeep(b *testing.B) {
	b.ReportAllocs()
	deepText := strings.Repeat("benchmark ", 120000)
	raw := `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"` + deepText + `"}]}]}}`

	write := func([]byte) error { return nil }
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := translateNonStreamingResponse(strings.NewReader(raw), "bench_req", "sentinel-router", write); err != nil {
			b.Fatalf("translateNonStreamingResponse: %v", err)
		}
	}
}