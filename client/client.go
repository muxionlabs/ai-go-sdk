package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultStreamTimeout is the default timeout for AI gateway stream sessions.
	DefaultStreamTimeout = 120

	// StopStreamTimeout is the timeout for stopping a stream session.
	StopStreamTimeout = 5

	// maxResponseBodySize limits how much of an error response body we read.
	maxResponseBodySize = 8 << 10 // 8KB

	// sseBufferSize is the initial buffer size for SSE scanning.
	sseBufferSize = 64 * 1024

	// sseMaxBufferSize is the maximum buffer size for SSE scanning.
	sseMaxBufferSize = 1024 * 1024
)

type streamStartRequest struct {
	StreamName string `json:"stream_name"`
	Params     string `json:"params"`
	StreamID   string `json:"stream_id"`
}

type streamStartResponse struct {
	StatusEndpoint string `json:"status_url"`
	DataStreamURL  string `json:"data_url"`
	UpdateEndpoint string `json:"update_url"`
	WHIPURL        string `json:"whip_url"`
	WHEPURL        string `json:"whep_url"`
	StopEndpoint   string `json:"stop_url"`
	StreamID       string `json:"stream_id"`
}

type StreamStartOptions struct {
	EnableVideoIngress bool `json:"enable_video_ingress"`
	EnableVideoEgress  bool `json:"enable_video_egress"`
	EnableDataOutput   bool `json:"enable_data_output"`
}

// GatewayEnvelope wraps request parameters in the Livepeer gateway header format.
type GatewayEnvelope struct {
	Request        string `json:"request"`
	ParametersJSON string `json:"parameters"`
	Capability     string `json:"capability"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// StartSession initiates a new transcription session with the AI gateway.
// It returns a StreamSession containing the endpoints for WHIP media ingress and transcript output.
func StartSession(ctx context.Context, cfg StreamConfig, streamName string) (*StreamSession, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	startCandidates := []string{
		base + "/process/stream/start",
		base + "/ai/stream/start",
	}

	env := GatewayEnvelope{
		Request:        "{}",
		ParametersJSON: mustJSON(StreamStartOptions{EnableVideoIngress: true, EnableVideoEgress: true, EnableDataOutput: true}),
		Capability:     cfg.Pipeline,
		TimeoutSeconds: DefaultStreamTimeout,
	}
	envBytes, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	livepeerHeader := base64.StdEncoding.EncodeToString(envBytes)

	paramsObj := map[string]any{
		"height": 720,
		"width":  1280,
	}
	paramsJSON := mustJSON(paramsObj)

	body := streamStartRequest{
		StreamName: streamName,
		Params:     paramsJSON,
		StreamID:   "",
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	var lastNotFoundStatus string
	var lastNotFoundBody string
	var lastErr error

	for _, startURL := range startCandidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Livepeer", livepeerHeader)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			lastNotFoundStatus = resp.Status
			lastNotFoundBody = string(b)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("start stream failed: %s: %s", resp.Status, string(b))
		}

		var sr streamStartResponse
		if err := json.Unmarshal(b, &sr); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}

		if sr.StreamID == "" {
			return nil, fmt.Errorf("start response missing stream_id")
		}

		session := &StreamSession{
			ID:             sr.StreamID,
			StopEndpoint:   sr.StopEndpoint,
			StatusEndpoint: sr.StatusEndpoint,
			DataStreamURL:  sr.DataStreamURL,
			UpdateEndpoint: sr.UpdateEndpoint,
			WHIPURL:        sr.WHIPURL,
			WHEPURL:        sr.WHEPURL,
		}

		normalizeBase := strings.TrimRight(cfg.BaseURL, "/")
		session.StopEndpoint = normalizeGatewayURL(normalizeBase, session.StopEndpoint)
		session.StatusEndpoint = normalizeGatewayURL(normalizeBase, session.StatusEndpoint)
		session.DataStreamURL = normalizeGatewayURL(normalizeBase, session.DataStreamURL)
		session.UpdateEndpoint = normalizeGatewayURL(normalizeBase, session.UpdateEndpoint)
		session.WHIPURL = normalizeGatewayURL(normalizeBase, session.WHIPURL)
		session.WHEPURL = normalizeGatewayURL(normalizeBase, session.WHEPURL)

		return session, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("do request: %w", lastErr)
	}
	return nil, fmt.Errorf("start stream failed: %s: %s", lastNotFoundStatus, lastNotFoundBody)
}

// StopSession terminates an active transcription session.
func StopSession(ctx context.Context, cfg StreamConfig, streamID string) error {
	if streamID == "" {
		return fmt.Errorf("empty streamID")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	stopCandidates := []string{
		base + "/process/stream/" + streamID + "/stop",
		base + "/ai/stream/" + streamID + "/stop",
	}

	env := GatewayEnvelope{
		Request:        mustJSON(map[string]string{"stream_id": streamID}),
		ParametersJSON: mustJSON(map[string]any{}),
		Capability:     cfg.Pipeline,
		TimeoutSeconds: StopStreamTimeout,
	}
	envBytes, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	livepeerHeader := base64.StdEncoding.EncodeToString(envBytes)

	bodyObj := map[string]string{"stream_id": streamID}
	bodyBytes, err := json.Marshal(bodyObj)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	var lastNotFoundStatus string
	var lastNotFoundBody string
	var lastErr error

	for _, stopURL := range stopCandidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, stopURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Livepeer", livepeerHeader)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			lastNotFoundStatus = resp.Status
			lastNotFoundBody = string(b)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("stop stream failed: %s: %s", resp.Status, string(b))
		}
		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("do request: %w", lastErr)
	}
	return fmt.Errorf("stop stream failed: %s: %s", lastNotFoundStatus, lastNotFoundBody)
}

// StreamTranscriptEvents connects to the SSE data stream and invokes handler for each transcript event.
// It blocks until the context is cancelled or the stream ends.
func StreamTranscriptEvents(ctx context.Context, dataStreamURL string, handler TranscriptEventHandler, logger Logger) error {
	l := ensureLogger(logger)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dataStreamURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
		return fmt.Errorf("data stream failed: %s: %s", resp.Status, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, sseBufferSize)
	scanner.Buffer(buf, sseMaxBufferSize)

	var eventBuf strings.Builder

	flushEvent := func() {
		if eventBuf.Len() == 0 {
			return
		}
		data := strings.TrimSpace(eventBuf.String())
		if data == "" {
			eventBuf.Reset()
			return
		}

		event, err := parseSSEPayload(data)
		if err != nil {
			l.Debug(ctx, "failed to parse SSE payload", "error", err)
			eventBuf.Reset()
			return
		}

		event.ReceivedAt = time.Now()
		handler(ctx, event)
		eventBuf.Reset()
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			flushEvent()
			return ctx.Err()
		default:
		}

		line := scanner.Text()

		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(line[len("data:"):])
			if eventBuf.Len() > 0 {
				eventBuf.WriteByte('\n')
			}
			eventBuf.WriteString(payload)
		} else if strings.TrimSpace(line) == "" {
			flushEvent()
		}
	}

	if err := scanner.Err(); err != nil && !isContextError(err, ctx) {
		return fmt.Errorf("scanner error: %w", err)
	}

	flushEvent()
	return nil
}

func normalizeGatewayURL(base, raw string) string {
	if raw == "" || base == "" {
		return raw
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return raw
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		ref, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		return baseURL.ResolveReference(ref).String()
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Scheme == "" || u.Host == "" {
		ref, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		return baseURL.ResolveReference(ref).String()
	}
	u.Scheme = baseURL.Scheme
	u.Host = baseURL.Host
	return u.String()
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func parseSSEPayload(data string) (TranscriptEvent, error) {
	var outer []string
	if err := json.Unmarshal([]byte(data), &outer); err != nil {
		return TranscriptEvent{}, fmt.Errorf("unmarshal SSE outer payload: %w", err)
	}
	if len(outer) != 1 {
		return TranscriptEvent{}, fmt.Errorf("unexpected SSE outer payload length: %d", len(outer))
	}

	inner := strings.TrimSpace(outer[0])
	if inner == "" {
		return TranscriptEvent{}, fmt.Errorf("empty SSE inner payload")
	}

	var event TranscriptEvent
	if err := json.Unmarshal([]byte(inner), &event); err != nil {
		return TranscriptEvent{}, fmt.Errorf("unmarshal SSE inner transcript event: %w", err)
	}
	return event, nil
}

func isContextError(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return true
	}
	if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "use of closed network connection") {
		return true
	}
	return false
}
