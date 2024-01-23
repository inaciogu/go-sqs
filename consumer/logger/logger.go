package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type DefaultLogger struct {
	*zap.Logger
}

func New() *DefaultLogger {
	logger := zap.Config{
		Level:       zap.NewAtomicLevelAt(zapcore.InfoLevel),
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

func (l *DefaultLogger) Log(message string, v ...interface{}) {
	l.Info(fmt.Sprintf(message, v...))
}
