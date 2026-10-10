# Grafana local para go-sqs

Configuração temporária e isolada para uso pessoal. O container LGTM contém
Collector, Prometheus, Loki e Grafana. O consumer real da biblioteca produz os
logs e métricas; o exemplo Go inicializa os providers/exporters OTLP. Apenas
o cliente SQS é simulado. Não é necessário AWS, cloud ou o emulator SQS/SNS.

## Iniciar

Execute da raiz do repositório. Pare o Collector do exemplo anterior se ele
estiver ocupando a porta 4318.

```sh
rtk docker compose -p go-sqs-local-grafana -f examples/opentelemetry/grafana/compose.yaml up -d
rtk docker compose -p go-sqs-local-grafana -f examples/opentelemetry/grafana/compose.yaml logs --tail=40
```

Quando a stack estiver pronta, execute o consumer por 30 minutos:

```sh
rtk proxy env OTEL_SERVICE_NAME=go-sqs-local OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=local,service.version=workspace OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 OTEL_METRIC_EXPORT_INTERVAL=2000 OTEL_BLRP_SCHEDULE_DELAY=1000 rtk go run ./examples/opentelemetry -duration=30m
```

Ctrl+C encerra o consumer, drena os workers e exporta os dados pendentes.
Sem `-duration`, o exemplo mantém seu cenário original de três mensagens.
O modo contínuo alterna sucesso, descarte e erro de handler a cada entrega;
as falhas são intencionais. Nenhum log ou instrumento OTel é criado manualmente
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

```sh
rtk docker compose -p go-sqs-local-grafana -f examples/opentelemetry/grafana/compose.yaml down
```

Os dados são preservados no volume Docker `lgtm-data`. Para apagar os dados
locais também, use `down -v` explicitamente. As portas 3000 e 4318 são publicadas
apenas em localhost. Não há destino cloud configurado.

Para tirar essa configuração da lib depois, a stack e o dashboard estão todos
nesta pasta. O único complemento ao exemplo Go é o modo `-duration`; o código
da biblioteca não precisa mudar para essa visualização.

Referência: https://github.com/grafana/docker-otel-lgtm
