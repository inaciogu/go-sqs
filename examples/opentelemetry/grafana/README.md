# Grafana local para go-sqs

Configuração temporária e isolada para uso pessoal. O container LGTM contém
Collector, Prometheus, Loki e Grafana. O consumer real da biblioteca produz os
logs e métricas; o exemplo Go inicializa os providers/exporters OTLP. O cliente
SQS usa o MiniStack; não é necessária uma conta AWS ou destino cloud.

## Iniciar

Execute da raiz do repositório. Pare o Collector do exemplo anterior se ele
estiver ocupando a porta 4318.

```sh
docker compose up -d
go run ./examples/setup
docker compose -p go-sqs-local-grafana -f examples/opentelemetry/grafana/compose.yaml up -d
docker compose -p go-sqs-local-grafana -f examples/opentelemetry/grafana/compose.yaml logs --tail=40
```

Quando a stack estiver pronta, execute o consumer por 30 minutos:

```sh
env OTEL_SERVICE_NAME=go-sqs-local OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=local,service.version=workspace OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 OTEL_METRIC_EXPORT_INTERVAL=2000 OTEL_BLRP_SCHEDULE_DELAY=1000 go run ./examples/opentelemetry -duration=30m
```

Ctrl+C encerra o consumer, drena os workers e exporta os dados pendentes.
Sem `-duration`, o consumer permanece ativo até Ctrl+C. Ele não gera mensagens.
Em outro terminal, publique tráfego real:

```sh
go run ./examples/publish -target otel -action cycle -count 0 -interval 500ms
```

As mensagens alternam sucesso, descarte e erro transitório. A mensagem de retry
falha na primeira entrega e sucede na redelivery pelo MiniStack.
Nenhum log ou instrumento OTel é criado manualmente
para o dashboard. O handler simula durações diferentes para alimentar os
histogramas instrumentados pela própria biblioteca.

Abra http://localhost:3000/d/go-sqs-local. Login: `admin`, senha: `admin`.
O dashboard provisionado inclui taxa de mensagens e erros, p95 do processamento,
resultados por segundo, workers ativos, total recebido e logs. Aguarde cerca
de um minuto para os gráficos de taxa terem amostras suficientes.

Em Explore, selecione Prometheus para métricas ou Loki para logs.
Logs: `{service_name="go-sqs-local"}`. A identidade do serviço precisa coincidir
com o comando acima porque o dashboard filtra esse serviço.

O nome OTLP `messaging.client.consumed.messages` é apresentado pelo Prometheus
como `messaging_client_consumed_messages_total`. Essa tradução de nomes ocorre
no backend; a biblioteca continua emitindo seu contrato OpenTelemetry original.

## Encerrar e remover

Pare o consumer e o publicador com Ctrl+C antes de remover os recursos.

```sh
docker compose -p go-sqs-local-grafana -f examples/opentelemetry/grafana/compose.yaml down
go run ./examples/setup -cleanup
docker compose down
```

Os dados são preservados no volume Docker `lgtm-data`. Para apagar os dados
locais também, use `down -v` explicitamente. As portas 3000 e 4318 são publicadas
apenas em localhost. Não há destino cloud configurado.

Para tirar essa configuração da lib depois, a stack e o dashboard estão todos
nesta pasta. O exemplo Go inicializa exporters e consome a fila `example-otel`; o código
da biblioteca não precisa mudar para essa visualização.

Referência: https://github.com/grafana/docker-otel-lgtm
