package voice

import "testing"

// What a line costs this plugin besides the vendor: cleaning the words and
// deciding whether it has said them before. Both run once per line for every
// driver on the grid.

func BenchmarkCleaningALine(b *testing.B) {
	const line = "  Brake twenty metres later into Turn 4 and ease to seventy-five percent by turn-in.  "
	b.ReportAllocs()
	for b.Loop() {
		if _, err := cleanText(line); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKeyingALine(b *testing.B) {
	cfg := configOf(settings(), secrets())
	req := requestFor(cfg, "Brake twenty metres later into Turn 4.", "")
	b.ReportAllocs()
	for b.Loop() {
		_ = cacheKey(req)
	}
}
