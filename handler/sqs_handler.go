package handler

import sqsclient "github.com/inaciogu/go-sqs-client/client"

type SQSHandler struct {
	Clients []*sqsclient.SQSClient
}

func New(clients []*sqsclient.SQSClient) *SQSHandler {
	return &SQSHandler{
		Clients: clients,
	}
}

func (h *SQSHandler) Run() {
	for _, client := range h.Clients {
		go client.Poll()
	}

	select {}
}
