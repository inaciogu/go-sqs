package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type DefaultLogger struct {
	*zap.Logger
}

type DefaultLoggerOptions struct {
	// eg. info, warn, error, debug
	Level string
}

func New(options DefaultLoggerOptions) *DefaultLogger {
	level := getZapLevel(options.Level)

	logger := zap.Config{
		Level:       zap.NewAtomicLevelAt(level),
		OutputPaths: []string{"stdout"},
		Encoding:    "json",
		EncoderConfig: zapcore.EncoderConfig{
			MessageKey:  "message",
			TimeKey:     "time",
			LevelKey:    "level",
			EncodeTime:  zapcore.ISO8601TimeEncoder,
			EncodeLevel: zapcore.LowercaseLevelEncoder,
		},
	}

	zapLogger, _ := logger.Build()

	return &DefaultLogger{
		Logger: zapLogger,
	}
}

func getZapLevel(level string) zapcore.Level {
	switch level {
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "debug":
		return zapcore.DebugLevel
	default:
		return zapcore.InfoLevel
	}
}

func (l *DefaultLogger) Log(message string, v ...interface{}) {
	l.Logger.Log(l.Level(), fmt.Sprintf(message, v...))
}
