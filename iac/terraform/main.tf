provider "aws" {
  region = "us-east-1"
  access_key = "test"
  secret_key = "test"
  skip_requesting_account_id = true
  skip_credentials_validation = true
  skip_metadata_api_check = true

  endpoints {
    sqs = "http://localstack:4566"
    sns = "http://localstack:4566"
  }
}

module "sns_topic_subscription" {
  source = "github.com/paulo-tinoco/terraform-sns-topic-subscription"

  topics = [
    {
      name = "example"
    }
  ]

  queues = [
    {
      name = "example_queue"
      topics_to_subscribe = [
        {
          name = "example"
        }
      ]
    }
  ]
  account_id = "000000000000"
}