package client

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MediaSample represents a media sample (video or audio) to be sent to the AI gateway.
type MediaSample struct {
	Data     []byte
	Duration time.Duration
}

// TranscriptionSession provides a high-level interface for AI gateway transcription.
// It manages the session lifecycle, WHIP publishing, SSE transcript streaming,
// and optional transcript storage in a single unified API.
type TranscriptionSession struct {
	ctx    context.Context
	cancel context.CancelFunc

	cfg        StreamConfig
	streamName string
	logger     Logger

	session   *StreamSession
	publisher *WHIPPublisher
	store     *TranscriptStore

	handlers   []TranscriptEventHandler
	handlersMu sync.RWMutex

	storeKey    string
	storeEvents bool

	startedMu sync.Mutex
	started   bool
	stopped   bool

	sseErr     error
	sseErrOnce sync.Once
}

// SessionOption configures a TranscriptionSession.
type SessionOption func(*TranscriptionSession)

// WithLogger sets the logger for the session.
func WithLogger(logger Logger) SessionOption {
	return func(s *TranscriptionSession) {
		s.logger = logger
	}
}

// WithTranscriptHandler adds a callback that will be invoked for each transcript event.
// Multiple handlers can be added.
func WithTranscriptHandler(handler TranscriptEventHandler) SessionOption {
	return func(s *TranscriptionSession) {
		s.handlers = append(s.handlers, handler)
	}
}

// WithTranscriptStore enables storing transcripts in the provided store.
// The storeKey is used to identify this session's transcripts in the store.
func WithTranscriptStore(store *TranscriptStore, storeKey string) SessionOption {
	return func(s *TranscriptionSession) {
		s.store = store
		s.storeKey = storeKey
		s.storeEvents = true
	}
}

// WithBuiltinStore creates and uses an internal TranscriptStore.
// The storeKey is used to identify this session's transcripts.
func WithBuiltinStore(storeKey string, opts ...TranscriptStoreOption) SessionOption {
	return func(s *TranscriptionSession) {
		s.store = NewTranscriptStore(opts...)
		s.storeKey = storeKey
		s.storeEvents = true
	}
}

// NewTranscriptionSession creates a new transcription session but does not start it.
// Call Start() to initiate the connection to the AI gateway.
func NewTranscriptionSession(ctx context.Context, cfg StreamConfig, streamName string, opts ...SessionOption) *TranscriptionSession {
	ctx, cancel := context.WithCancel(ctx)
	s := &TranscriptionSession{
		ctx:        ctx,
		cancel:     cancel,
		cfg:        cfg,
		streamName: streamName,
		logger:     NoopLogger{},
		handlers:   make([]TranscriptEventHandler, 0),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start initiates the AI gateway session, starts the WHIP publisher,
// and begins listening for transcript events via SSE.
func (s *TranscriptionSession) Start() error {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()

	if s.started {
		return nil
	}
	if s.stopped {
		return fmt.Errorf("session already stopped")
	}

	// Start the AI gateway session
	session, err := StartSession(s.ctx, s.cfg, s.streamName)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	s.session = session

	s.logger.Log(s.ctx, "AI gateway session started",
		"streamID", session.ID,
		"dataURL", session.DataStreamURL,
		"streamName", s.streamName,
	)

	if session.WHIPURL == "" {
		_ = StopSession(context.Background(), s.cfg, session.ID)
		return fmt.Errorf("no WHIP URL provided by gateway")
	}

	// Start WHIP publisher
	s.publisher = NewWHIPPublisher(s.ctx, session.WHIPURL, s.logger)
	if err := s.publisher.Start(); err != nil {
		_ = StopSession(context.Background(), s.cfg, session.ID)
		return fmt.Errorf("start WHIP publisher: %w", err)
	}

	// Start SSE listener for transcripts
	s.startSSE()

	s.started = true
	return nil
}

// startSSE starts a goroutine to listen for transcript events.
func (s *TranscriptionSession) startSSE() {
	go func() {
		if s.session.DataStreamURL == "" {
			s.logger.Warn(s.ctx, "no data_url returned from AI gateway, skipping SSE")
			return
		}

		err := StreamTranscriptEvents(s.ctx, s.session.DataStreamURL, func(ctx context.Context, event TranscriptEvent) {
			s.logger.Debug(ctx, "received transcript event",
				"type", event.Type,
				"timestamp_utc", event.TimestampUTC,
				"segments", len(event.Segments),
			)

			// Store event if configured
			if s.storeEvents && s.store != nil {
				s.store.AddEvent(ctx, s.storeKey, event)
			}

			// Invoke all registered handlers
			s.handlersMu.RLock()
			handlers := s.handlers
			s.handlersMu.RUnlock()

			for _, handler := range handlers {
				handler(ctx, event)
			}
		}, s.logger)

		if err != nil && s.ctx.Err() == nil {
			s.sseErrOnce.Do(func() {
				s.sseErr = err
				s.logger.Error(s.ctx, "SSE reader error", "error", err)
			})
		}
	}()
}

// WriteVideoSample sends a video sample to the AI gateway via WHIP.
// Returns an error if the session is not started or the write fails.
func (s *TranscriptionSession) WriteVideoSample(data []byte, duration time.Duration) error {
	if s.publisher == nil {
		return fmt.Errorf("session not started")
	}
	return s.publisher.WriteVideoSample(data, duration)
}

// WriteAudioSample sends an audio sample to the AI gateway via WHIP.
// Returns an error if the session is not started or the write fails.
func (s *TranscriptionSession) WriteAudioSample(data []byte, duration time.Duration) error {
	if s.publisher == nil {
		return fmt.Errorf("session not started")
	}
	return s.publisher.WriteAudioSample(data, duration)
}

// WriteSample sends a media sample to the appropriate track based on type.
// This is a convenience method when you have MediaSample structs.
func (s *TranscriptionSession) WriteVideoMediaSample(sample MediaSample) error {
	return s.WriteVideoSample(sample.Data, sample.Duration)
}

// WriteAudioMediaSample sends an audio MediaSample.
func (s *TranscriptionSession) WriteAudioMediaSample(sample MediaSample) error {
	return s.WriteAudioSample(sample.Data, sample.Duration)
}

// GetTranscripts returns all stored transcript segments for this session.
// Returns nil if no store is configured or no segments exist.
func (s *TranscriptionSession) GetTranscripts() []TranscriptSegment {
	if s.store == nil {
		return nil
	}
	return s.store.GetSegments(s.storeKey)
}

// TranscriptCount returns the number of stored transcript segments.
func (s *TranscriptionSession) TranscriptCount() int {
	if s.store == nil {
		return 0
	}
	return s.store.Count(s.storeKey)
}

// AddHandler adds a transcript event handler at runtime.
// The handler will be invoked for all future transcript events.
func (s *TranscriptionSession) AddHandler(handler TranscriptEventHandler) {
	s.handlersMu.Lock()
	defer s.handlersMu.Unlock()
	s.handlers = append(s.handlers, handler)
}

// Session returns the underlying StreamSession.
// Returns nil if the session has not been started.
func (s *TranscriptionSession) Session() *StreamSession {
	return s.session
}

// Store returns the transcript store, if configured.
func (s *TranscriptionSession) Store() *TranscriptStore {
	return s.store
}

// Stop terminates the transcription session.
// It stops the WHIP publisher, closes the SSE connection, and stops the AI gateway session.
// If a store is configured, transcripts are cleared.
func (s *TranscriptionSession) Stop() error {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()

	if s.stopped {
		return nil
	}
	s.stopped = true

	// Cancel context to stop SSE and other goroutines
	s.cancel()

	// Stop WHIP publisher
	if s.publisher != nil {
		s.publisher.Stop()
	}

	// Stop AI gateway session
	var stopErr error
	if s.session != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := StopSession(stopCtx, s.cfg, s.session.ID); err != nil {
			s.logger.Error(s.ctx, "failed to stop AI gateway session", "error", err)
			stopErr = err
		} else {
			s.logger.Log(s.ctx, "AI gateway session stopped", "streamID", s.session.ID)
		}
	}

	// Clear stored transcripts
	if s.storeEvents && s.store != nil {
		s.store.Clear(s.storeKey)
	}

	return stopErr
}

// StopWithoutClear terminates the session but preserves stored transcripts.
func (s *TranscriptionSession) StopWithoutClear() error {
	// Temporarily disable store clearing
	storeEvents := s.storeEvents
	s.storeEvents = false
	err := s.Stop()
	s.storeEvents = storeEvents
	return err
}

// SSEError returns any error that occurred during SSE streaming.
// Returns nil if no error occurred or streaming is still active.
func (s *TranscriptionSession) SSEError() error {
	return s.sseErr
}

// IsStarted returns true if the session has been started.
func (s *TranscriptionSession) IsStarted() bool {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()
	return s.started
}

// IsStopped returns true if the session has been stopped.
func (s *TranscriptionSession) IsStopped() bool {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()
	return s.stopped
}
