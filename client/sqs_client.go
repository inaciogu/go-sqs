package client

import (
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/inaciogu/go-sqs-consumer/message"
)

type SQSService interface {
	GetQueueUrl(input *sqs.GetQueueUrlInput) (*sqs.GetQueueUrlOutput, error)
	ReceiveMessage(input *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error)
	ChangeMessageVisibility(input *sqs.ChangeMessageVisibilityInput) (*sqs.ChangeMessageVisibilityOutput, error)
	DeleteMessage(input *sqs.DeleteMessageInput) (*sqs.DeleteMessageOutput, error)
	ListQueues(input *sqs.ListQueuesInput) (*sqs.ListQueuesOutput, error)
}

type SQSClientInterface interface {
	GetQueueUrl() *string
	ReceiveMessages(queueUrl string) error
	ProcessMessage(message *sqs.Message)
	Poll()
	GetQueues(prefix string) []*string
	Start()
}

// Indicates the origin of the message (SQS or SNS)
type MessageOrigin string

const (
	// OriginSQS indicates that the message was sent directly to the SQS queue
	OriginSQS MessageOrigin = "SQS"
	// OriginSNS indicates that the message was sent to the SQS queue through SNS
	OriginSNS MessageOrigin = "SNS"
)

type SQSClientOptions struct {
	QueueName string // required
	// Handle is the function that will be called when a message is received.
	// Return true if you want to delete the message from the queue, otherwise, return false
	Handle                 func(message *message.Message) bool
	PollingWaitTimeSeconds int64
	Region                 string
	Endpoint               string
	PrefixBased            bool
	MaxNumberOfMessages    int64
	VisibilityTimeout      int64
	WaitTimeSeconds        int64
}

type SQSClient struct {
	client        SQSService
	clientOptions *SQSClientOptions
}

const (
	DefaultPollingWaitTimeSeconds = 20
	DefaultMaxNumberOfMessages    = 10
	DefaultVisibilityTimeout      = 30
	DefaultWaitTimeSeconds        = 20
	DefaultRegion                 = "us-east-1"
)

func New(sqsService SQSService, options SQSClientOptions) *SQSClient {
	if options.QueueName == "" {
		panic("QueueName is required")
	}

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

	setDefaultOptions(&options)

	return &SQSClient{
		client:        sqsService,
		clientOptions: &options,
	}
}

func setDefaultOptions(options *SQSClientOptions) {
	if options.PollingWaitTimeSeconds == 0 {
		options.PollingWaitTimeSeconds = DefaultPollingWaitTimeSeconds
	}

	if options.MaxNumberOfMessages == 0 {
		options.MaxNumberOfMessages = DefaultMaxNumberOfMessages
	}

	if options.VisibilityTimeout == 0 {
		options.VisibilityTimeout = DefaultVisibilityTimeout
	}

	if options.WaitTimeSeconds == 0 {
		options.WaitTimeSeconds = DefaultWaitTimeSeconds
	}

	if options.Region == "" {
		options.Region = "us-east-1"
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

// GetQueues returns a list of queues based on the prefix
func (s *SQSClient) GetQueues(prefix string) []*string {
	input := &sqs.ListQueuesInput{
		QueueNamePrefix: aws.String(prefix),
	}

	result, err := s.client.ListQueues(input)

	if err != nil {
		panic(err)
	}

	return result.QueueUrls
}

// ReceiveMessages polls messages from the queue
func (s *SQSClient) ReceiveMessages(queueUrl string) error {
	splittedUrl := strings.Split(queueUrl, "/")

	queueName := splittedUrl[len(splittedUrl)-1]

	fmt.Printf("polling messages from %s\n", queueName)

	result, err := s.client.ReceiveMessage(&sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueUrl),
		MaxNumberOfMessages: aws.Int64(s.clientOptions.MaxNumberOfMessages),
		WaitTimeSeconds:     aws.Int64(s.clientOptions.WaitTimeSeconds),
		VisibilityTimeout:   aws.Int64(s.clientOptions.VisibilityTimeout),
	})

	if err != nil {
		panic(err)
	}

	for _, message := range result.Messages {
		go s.ProcessMessage(message)
	}

	return nil
}

// ProcessMessage executes the Handle method and deletes the message from the queue if the Handle method returns true
func (s *SQSClient) ProcessMessage(sqsMessage *sqs.Message) {
	queueUrl := s.GetQueueUrl()

	message := message.New(sqsMessage)

	handled := s.clientOptions.Handle(message)

	if !handled {
		fmt.Printf("failed to handle message with ID: %s\n", message.Metadata.MessageId)

		s.client.ChangeMessageVisibility(&sqs.ChangeMessageVisibilityInput{
			QueueUrl:          queueUrl,
			ReceiptHandle:     &message.Metadata.ReceiptHandle,
			VisibilityTimeout: aws.Int64(0),
		})

		return
	}

	fmt.Printf("message handled ID: %s\n", message.Metadata.MessageId)

	s.client.DeleteMessage(&sqs.DeleteMessageInput{
		QueueUrl:      queueUrl,
		ReceiptHandle: &message.Metadata.ReceiptHandle,
	})
}

// Poll calls ReceiveMessages based on the polling wait time
func (s *SQSClient) Poll() {
	if s.clientOptions.PrefixBased {
		queues := s.GetQueues(s.clientOptions.QueueName)

		for _, queue := range queues {
			go s.ReceiveMessages(*queue)
		}

		return
	}

	queueUrl := s.GetQueueUrl()

	s.ReceiveMessages(*queueUrl)
}

func (s *SQSClient) Start() {
	time := time.NewTicker(time.Duration(s.clientOptions.PollingWaitTimeSeconds) * time.Second)

	s.Poll()

	for range time.C {
		s.Poll()
	}
}
