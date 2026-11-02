package logx

import (
	"fmt"
	"io"

	fiberlog "github.com/gofiber/fiber/v3/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// FiberLogger adapts logx to Fiber's AllLogger interface,
// replacing Fiber's internal default logger with zap.
type FiberLogger struct {
	logger *zap.Logger
	sugar  *zap.SugaredLogger
	level  fiberlog.Level
}

// compile-time interface check
var _ fiberlog.AllLogger[*zap.Logger] = (*FiberLogger)(nil)

// NewFiberLogger creates a FiberLogger backed by the global logx logger.
func NewFiberLogger() *FiberLogger {
	l := Logger()
	return &FiberLogger{
		logger: l,
		sugar:  l.Sugar(),
		level:  fiberlog.LevelDebug,
	}
}

// SetupFiberLogger replaces Fiber's default logger with our logx-based implementation.
// Call this after logx.Init() and before creating Fiber app.
func SetupFiberLogger() {
	fiberlog.SetLogger(NewFiberLogger())
}

// --- ConfigurableLogger[*zap.Logger] ---

func (l *FiberLogger) SetLevel(level fiberlog.Level) {
	l.level = level
}

func (l *FiberLogger) SetOutput(_ io.Writer) {
	// no-op: output is managed by logx/lumberjack
}

func (l *FiberLogger) Logger() *zap.Logger {
	return l.logger
}

// --- WithContext ---

func (l *FiberLogger) WithContext(_ any) fiberlog.CommonLogger {
	// Return self; context enrichment is handled by our LogMiddleware
	return l
}

// --- Logger interface ---

func (l *FiberLogger) Trace(v ...any) {
	if l.level > fiberlog.LevelTrace {
		return
	}
	l.logger.Debug(fmt.Sprint(v...), zap.String("fiberLevel", "trace"))
}

func (l *FiberLogger) Debug(v ...any) {
	if l.level > fiberlog.LevelDebug {
		return
	}
	l.logger.Debug(fmt.Sprint(v...))
}

func (l *FiberLogger) Info(v ...any) {
	if l.level > fiberlog.LevelInfo {
		return
	}
	l.logger.Info(fmt.Sprint(v...))
}

func (l *FiberLogger) Warn(v ...any) {
	if l.level > fiberlog.LevelWarn {
		return
	}
	l.logger.Warn(fmt.Sprint(v...))
}

func (l *FiberLogger) Error(v ...any) {
	if l.level > fiberlog.LevelError {
		return
	}
	l.logger.Error(fmt.Sprint(v...))
}

func (l *FiberLogger) Fatal(v ...any) {
	if l.level > fiberlog.LevelFatal {
		return
	}
	l.logger.Fatal(fmt.Sprint(v...))
}

func (l *FiberLogger) Panic(v ...any) {
	if l.level > fiberlog.LevelPanic {
		return
	}
	l.logger.Panic(fmt.Sprint(v...))
}

// --- FormatLogger interface ---

func (l *FiberLogger) Tracef(format string, v ...any) {
	if l.level > fiberlog.LevelTrace {
		return
	}
	l.logger.Debug(fmt.Sprintf(format, v...), zap.String("fiberLevel", "trace"))
}

func (l *FiberLogger) Debugf(format string, v ...any) {
	if l.level > fiberlog.LevelDebug {
		return
	}
	l.logger.Debug(fmt.Sprintf(format, v...))
}

func (l *FiberLogger) Infof(format string, v ...any) {
	if l.level > fiberlog.LevelInfo {
		return
	}
	l.logger.Info(fmt.Sprintf(format, v...))
}

func (l *FiberLogger) Warnf(format string, v ...any) {
	if l.level > fiberlog.LevelWarn {
		return
	}
	l.logger.Warn(fmt.Sprintf(format, v...))
}

func (l *FiberLogger) Errorf(format string, v ...any) {
	if l.level > fiberlog.LevelError {
		return
	}
	l.logger.Error(fmt.Sprintf(format, v...))
}

func (l *FiberLogger) Fatalf(format string, v ...any) {
	if l.level > fiberlog.LevelFatal {
		return
	}
	l.logger.Fatal(fmt.Sprintf(format, v...))
}

func (l *FiberLogger) Panicf(format string, v ...any) {
	if l.level > fiberlog.LevelPanic {
		return
	}
	l.logger.Panic(fmt.Sprintf(format, v...))
}

// --- WithLogger interface ---

func (l *FiberLogger) Tracew(msg string, keysAndValues ...any) {
	if l.level > fiberlog.LevelTrace {
		return
	}
	l.sugar.Debugw(msg, append([]any{"fiberLevel", "trace"}, keysAndValues...)...)
}

func (l *FiberLogger) Debugw(msg string, keysAndValues ...any) {
	if l.level > fiberlog.LevelDebug {
		return
	}
	l.sugar.Debugw(msg, keysAndValues...)
}

func (l *FiberLogger) Infow(msg string, keysAndValues ...any) {
	if l.level > fiberlog.LevelInfo {
		return
	}
	l.sugar.Infow(msg, keysAndValues...)
}

func (l *FiberLogger) Warnw(msg string, keysAndValues ...any) {
	if l.level > fiberlog.LevelWarn {
		return
	}
	l.sugar.Warnw(msg, keysAndValues...)
}

func (l *FiberLogger) Errorw(msg string, keysAndValues ...any) {
	if l.level > fiberlog.LevelError {
		return
	}
	l.sugar.Errorw(msg, keysAndValues...)
}

func (l *FiberLogger) Fatalw(msg string, keysAndValues ...any) {
	if l.level > fiberlog.LevelFatal {
		return
	}
	l.sugar.Fatalw(msg, keysAndValues...)
}

func (l *FiberLogger) Panicw(msg string, keysAndValues ...any) {
	if l.level > fiberlog.LevelPanic {
		return
	}
	l.sugar.Panicw(msg, keysAndValues...)
}

// fiberToZapLevel converts Fiber log level to zap level (unused but kept for reference).
func fiberToZapLevel(level fiberlog.Level) zapcore.Level {
	switch level {
	case fiberlog.LevelTrace, fiberlog.LevelDebug:
		return zapcore.DebugLevel
	case fiberlog.LevelInfo:
		return zapcore.InfoLevel
	case fiberlog.LevelWarn:
		return zapcore.WarnLevel
	case fiberlog.LevelError:
		return zapcore.ErrorLevel
	case fiberlog.LevelFatal:
		return zapcore.FatalLevel
	case fiberlog.LevelPanic:
		return zapcore.DPanicLevel
	default:
		return zapcore.InfoLevel
	}
}
