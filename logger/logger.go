package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type DefaultLogger struct {
	*zap.Logger
	LogLevel string
}

type DefaultLoggerConfig struct {
	LogLevel string
}

func New(config DefaultLoggerConfig) *DefaultLogger {
	logLevelMap := map[string]zapcore.Level{
		"debug":  zapcore.DebugLevel,
		"info":   zapcore.InfoLevel,
		"warn":   zapcore.WarnLevel,
		"error":  zapcore.ErrorLevel,
		"dpanic": zapcore.DPanicLevel,
		"panic":  zapcore.PanicLevel,
		"fatal":  zapcore.FatalLevel,
	}

	if _, ok := logLevelMap[config.LogLevel]; !ok {
		config.LogLevel = "info"
	}

	logger := zap.Config{
		Level:       zap.NewAtomicLevelAt(logLevelMap[config.LogLevel]),
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
		Logger:   zapLogger,
		LogLevel: config.LogLevel,
	}
}

func (l *DefaultLogger) Log(message string, v ...interface{}) {
	logFunctionMap := map[string]func(string, ...zapcore.Field){
		"debug":  l.Debug,
		"info":   l.Info,
		"warn":   l.Warn,
		"error":  l.Error,
		"dpanic": l.DPanic,
		"panic":  l.Panic,
		"fatal":  l.Fatal,
	}

	logFunctionMap[l.LogLevel](fmt.Sprintf(message, v...))
}
