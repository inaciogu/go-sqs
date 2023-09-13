package handler_test

import (
	"testing"
	"time"

	sqsclient "github.com/inaciogu/go-sqs-client/client"
	"github.com/inaciogu/go-sqs-client/handler"
	"github.com/inaciogu/go-sqs-client/mocks"
	"github.com/stretchr/testify/suite"
)

type UnitTest struct {
	suite.Suite
	clients []*mocks.SQSClientInterface
	handler *handler.SQSHandler
}

func (u *UnitTest) SetupTest() {
	exampleClient1 := new(mocks.SQSClientInterface)

	exampleClient2 := new(mocks.SQSClientInterface)

	u.clients = []*mocks.SQSClientInterface{
		exampleClient1,
		exampleClient2,
	}

	u.handler = handler.New([]sqsclient.SQSClientInterface{
		exampleClient1,
		exampleClient2,
	})
}

func TestUnitSuites(t *testing.T) {
	suite.Run(t, &UnitTest{})
}

func (ut *UnitTest) TestRun() {
	for _, client := range ut.clients {
		client.On("Poll").Return()
	}

	go ut.handler.Run()

	time.Sleep(100 * time.Millisecond)

	for _, client := range ut.clients {
		client.AssertCalled(ut.T(), "Poll")
	}
}
