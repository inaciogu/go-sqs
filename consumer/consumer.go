package consumer

import (
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/inaciogu/go-sqs/consumer/logger"
	"github.com/inaciogu/go-sqs/consumer/message"
)

type SQSService interface {
	GetQueueUrl(input *sqs.GetQueueUrlInput) (*sqs.GetQueueUrlOutput, error)
	ReceiveMessage(input *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error)
	ChangeMessageVisibility(input *sqs.ChangeMessageVisibilityInput) (*sqs.ChangeMessageVisibilityOutput, error)
	DeleteMessage(input *sqs.DeleteMessageInput) (*sqs.DeleteMessageOutput, error)
	ListQueues(input *sqs.ListQueuesInput) (*sqs.ListQueuesOutput, error)
}

type Logger interface {
	Log(message string, v ...interface{})
}

type SQSClientInterface interface {
	GetQueueUrl() *string
	ReceiveMessages(queueUrl string, ch chan *sqs.Message) error
	ProcessMessage(message *sqs.Message, queueUrl string)
	Poll()
	GetQueues(prefix string) []*string
	Start()
}

type SQSClientOptions struct {
	QueueName string
	// Handle is the function that will be called when a message is received.
	// Return true if you want to delete the message from the queue, otherwise, return false
	Handle   func(message *message.Message) bool
	Region   string
	Endpoint string
	// PrefixBased is a flag that indicates if the queue name is a prefix
	PrefixBased         bool
	MaxNumberOfMessages int64
	VisibilityTimeout   int64
	WaitTimeSeconds     int64
	LogLevel            string
}

type SQSClient struct {
	Client        SQSService
	ClientOptions *SQSClientOptions
	Logger        Logger
}

const (
	DefaultMaxNumberOfMessages = 10
	DefaultVisibilityTimeout   = 30
	DefaultWaitTimeSeconds     = 20
	DefaultRegion              = "us-east-1"
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

	logger := logger.New(logger.DefaultLoggerConfig{LogLevel: options.LogLevel})
	return &SQSClient{
		Client:        sqsService,
		ClientOptions: &options,
		Logger:        logger,
	}
}

func setDefaultOptions(options *SQSClientOptions) {
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
		options.Region = DefaultRegion
	}

	if options.LogLevel == "" {
		options.LogLevel = "info"
	}
}

func (s *SQSClient) SetLogger(logger Logger) {
	s.Logger = logger
}

// GetQueueUrl returns the URL of the queue based on the queue name
func (s *SQSClient) GetQueueUrl() *string {
	urlResult, err := s.Client.GetQueueUrl(&sqs.GetQueueUrlInput{
		QueueName: aws.String(s.ClientOptions.QueueName),
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

	result, err := s.Client.ListQueues(input)

	if err != nil {
		panic(err)
	}

	return result.QueueUrls
}

// ReceiveMessages polls messages from the queue
func (s *SQSClient) ReceiveMessages(queueUrl string, ch chan *sqs.Message) error {
	splittedUrl := strings.Split(queueUrl, "/")

	queueName := splittedUrl[len(splittedUrl)-1]

	for {
		s.Logger.Log("polling messages from queue %s", queueName)

		result, err := s.Client.ReceiveMessage(&sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueUrl),
			MaxNumberOfMessages: aws.Int64(s.ClientOptions.MaxNumberOfMessages),
			WaitTimeSeconds:     aws.Int64(s.ClientOptions.WaitTimeSeconds),
			VisibilityTimeout:   aws.Int64(s.ClientOptions.VisibilityTimeout),
		})

		if err != nil {
			panic(err)
		}

		s.Logger.Log("received %d messages from queue %s", len(result.Messages), queueName)

		for _, message := range result.Messages {
			ch <- message
		}
	}
}

// ProcessMessage deletes or changes the visibility of the message based on the Handle function return.
func (s *SQSClient) ProcessMessage(sqsMessage *sqs.Message, queueUrl string) {
	message := message.New(sqsMessage)

	handled := s.ClientOptions.Handle(message)

	if !handled {
		_, err := s.Client.ChangeMessageVisibility(&sqs.ChangeMessageVisibilityInput{
			QueueUrl:          aws.String(queueUrl),
			ReceiptHandle:     &message.Metadata.ReceiptHandle,
			VisibilityTimeout: aws.Int64(0),
		})

		if err != nil {
			panic(err)
		}

		s.Logger.Log("failed to handle message with ID: %s", message.Metadata.MessageId)

		return
	}

	_, err := s.Client.DeleteMessage(&sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueUrl),
		ReceiptHandle: &message.Metadata.ReceiptHandle,
	})

	if err != nil {
		panic(err)
	}

	s.Logger.Log("message handled ID: %s", message.Metadata.MessageId)
}

// Poll starts polling messages from the queue
func (s *SQSClient) Poll() {
	if s.ClientOptions.PrefixBased {
		queues := s.GetQueues(s.ClientOptions.QueueName)

		for _, queue := range queues {
			ch := make(chan *sqs.Message)

			go s.ReceiveMessages(*queue, ch)

			go func(queueUrl string) {
				for message := range ch {
					go s.ProcessMessage(message, queueUrl)
				}
			}(*queue)
		}

		select {}
	}

	ch := make(chan *sqs.Message)

	queueUrl := s.GetQueueUrl()

	go s.ReceiveMessages(*queueUrl, ch)

	for message := range ch {
		go s.ProcessMessage(message, *queueUrl)
	}
}

func (s *SQSClient) Start() {
	s.Poll()
}
