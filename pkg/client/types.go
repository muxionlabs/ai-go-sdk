package client

import (
	"context"
	"time"
)

// StreamConfig holds the configuration for connecting to an AI gateway.
type StreamConfig struct {
	// BaseURL is the base URL of the AI gateway (e.g., "http://localhost:5937").
	BaseURL string

	// Pipeline is the AI pipeline capability name (e.g., "transcriber").
	Pipeline string

	// EnableVideoIngress controls whether the gateway should accept video input
	// for the session. Defaults to true if nil.
	EnableVideoIngress *bool

	// EnableVideoEgress controls whether the gateway should produce video output
	// for the session. Defaults to true if nil.
	EnableVideoEgress *bool

	// EnableDataOutput controls whether the gateway should produce data output
	// (e.g. transcripts). Defaults to true if nil.
	EnableDataOutput *bool
}

// StreamSession represents an active AI gateway transcription session.
type StreamSession struct {
	// ID is the unique identifier for this session.
	ID string

	// StopEndpoint is the URL for stopping the session (if provided by gateway).
	StopEndpoint string

	// StatusEndpoint is the URL to check session status.
	StatusEndpoint string

	// DataStreamURL is the SSE endpoint for receiving transcript events.
	DataStreamURL string

	// UpdateEndpoint is the URL for sending session updates.
	UpdateEndpoint string

	// WHIPURL is the WHIP endpoint for WebRTC media ingress.
	WHIPURL string

	// WHEPURL is the WHEP endpoint for WebRTC media egress (if available).
	WHEPURL string
}

// TranscriptEvent represents a single transcription event from the AI gateway.
type TranscriptEvent struct {
	// Type is the event type (e.g., "transcript").
	Type string `json:"type"`

	// TimestampUTC is the wall-clock timestamp when the transcript was generated.
	// The SSE format uses an RFC3339 timestamp string.
	TimestampUTC *time.Time `json:"timestamp_utc,omitempty"`

	Timing *Timing `json:"timing,omitempty"`

	// Stats contains optional performance statistics for this transcription.
	Stats *Stats `json:"stats,omitempty"`

	// ReceivedAt is when the client received this event (not from JSON).
	ReceivedAt time.Time `json:"-"`

	// Segments contains the structured transcript payload with explicit media-clock timestamps.
	Segments []TranscriptSegment `json:"segments,omitempty"`
}

// TranscriptSegment represents a timed subtitle unit (phrase/line) in media-clock time.
// Times are stream-relative milliseconds.
type TranscriptSegment struct {
	ID      string          `json:"id"`
	StartMS int64           `json:"start_ms"`
	EndMS   int64           `json:"end_ms"`
	Text    string          `json:"text"`
	Words   []WordTimestamp `json:"words,omitempty"`
}

// WordTimestamp is an optional word-level timing payload.
// Times are stream-relative milliseconds.
type WordTimestamp struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

type Timing struct {
	MediaWindowStartMS int64 `json:"media_window_start_ms"`
	MediaWindowEndMS   int64 `json:"media_window_end_ms"`
}

// Stats contains performance statistics for a transcription event.
type Stats struct {
	AudioDurationMS int `json:"audio_duration_ms"`
}

// TranscriptEventHandler is a callback function for processing transcript events.
type TranscriptEventHandler func(ctx context.Context, event TranscriptEvent)
