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

locals {
  queues = {
    test = {
      name = "test"
    },
    test2 = {
      name = "test2"
    },
    test3 = {
      name = "test3"
    }
  }
}

resource "aws_sqs_queue" "test" {
  for_each = local.queues

  name = each.value.name
}

resource "aws_sqs_queue_policy" "test" {
  for_each = local.queues

  queue_url = aws_sqs_queue.test[each.key].id
  policy =  jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid = "Allow-SNS-SendMessage"
        Effect = "Allow"
        Principal = {
          AWS = "*"
        }
        Action = "sqs:SendMessage"
        Resource = aws_sqs_queue.test[each.key].arn
        Condition = {
          ArnEquals = {
            "aws:SourceArn" = aws_sns_topic.test.arn
          }
        }
      }
    ]
  })
}

resource "aws_sns_topic" "test" {
  name = "test"
}

resource "aws_sns_topic_subscription" "test" {
  for_each = local.queues

  topic_arn = aws_sns_topic.test.arn
  protocol = "sqs"
  endpoint = aws_sqs_queue.test[each.key].arn
}