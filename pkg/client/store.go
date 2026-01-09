package client

import (
	"context"
	"strings"
	"sync"
)

const (
	// DefaultMaxSegments is the default maximum number of segments to retain per key.
	DefaultMaxSegments = 1000
)

// TranscriptStore provides thread-safe storage for transcript segments (timed cues)
// keyed by an arbitrary string (e.g., stream name, user ID).
// Segments are limited to MaxSegments per key to prevent unbounded memory growth.
type TranscriptStore struct {
	mu          sync.RWMutex
	segs        map[string][]TranscriptSegment
	maxSegments int
	logger      Logger
}

// TranscriptStoreOption configures a TranscriptStore.
type TranscriptStoreOption func(*TranscriptStore)

// WithMaxSegments sets the maximum number of segments to retain per key.
func WithMaxSegments(max int) TranscriptStoreOption {
	return func(ts *TranscriptStore) {
		if max > 0 {
			ts.maxSegments = max
		}
	}
}

// WithStoreLogger sets the logger for the transcript store.
func WithStoreLogger(logger Logger) TranscriptStoreOption {
	return func(ts *TranscriptStore) {
		ts.logger = logger
	}
}

// NewTranscriptStore creates a new empty TranscriptStore.
func NewTranscriptStore(opts ...TranscriptStoreOption) *TranscriptStore {
	ts := &TranscriptStore{
		segs:        make(map[string][]TranscriptSegment),
		maxSegments: DefaultMaxSegments,
		logger:      NoopLogger{},
	}
	for _, opt := range opts {
		opt(ts)
	}
	return ts
}

// AddEvent ingests a transcript event for the given key.
// If the event contains structured Segments, they are added as timed cues.
// Empty segments (whitespace-only text) are skipped.
// If the segment count exceeds MaxSegments, older segments are discarded.
func (ts *TranscriptStore) AddEvent(ctx context.Context, key string, event TranscriptEvent) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, ok := ts.segs[key]; !ok {
		ts.segs[key] = make([]TranscriptSegment, 0, ts.maxSegments)
	}

	addSeg := func(seg TranscriptSegment) {
		if seg.EndMS <= seg.StartMS {
			return
		}
		ts.segs[key] = append(ts.segs[key], seg)
		if len(ts.segs[key]) > ts.maxSegments {
			removeCount := len(ts.segs[key]) - ts.maxSegments
			ts.segs[key] = ts.segs[key][removeCount:]
		}
	}

	if len(event.Segments) == 0 {
		return
	}

	for _, seg := range event.Segments {
		if strings.TrimSpace(seg.Text) == "" {
			ts.logger.Debug(ctx, "skipping empty transcript segment",
				"id", seg.ID, "text", seg.Text, "startMs", seg.StartMS, "endMs", seg.EndMS)
			continue
		}
		ts.logger.Debug(ctx, "adding transcript segment",
			"id", seg.ID, "text", seg.Text, "startMs", seg.StartMS, "endMs", seg.EndMS)
		addSeg(seg)
	}
}

// GetSegments returns a copy of all transcript segments for the given key.
// Returns nil if no segments exist for the key.
func (ts *TranscriptStore) GetSegments(key string) []TranscriptSegment {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	segs, ok := ts.segs[key]
	if !ok {
		return nil
	}

	result := make([]TranscriptSegment, len(segs))
	copy(result, segs)
	return result
}

// Clear removes all transcript segments for the given key.
func (ts *TranscriptStore) Clear(key string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.segs, key)
}

// ClearAll removes all transcript segments for all keys.
func (ts *TranscriptStore) ClearAll() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.segs = make(map[string][]TranscriptSegment)
}

// Keys returns a list of all keys that have segments stored.
func (ts *TranscriptStore) Keys() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	keys := make([]string, 0, len(ts.segs))
	for k := range ts.segs {
		keys = append(keys, k)
	}
	return keys
}

// Count returns the number of segments stored for the given key.
func (ts *TranscriptStore) Count(key string) int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.segs[key])
}
