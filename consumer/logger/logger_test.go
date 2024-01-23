package logger_test

import (
	"github.com/inaciogu/go-sqs/consumer/logger"
	"github.com/stretchr/testify/suite"
)

type UnitTestSuite struct {
	suite.Suite
}

func (ut *UnitTestSuite) TestNew() {
	logger := logger.New()

	ut.NotNil(logger)
}
