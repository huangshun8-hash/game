package logx

import "go.uber.org/zap"

// Debug logs a message at DebugLevel.
func Debug(msg string, fields ...zap.Field) {
	Logger().Debug(msg, fields...)
}

// Info logs a message at InfoLevel.
func Info(msg string, fields ...zap.Field) {
	Logger().Info(msg, fields...)
}

// Warn logs a message at WarnLevel.
func Warn(msg string, fields ...zap.Field) {
	Logger().Warn(msg, fields...)
}

// Error logs a message at ErrorLevel.
func Error(msg string, fields ...zap.Field) {
	Logger().Error(msg, fields...)
}

// Fatal logs a message at FatalLevel then calls os.Exit(1).
func Fatal(msg string, fields ...zap.Field) {
	Logger().Fatal(msg, fields...)
}

// Debugf logs a formatted message at DebugLevel.
func Debugf(template string, args ...interface{}) {
	Sugar().Debugf(template, args...)
}

// Infof logs a formatted message at InfoLevel.
func Infof(template string, args ...interface{}) {
	Sugar().Infof(template, args...)
}

// Warnf logs a formatted message at WarnLevel.
func Warnf(template string, args ...interface{}) {
	Sugar().Warnf(template, args...)
}

// Errorf logs a formatted message at ErrorLevel.
func Errorf(template string, args ...interface{}) {
	Sugar().Errorf(template, args...)
}

// Fatalf logs a formatted message at FatalLevel then calls os.Exit(1).
func Fatalf(template string, args ...interface{}) {
	Sugar().Fatalf(template, args...)
}

// With creates a child logger with the given fields.
func With(fields ...zap.Field) *zap.Logger {
	return Logger().With(fields...)
}
