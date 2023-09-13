package client

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
)

type SQSService interface {
	GetQueueUrl(input *sqs.GetQueueUrlInput) (*sqs.GetQueueUrlOutput, error)
	ReceiveMessage(input *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error)
	ChangeMessageVisibility(input *sqs.ChangeMessageVisibilityInput) (*sqs.ChangeMessageVisibilityOutput, error)
	DeleteMessage(input *sqs.DeleteMessageInput) (*sqs.DeleteMessageOutput, error)
}

type SQSClientInterface interface {
	GetQueueUrl() *string
	ReceiveMessages() error
	ProcessMessage(message *sqs.Message)
	Poll()
}

type SQSClientOptions struct {
	QueueName              string
	Handle                 func(message map[string]interface{}) bool
	PollingWaitTimeSeconds int64
	Region                 string
	Endpoint               string
}

type SQSClient struct {
	client        SQSService
	clientOptions *SQSClientOptions
}

type MessageResponse struct {
	Content string
}

func New(sqsService SQSService, options SQSClientOptions) *SQSClient {
	if sqsService == nil {
		sess := session.Must(session.NewSessionWithOptions(session.Options{
			Config: aws.Config{
				Credentials: credentials.NewCredentials(&credentials.EnvProvider{}),
				Region:      aws.String(options.Region),
				Endpoint:    aws.String(options.Endpoint),
			},
		}))
		sqsService = sqs.New(sess)
	}

	return &SQSClient{
		client:        sqsService,
		clientOptions: &options,
	}
}

// GetQueueUrl returns the URL of the queue based on the queue name
func (s *SQSClient) GetQueueUrl() *string {
	urlResult, err := s.client.GetQueueUrl(&sqs.GetQueueUrlInput{
		QueueName: aws.String(s.clientOptions.QueueName),
	})

	if err != nil {
		panic(err)
	}

	return aws.String(*urlResult.QueueUrl)
}

// ReceiveMessages polls messages from the queue
func (s *SQSClient) ReceiveMessages() error {
	fmt.Printf("polling messages from %s\n", s.clientOptions.QueueName)

	queueUrl := s.GetQueueUrl()

	result, err := s.client.ReceiveMessage(&sqs.ReceiveMessageInput{
		QueueUrl:          queueUrl,
		WaitTimeSeconds:   aws.Int64(20),
		VisibilityTimeout: aws.Int64(30),
	})

	if err != nil {
		panic(err)
	}

	for _, message := range result.Messages {
		s.ProcessMessage(message)
	}

	return nil
}

// ProcessMessage Transforms message body and delete it from the queue if it was handled successfully, otherwise, it changes the message visibility
func (s *SQSClient) ProcessMessage(message *sqs.Message) {
	queueUrl := s.GetQueueUrl()

	formattedBody := strings.ReplaceAll(*message.Body, "'", "")

	var messageBody map[string]interface{}

	err := json.Unmarshal([]byte(formattedBody), &messageBody)

	if err != nil {
		fmt.Println(err.Error())

		return
	}

	fmt.Printf("handling message: %s\n", *message.Body)

	handled := s.clientOptions.Handle(messageBody)

	if !handled {
		fmt.Printf("failed to handle message: %s\n", *message.Body)

		s.client.ChangeMessageVisibility(&sqs.ChangeMessageVisibilityInput{
			QueueUrl:          queueUrl,
			ReceiptHandle:     message.ReceiptHandle,
			VisibilityTimeout: aws.Int64(0),
		})

		return
	}

	s.client.DeleteMessage(&sqs.DeleteMessageInput{
		QueueUrl:      queueUrl,
		ReceiptHandle: message.ReceiptHandle,
	})
}

// Poll calls ReceiveMessages based on the polling wait time
func (s *SQSClient) Poll() {
	fmt.Println("starting polling")

	time := time.NewTicker(time.Duration(s.clientOptions.PollingWaitTimeSeconds) * time.Second)

	for range time.C {
		s.ReceiveMessages()
	}
}
