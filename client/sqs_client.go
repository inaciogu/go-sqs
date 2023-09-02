package client

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
)

type SQSClientOptions struct {
	Session                *session.Session
	QueueName              string
	Handle                 func(message map[string]interface{}) (bool, error)
	PollingWaitTimeSeconds int64
}

type SQSClient struct {
	client        *sqs.SQS
	clientOptions *SQSClientOptions
}

type MessageResponse struct {
	Content string
}

func New(options SQSClientOptions) *SQSClient {
	return &SQSClient{
		client:        sqs.New(options.Session),
		clientOptions: &options,
	}
}

func (s *SQSClient) GetQueueUrl() (string, error) {
	urlResult, err := s.client.GetQueueUrl(&sqs.GetQueueUrlInput{
		QueueName: aws.String(s.clientOptions.QueueName),
	})

	if err != nil {
		return "", err
	}

	return *aws.String(*urlResult.QueueUrl), nil
}

func (s *SQSClient) ReceiveMessages() error {
	fmt.Printf("polling messages from %s\n", s.clientOptions.QueueName)

	queueUrl, err := s.GetQueueUrl()

	if err != nil {
		return err
	}

	result, err := s.client.ReceiveMessage(&sqs.ReceiveMessageInput{
		QueueUrl:          &queueUrl,
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

func (s *SQSClient) ProcessMessage(message *sqs.Message) {
	queueUrl, err := s.GetQueueUrl()

	if err != nil {
		fmt.Println(err.Error())

		return
	}

	formattedBody := strings.ReplaceAll(*message.Body, "'", "")

	var messageBody map[string]interface{}

	err = json.Unmarshal([]byte(formattedBody), &messageBody)

	if err != nil {
		fmt.Println(err.Error())

		return
	}

	fmt.Printf("handling message: %s\n", *message.Body)

	handled, err := s.clientOptions.Handle(messageBody)

	if err != nil {
		fmt.Printf("failed to handle message: %s\n", err.Error())

		s.client.ChangeMessageVisibility(&sqs.ChangeMessageVisibilityInput{
			QueueUrl:          &queueUrl,
			ReceiptHandle:     message.ReceiptHandle,
			VisibilityTimeout: aws.Int64(0),
		})

		return
	}

	if handled {
		s.client.DeleteMessage(&sqs.DeleteMessageInput{
			QueueUrl:      &queueUrl,
			ReceiptHandle: message.ReceiptHandle,
		})

		return
	}
}

func (s *SQSClient) Poll() {
	fmt.Println("starting polling")

	time := time.NewTicker(time.Duration(s.clientOptions.PollingWaitTimeSeconds) * time.Second)

	for range time.C {
		s.ReceiveMessages()
	}
}
