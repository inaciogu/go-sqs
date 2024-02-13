package logger_test

import (
	"testing"

	"github.com/inaciogu/go-sqs/consumer/logger"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap/zapcore"
)

type UnitTestSuite struct {
	suite.Suite
}

type Levels struct {
	Level    string
	ZapLevel zapcore.Level
}

func TestUnitSuites(t *testing.T) {
	suite.Run(t, new(UnitTestSuite))
}

func (ut *UnitTestSuite) TestNew() {
	levels := []Levels{
		{"info", zapcore.InfoLevel},
		{"warn", zapcore.WarnLevel},
		{"error", zapcore.ErrorLevel},
		{"debug", zapcore.DebugLevel},
		{"", zapcore.InfoLevel},
	}

	for _, level := range levels {
		logger := logger.New(logger.DefaultLoggerOptions{Level: level.Level})

		ut.NotNil(logger)
		ut.Assert().Equal(logger.Level(), level.ZapLevel)
	}
}

func (ut *UnitTestSuite) TestLog() {
	logger := logger.New(logger.DefaultLoggerOptions{})

	logger.Log("test")

	ut.Assert().Equal(logger.Level(), zapcore.InfoLevel)
	ut.NotNil(logger)
}
