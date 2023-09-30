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

type MessageOrigin string

const (
	OriginSQS MessageOrigin = "SQS"
	OriginSNS MessageOrigin = "SNS"
)

type SQSClientOptions struct {
	QueueName              string // required
	Handle                 func(message map[string]interface{}) bool
	PollingWaitTimeSeconds int64
	Region                 string
	Endpoint               string
	From                   MessageOrigin
}

type SQSClient struct {
	client        SQSService
	clientOptions *SQSClientOptions
}

type MessageAttributes map[string]Attribute

type Attribute struct {
	Type  string
	Value string
}

type MessageMetadata struct {
	MessageId         string
	ReceiptHandle     string
	MessageAttributes map[string]string
}

type MessageModel struct {
	Content  map[string]interface{}
	Metadata MessageMetadata
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
		QueueUrl:            queueUrl,
		MaxNumberOfMessages: aws.Int64(10),
		WaitTimeSeconds:     aws.Int64(20),
		VisibilityTimeout:   aws.Int64(30),
	})

	if err != nil {
		panic(err)
	}

	for _, message := range result.Messages {
		go s.ProcessMessage(message)
	}

	return nil
}

func (s *SQSClient) getMessageAttributes(messageBody map[string]interface{}) map[string]string {
	attributes := make(map[string]string)
	snsMessageAttributes := make(MessageAttributes)

	messageAttributes, ok := messageBody["MessageAttributes"].(map[string]interface{})
	if !ok {
		return attributes
	}

	for key, value := range messageAttributes {
		attribute := value.(map[string]interface{})
		snsMessageAttributes[key] = Attribute{
			Type:  attribute["Type"].(string),
			Value: attribute["Value"].(string),
		}
	}

	for key, value := range snsMessageAttributes {
		attributes[key] = value.Value
	}

	return attributes
}

// ProcessMessage Transforms message body and delete it from the queue if it was handled successfully, otherwise, it changes the message visibility
func (s *SQSClient) ProcessMessage(message *sqs.Message) {
	queueUrl := s.GetQueueUrl()

	var messageBody map[string]interface{}
	var messageAttributes map[string]string

	formattedBody := strings.ReplaceAll(*message.Body, "'", "")

	err := json.Unmarshal([]byte(formattedBody), &messageBody)

	if err != nil {
		fmt.Println(err.Error())

		return
	}

	if s.clientOptions.From == OriginSNS {
		var snsMessageBody map[string]interface{}

		formattedSNSBody := strings.ReplaceAll(messageBody["Message"].(string), "'", "")
		fmt.Printf("formattedSNSBody: %s\n", formattedSNSBody)

		err := json.Unmarshal([]byte(formattedSNSBody), &snsMessageBody)

		if err != nil {
			fmt.Println(err.Error())

			return
		}
		messageAttributes = s.getMessageAttributes(messageBody)
		messageBody = snsMessageBody

		fmt.Printf("messageAttributes: %s\n", messageAttributes)
	}

	meta := MessageMetadata{
		MessageId:         *message.MessageId,
		ReceiptHandle:     *message.ReceiptHandle,
		MessageAttributes: messageAttributes,
	}

	translatedMessage := &MessageModel{
		Content:  messageBody,
		Metadata: meta,
	}

	handled := s.clientOptions.Handle(messageBody)

	if !handled {
		fmt.Printf("failed to handle message: %s\n", translatedMessage.Content)

		s.client.ChangeMessageVisibility(&sqs.ChangeMessageVisibilityInput{
			QueueUrl:          queueUrl,
			ReceiptHandle:     message.ReceiptHandle,
			VisibilityTimeout: aws.Int64(0),
		})

		return
	}

	fmt.Printf("message handled: %s\n", translatedMessage.Content)
	fmt.Printf("metadata: %s\n", messageAttributes)

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
