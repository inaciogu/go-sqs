[![Maintainability](https://api.codeclimate.com/v1/badges/9693cf5c61dc08d04fd5/maintainability)](https://codeclimate.com/github/inaciogu/go-sqs-consumer/maintainability)

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
	"github.com/inaciogu/go-sqs-consumer/message"
	"github.com/joho/godotenv"
)

type Message struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func main() {
	godotenv.Load(".env")

	consumer1 := client.New(nil, client.SQSClientOptions{
		QueueName: "test_queue",
		Handle: func(message *message.Message) bool {
			myMessage := Message{}

			// Unmarshal the message content
			err := message.Unmarshal(&myMessage)

			if err != nil {
				fmt.Println(err)

				// Do something if the message content cannot be unmarshalled
				return false
			}

			fmt.Println(myMessage.Email)

			// Do something with the message content

			// Return true if the message was successfully processed
			return true
		},
		PollingWaitTimeSeconds: 10,
		Region:                 "us-east-1",
		From:                   client.OriginSQS,
	})
	handler := handler.New([]client.SQSClientInterface{consumer1})

	handler.Run()
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