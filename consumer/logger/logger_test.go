package logger_test

import (
	"testing"

	"github.com/inaciogu/go-sqs/consumer/logger"
	"github.com/stretchr/testify/suite"
)

type UnitTestSuite struct {
	suite.Suite
}

func TestUnitSuites(t *testing.T) {
	suite.Run(t, new(UnitTestSuite))
}

func (ut *UnitTestSuite) TestNew() {
	logger := logger.New()

	ut.NotNil(logger)
}

func (ut *UnitTestSuite) TestLog() {
	logger := logger.New()

	logger.Log("test")

	ut.NotNil(logger)
}
