package client_test

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/inaciogu/go-sqs-client/client"
	"github.com/inaciogu/go-sqs-client/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type UnitTest struct {
	suite.Suite
	mockSQSService *mocks.SQSService
}

func (u *UnitTest) SetupTest() {
	u.mockSQSService = new(mocks.SQSService)
}

func TestUnitSuites(t *testing.T) {
	suite.Run(t, &UnitTest{})
}

func (ut *UnitTest) TestGetQueueUrl() {
	expectedOutput := &sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}
	ut.mockSQSService.On("GetQueueUrl", mock.Anything).Return(expectedOutput, nil)

	client := client.New(ut.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
	})

	queueURL := client.GetQueueUrl()

	assert.Equal(ut.T(), "https://fake-queue-url", *queueURL)

	ut.mockSQSService.AssertCalled(ut.T(), "GetQueueUrl", &sqs.GetQueueUrlInput{
		QueueName: aws.String("fake-queue-name"),
	})
}

func (ut *UnitTest) TestQueueUrl_Error() {
	ut.mockSQSService.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{}, errors.New("erro"))

	client := client.New(ut.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
	})

	assert.Panics(ut.T(), func() {
		client.GetQueueUrl()
	})
}

func (ut *UnitTest) TestReceiveMessage() {
	ut.mockSQSService.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}, nil)

	expectedOutput := &sqs.ReceiveMessageOutput{
		Messages: []*sqs.Message{
			{
				Body:          aws.String(`{"content": "fake-content"}`),
				ReceiptHandle: aws.String("fake-receipt-handle"),
			},
		},
	}

	ut.mockSQSService.On("ReceiveMessage", mock.Anything).Return(expectedOutput, nil)
	ut.mockSQSService.On("DeleteMessage", mock.Anything).Return(&sqs.DeleteMessageOutput{}, nil)

	client := client.New(ut.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message map[string]interface{}) bool {
			return true
		},
	})

	err := client.ReceiveMessages()
	if err != nil {
		ut.T().Fatalf("Erro ao receber mensagens: %v", err)
	}

	ut.mockSQSService.AssertCalled(ut.T(), "ReceiveMessage", &sqs.ReceiveMessageInput{
		QueueUrl:          aws.String("https://fake-queue-url"),
		VisibilityTimeout: aws.Int64(30),
		WaitTimeSeconds:   aws.Int64(20),
	})
	ut.mockSQSService.AssertCalled(ut.T(), "DeleteMessage", &sqs.DeleteMessageInput{
		QueueUrl:      aws.String("https://fake-queue-url"),
		ReceiptHandle: aws.String("fake-receipt-handle"),
	})
}

func (ut *UnitTest) TestReceiveMessage_Error() {
	ut.mockSQSService.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}, nil)

	ut.mockSQSService.On("ReceiveMessage", mock.Anything).Return(&sqs.ReceiveMessageOutput{}, errors.New("erro"))

	client := client.New(ut.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message map[string]interface{}) bool {
			return true
		},
	})

	assert.Panics(ut.T(), func() {
		client.ReceiveMessages()
	})
}

func (uts *UnitTest) TestProcessMessage_Handled() {
	uts.mockSQSService.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}, nil)

	client := client.New(uts.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message map[string]interface{}) bool {
			return true
		},
		PollingWaitTimeSeconds: 20,
	})

	message := &sqs.Message{
		Body:          aws.String(`{"content": "fake-content"}`),
		ReceiptHandle: aws.String("fake-receipt-handle"),
	}

	uts.mockSQSService.On("DeleteMessage", mock.Anything).Return(&sqs.DeleteMessageOutput{}, nil)

	client.ProcessMessage(message)

	uts.mockSQSService.AssertCalled(uts.T(), "DeleteMessage", &sqs.DeleteMessageInput{
		QueueUrl:      aws.String("https://fake-queue-url"),
		ReceiptHandle: aws.String("fake-receipt-handle"),
	})
}

func (uts *UnitTest) TestProcessMessage_Not_Handled() {
	uts.mockSQSService.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}, nil)

	client := client.New(uts.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message map[string]interface{}) bool {
			return false
		},
		PollingWaitTimeSeconds: 20,
	})

	message := &sqs.Message{
		Body:          aws.String(`{"content": "fake-content"}`),
		ReceiptHandle: aws.String("fake-receipt-handle"),
	}

	uts.mockSQSService.On("DeleteMessage", mock.Anything).Return(&sqs.DeleteMessageOutput{}, nil)
	uts.mockSQSService.On("ChangeMessageVisibility", mock.Anything).Return(&sqs.ChangeMessageVisibilityOutput{}, nil)

	client.ProcessMessage(message)

	uts.mockSQSService.AssertNotCalled(uts.T(), "DeleteMessage", &sqs.DeleteMessageInput{
		QueueUrl:      aws.String("https://fake-queue-url"),
		ReceiptHandle: aws.String("fake-receipt-handle"),
	})
	uts.mockSQSService.AssertCalled(uts.T(), "ChangeMessageVisibility", &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String("https://fake-queue-url"),
		ReceiptHandle:     aws.String("fake-receipt-handle"),
		VisibilityTimeout: aws.Int64(0),
	})
}
