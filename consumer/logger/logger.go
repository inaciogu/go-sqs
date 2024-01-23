package logger

import (
	"fmt"
	"log/slog"
	"os"
)

type DefaultLogger struct {
	*slog.Logger
}

func New() *DefaultLogger {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	slog.SetDefault(logger)

	return &DefaultLogger{
		Logger: logger,
	}
}

func (l *DefaultLogger) Log(message string, v ...interface{}) {
	l.Info(fmt.Sprintf(message, v...))
}
