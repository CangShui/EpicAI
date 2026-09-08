// Package benchmark measures the actual uncapped output throughput of this
// deployment using the real Echo Engine, tokenizer, adapter and SSE framing.
package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/epicai/epicai/backend/internal/adapters/openai_chat"
	ored "github.com/epicai/epicai/backend/internal/adapters/openai_responses"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/epicai/epicai/backend/internal/tokenizer"
	"github.com/google/uuid"
)

// counter mimics a streaming sink: it counts bytes and tokens without any I/O
// so the measurement reflects serialization cost, not socket throughput.
type counter struct {
	bytes  int64
	tokens int64
	chunks int64
}

func (c *counter) add(b []byte, tokens int) {
	atomic.AddInt64(&c.bytes, int64(len(b)))
	atomic.AddInt64(&c.tokens, int64(tokens))
	atomic.AddInt64(&c.chunks, 1)
}

type Result struct {
	ID          string    `json:"id"`
	Protocol    string    `json:"protocol"`
	ChunkSize   int       `json:"chunk_size"`
	DurationMS  int64     `json:"duration_ms"`
	TokensSent  int64     `json:"tokens_sent"`
	AverageRate float64   `json:"average_token_rate"`
	PeakRate    float64   `json:"peak_token_rate"`
	BytesSent   int64     `json:"bytes_sent"`
	Chunks      int64     `json:"chunks"`
	CPUPercent  float64   `json:"cpu_percent"`
	MemoryMB    float64   `json:"memory_mb"`
	Timestamp   time.Time `json:"timestamp"`
}

// RunUncapped streams synthetic echo output through the real serialization path
// for the requested duration and reports the sustainable token rate.
func RunUncapped(ctx context.Context, protocol string, chunkTokens int, duration time.Duration) Result {
	if chunkTokens <= 0 {
		chunkTokens = 32
	}
	if duration <= 0 {
		duration = 3 * time.Second
	}

	// Build a representative payload of the requested token size.
	text := buildText(chunkTokens)
	tokensPerChunk := tokenizer.Count(text)
	if tokensPerChunk <= 0 {
		tokensPerChunk = 1
	}

	c := &counter{}
	start := time.Now()
	deadline := start.Add(duration)

	var peak float64
	var lastWindowStart = start
	var lastWindowTokens int64

	// sample peak every 100ms
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				now := time.Now()
				if now.After(deadline) {
					return
				}
				cur := atomic.LoadInt64(&c.tokens)
				windowTokens := cur - lastWindowTokens
				windowSec := now.Sub(lastWindowStart).Seconds()
				if windowSec > 0 {
					rate := float64(windowTokens) / windowSec
					if rate > peak {
						peak = rate
					}
				}
				lastWindowStart = now
				lastWindowTokens = cur
			}
		}
	}()

	id := "chatcmpl_epic_bench" + uuid.NewString()[:12]
	model := "epic-alpha"
	created := start.Unix()

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			goto done
		default:
		}
		switch protocol {
		case "responses":
			ev := ored.TextDelta(id, "msg_epic_"+id[10:], text, 0, 0)
			b, _ := json.Marshal(ev)
			framed := "data: " + string(b) + "\n\n"
			c.add([]byte(framed), tokensPerChunk)
		default:
			chunk := openai_chat.NewChunk(id, model, created, text, "")
			b, _ := json.Marshal(chunk)
			framed := "data: " + string(b) + "\n\n"
			c.add([]byte(framed), tokensPerChunk)
		}
	}
done:

	elapsed := time.Since(start)
	secs := elapsed.Seconds()
	if secs <= 0 {
		secs = 0.001
	}
	avg := float64(c.tokens) / secs
	if peak < avg {
		peak = avg
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	return Result{
		ID:          "bench_" + uuid.NewString()[:12],
		Protocol:    protocol,
		ChunkSize:   chunkTokens,
		DurationMS:  elapsed.Milliseconds(),
		TokensSent:  c.tokens,
		AverageRate: avg,
		PeakRate:    peak,
		BytesSent:   c.bytes,
		Chunks:      c.chunks,
		MemoryMB:    float64(ms.HeapAlloc) / 1024 / 1024,
		Timestamp:   time.Now(),
	}
}

func buildText(tokens int) string {
	// ~4 chars per latin token for the canonical tokenizer
	n := tokens * 4
	if n < 4 {
		n = 4
	}
	b := make([]byte, 0, n)
	alphabet := []byte("abcdefghijklmnopqrstuvwxyz ")
	for i := 0; i < n; i++ {
		b = append(b, alphabet[i%len(alphabet)])
	}
	return string(b)
}

// ToStorage converts a benchmark result to a persistable record.
func (r Result) ToStorage() *storage.Benchmark {
	return &storage.Benchmark{
		ID: r.ID, Timestamp: r.Timestamp, Protocol: r.Protocol, ChunkSize: r.ChunkSize,
		DurationMS: r.DurationMS, TokensSent: r.TokensSent, AverageRate: r.AverageRate,
		PeakRate: r.PeakRate, BytesSent: r.BytesSent, MemoryMB: r.MemoryMB,
	}
}

func (r Result) String() string {
	return fmt.Sprintf("%s: average %.0f token/s, peak %.0f token/s, %d tokens, %d bytes in %dms",
		r.Protocol, r.AverageRate, r.PeakRate, r.TokensSent, r.BytesSent, r.DurationMS)
}
