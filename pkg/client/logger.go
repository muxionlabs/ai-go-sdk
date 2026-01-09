package client

import "context"

// Logger defines the minimal logging surface used by the SDK.
// Applications can adapt their own logger to this interface.
type Logger interface {
	Debug(ctx context.Context, msg string, args ...any)
	Log(ctx context.Context, msg string, args ...any)
	Warn(ctx context.Context, msg string, args ...any)
	Error(ctx context.Context, msg string, args ...any)
}

// NoopLogger is the default logger when none is provided.
type NoopLogger struct{}

func (NoopLogger) Debug(context.Context, string, ...any) {}
func (NoopLogger) Log(context.Context, string, ...any)   {}
func (NoopLogger) Warn(context.Context, string, ...any)  {}
func (NoopLogger) Error(context.Context, string, ...any) {}

func ensureLogger(l Logger) Logger {
	if l != nil {
		return l
	}
	return NoopLogger{}
}
