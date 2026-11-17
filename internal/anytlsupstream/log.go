package anytlsupstream

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/sagernet/sing/common/logger"
)

// singLogger writes the log of the AnyTLS library to an slog logger.  Trace
// messages are dropped, the rest keep their level, and fatal and panic
// messages are errors.
type singLogger struct {
	logger *slog.Logger
}

var _ logger.ContextLogger = (*singLogger)(nil)

func (l *singLogger) log(ctx context.Context, level slog.Level, args []any) {
	if !l.logger.Enabled(ctx, level) {
		return
	}

	l.logger.Log(ctx, level, fmt.Sprint(args...))
}

func (l *singLogger) Trace(args ...any) {}
func (l *singLogger) Debug(args ...any) { l.log(context.Background(), slog.LevelDebug, args) }
func (l *singLogger) Info(args ...any)  { l.log(context.Background(), slog.LevelInfo, args) }
func (l *singLogger) Warn(args ...any)  { l.log(context.Background(), slog.LevelWarn, args) }
func (l *singLogger) Error(args ...any) { l.log(context.Background(), slog.LevelError, args) }
func (l *singLogger) Fatal(args ...any) { l.log(context.Background(), slog.LevelError, args) }
func (l *singLogger) Panic(args ...any) { l.log(context.Background(), slog.LevelError, args) }

func (l *singLogger) TraceContext(ctx context.Context, args ...any) {}
func (l *singLogger) DebugContext(ctx context.Context, args ...any) {
	l.log(ctx, slog.LevelDebug, args)
}
func (l *singLogger) InfoContext(ctx context.Context, args ...any) {
	l.log(ctx, slog.LevelInfo, args)
}
func (l *singLogger) WarnContext(ctx context.Context, args ...any) {
	l.log(ctx, slog.LevelWarn, args)
}
func (l *singLogger) ErrorContext(ctx context.Context, args ...any) {
	l.log(ctx, slog.LevelError, args)
}
func (l *singLogger) FatalContext(ctx context.Context, args ...any) {
	l.log(ctx, slog.LevelError, args)
}
func (l *singLogger) PanicContext(ctx context.Context, args ...any) {
	l.log(ctx, slog.LevelError, args)
}
