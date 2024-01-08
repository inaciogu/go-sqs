## Go SQS Consumer

### Description
This is a simple package to help you consume messages from AWS SQS concurrently.

### Installation
To install the package, use the following command:

``````shell
go get github.com/inaciogu/go-sqs-consumer
``````

### Usage

``````go
package main

import (
	"fmt"

	"github.com/inaciogu/go-sqs-consumer/client"
	"github.com/inaciogu/go-sqs-consumer/handler"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load(".env")

	consumer1 := client.New(nil, client.SQSClientOptions{
		QueueName: "test-queue1",
		Handle: func(message *client.MessageModel) bool {
			fmt.Printf("Message received: %s\n", message.Content)
			return true
		},
		PollingWaitTimeSeconds: 30,
		Region:                 "us-east-1",
		From:                   client.OriginSNS,
	})

	consumer2 := client.New(nil, client.SQSClientOptions{
		QueueName: "test-queue2",
		Handle: func(message *client.MessageModel) bool {
			fmt.Printf("Message received: %s\n", message.Content)
			return true
		},
		PollingWaitTimeSeconds: 30,
		Region:                 "us-east-1",
		From:                   client.OriginSNS,
	})

	handler.New([]client.SQSClientInterface{
		consumer1,
		consumer2,
	})
}
``````
If you want to consume queues by a prefix, you can just set the `PrefixBased` option to `true` Then, the `QueueName` will be used as a prefix to find all queues that match the prefix.

### Configuration
To give the package access to your AWS account, you can use the following environment variables:

``````shell
AWS_ACCESS_KEY_ID
AWS_SECRET_ACCESS_KEY
``````

### Contribution
If you want to contribute to the development of this package, follow these steps:

- Fork the repository
- Create a new branch (git checkout -b feature/new-feature)
- Commit your changes (git commit -m 'Add new feature')
- Push to the branch (git push origin feature/new-feature)
- Open a Pull Request

### License
This package is distributed under the **MIT** license. See the LICENSE file for more information.

### Contact
Gustavo Inacio - [Linkedin](https://linkedin.com/in/inaciogu)