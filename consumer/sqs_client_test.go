package client_test

import (
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/sqs"
	client "github.com/inaciogu/go-sqs/consumer"
	"github.com/inaciogu/go-sqs/consumer/message"
	"github.com/inaciogu/go-sqs/mocks"
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

func (ut *UnitTest) TestNewWithoutSQSService() {
	client := client.New(nil, client.SQSClientOptions{
		QueueName: "fake-queue-name",
	})

	assert.NotNil(ut.T(), client)
}

func (ut *UnitTest) TestNewWithSQSService() {
	client := client.New(ut.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
	})

	assert.NotNil(ut.T(), client)
}

func (ut *UnitTest) TestNewWithoutQueueName() {
	assert.Panics(ut.T(), func() {
		client.New(nil, client.SQSClientOptions{})
	})
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

	client := client.New(ut.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message *message.Message) bool {
			return true
		},
		PollingWaitTimeSeconds: 20,
	})

	expectedOutput := &sqs.ReceiveMessageOutput{
		Messages: []*sqs.Message{
			{
				Body:          aws.String(`{"content": "fake-content"}`),
				ReceiptHandle: aws.String("fake-receipt-handle"),
				MessageId:     aws.String("fake-message-id"),
			},
		},
	}

	ut.mockSQSService.On("ReceiveMessage", mock.Anything).Return(expectedOutput, nil)

	ut.mockSQSService.On("DeleteMessage", mock.Anything).Return(&sqs.DeleteMessageOutput{}, nil)

	go client.ReceiveMessages("https://fake-queue-url")

	time.Sleep(100 * time.Millisecond)

	ut.mockSQSService.AssertCalled(ut.T(), "ReceiveMessage", &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String("https://fake-queue-url"),
		MaxNumberOfMessages: aws.Int64(10),
		VisibilityTimeout:   aws.Int64(30),
		WaitTimeSeconds:     aws.Int64(20),
	})
}

func (ut *UnitTest) TestReceiveMessage_Error() {
	ut.mockSQSService.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}, nil)

	ut.mockSQSService.On("ReceiveMessage", mock.Anything).Return(&sqs.ReceiveMessageOutput{}, errors.New("erro"))

	client := client.New(ut.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message *message.Message) bool {
			return true
		},
	})

	assert.Panics(ut.T(), func() {
		client.ReceiveMessages("https://fake-queue-url")
	})
}

func (uts *UnitTest) TestProcessMessage_Handled() {
	uts.mockSQSService.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}, nil)

	client := client.New(uts.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message *message.Message) bool {
			return true
		},
		PollingWaitTimeSeconds: 20,
	})

	message := &sqs.Message{
		Body: aws.String(`{
			"Message": "{\n  \"asda\": \"asdas\"\n}",
			"MessageId": "fake-message-id",
			"ReceiptHandle": "fake-receipt-handle",
			"MessageAttributes": {
				"attribute1": {
					"Type": "String",
					"Value": "value1"
				}
			}
		}`),
		ReceiptHandle: aws.String("fake-receipt-handle"),
		MessageId:     aws.String("fake-message-id"),
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
		Handle: func(message *message.Message) bool {
			return false
		},
		PollingWaitTimeSeconds: 20,
	})

	message := &sqs.Message{
		Body:          aws.String(`{"content": "fake-content"}`),
		ReceiptHandle: aws.String("fake-receipt-handle"),
		MessageId:     aws.String("fake-message-id"),
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

func (uts *UnitTest) TestPoll() {
	uts.mockSQSService.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}, nil)

	client := client.New(uts.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message *message.Message) bool {
			return true
		},
		PollingWaitTimeSeconds: 2,
	})

	uts.mockSQSService.On("ReceiveMessage", mock.Anything).Return(&sqs.ReceiveMessageOutput{}, nil)

	client.Poll()

	uts.mockSQSService.AssertCalled(uts.T(), "ReceiveMessage", &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String("https://fake-queue-url"),
		MaxNumberOfMessages: aws.Int64(10),
		VisibilityTimeout:   aws.Int64(30),
		WaitTimeSeconds:     aws.Int64(20),
	})
}

func (ut *UnitTest) TestGetQueues_Error() {
	client := client.New(ut.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message *message.Message) bool {
			return true
		},
		PollingWaitTimeSeconds: 2,
	})

	ut.mockSQSService.On("ListQueues", mock.Anything).Return(nil, errors.New("Error"))

	assert.Panics(ut.T(), func() {
		client.GetQueues("fake-queue-name")
	})
}

func (uts *UnitTest) TestGetQueues() {
	client := client.New(uts.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message *message.Message) bool {
			return true
		},
		PollingWaitTimeSeconds: 2,
	})

	uts.mockSQSService.On("ListQueues", mock.Anything).Return(&sqs.ListQueuesOutput{
		QueueUrls: []*string{
			aws.String("https://fake-queue-url"),
			aws.String("https://fake-queue-url-2"),
		},
	}, nil)

	queues := client.GetQueues("fake-queue-name")

	assert.Equal(uts.T(), 2, len(queues))

	uts.mockSQSService.AssertCalled(uts.T(), "ListQueues", &sqs.ListQueuesInput{
		QueueNamePrefix: aws.String("fake-queue-name"),
	})
}

func (uts *UnitTest) TestPollPrefixBased() {
	client := client.New(uts.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message *message.Message) bool {
			return true
		},
		PollingWaitTimeSeconds: 2,
		PrefixBased:            true,
	})

	uts.mockSQSService.On("ListQueues", mock.Anything).Return(&sqs.ListQueuesOutput{
		QueueUrls: []*string{
			aws.String("https://fake-queue-url"),
			aws.String("https://fake-queue-url-2"),
		},
	}, nil)

	uts.mockSQSService.On("ReceiveMessage", mock.Anything).Return(&sqs.ReceiveMessageOutput{}, nil)

	client.Poll()

	time.Sleep(100 * time.Millisecond)

	uts.mockSQSService.AssertCalled(uts.T(), "ListQueues", &sqs.ListQueuesInput{
		QueueNamePrefix: aws.String("fake-queue-name"),
	})
	uts.mockSQSService.AssertCalled(uts.T(), "ReceiveMessage", &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String("https://fake-queue-url"),
		MaxNumberOfMessages: aws.Int64(10),
		VisibilityTimeout:   aws.Int64(30),
		WaitTimeSeconds:     aws.Int64(20),
	})
}

func (uts *UnitTest) TestStart() {
	uts.mockSQSService.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}, nil)

	client := client.New(uts.mockSQSService, client.SQSClientOptions{
		QueueName: "fake-queue-name",
		Handle: func(message *message.Message) bool {
			return true
		},
		PollingWaitTimeSeconds: 2,
	})

	uts.mockSQSService.On("ReceiveMessage", mock.Anything).Return(&sqs.ReceiveMessageOutput{}, nil)

	go client.Start()

	time.Sleep(3 * time.Second)

	uts.mockSQSService.AssertCalled(uts.T(), "ReceiveMessage", &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String("https://fake-queue-url"),
		MaxNumberOfMessages: aws.Int64(10),
		VisibilityTimeout:   aws.Int64(30),
		WaitTimeSeconds:     aws.Int64(20),
	})

	uts.mockSQSService.AssertNumberOfCalls(uts.T(), "ReceiveMessage", 2)
}
