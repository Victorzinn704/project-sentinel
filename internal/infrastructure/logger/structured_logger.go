package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger interface {
	Info(message string, fields ...zap.Field)
	Error(message string, fields ...zap.Field)
	Debug(message string, fields ...zap.Field)
	Warn(message string, fields ...zap.Field)
}

type StructuredLogger struct {
	base *zap.Logger
}

func New(level string) (*StructuredLogger, error) {
	cfg := zap.NewProductionConfig()

	parsedLevel := zapcore.InfoLevel
	if err := parsedLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, err
	}
	cfg.Level = zap.NewAtomicLevelAt(parsedLevel)

	base, err := cfg.Build()
	if err != nil {
		return nil, err
	}

	return &StructuredLogger{base: base}, nil
}

func (l *StructuredLogger) Sync() error {
	return l.base.Sync()
}

func (l *StructuredLogger) Info(message string, fields ...zap.Field) {
	l.base.Info(message, fields...)
}

func (l *StructuredLogger) Warn(message string, fields ...zap.Field) {
	l.base.Warn(message, fields...)
}

func (l *StructuredLogger) Error(message string, fields ...zap.Field) {
	l.base.Error(message, fields...)
}

func (l *StructuredLogger) Debug(message string, fields ...zap.Field) {
	l.base.Debug(message, fields...)
}
