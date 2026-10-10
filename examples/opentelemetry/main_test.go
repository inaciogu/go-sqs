package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// These tests use the same providers, exporters and scenario as go run.
// They assert the decoded wire payload, rather than SDK objects in memory.
func TestOTLPExport(t *testing.T) {
	for _, specific := range []bool{false, true} {
		name := "common environment"
		if specific {
			name = "signal overrides and gzip"
		}
		t.Run(name, func(t *testing.T) {
			cleanEnvironment(t)
			t.Setenv("OTEL_SERVICE_NAME", "otel-example-test")
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=overridden,deployment.environment.name=local,service.version=test")
			// Prevent timer-driven export: all evidence must survive shutdown.
			t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "60000")
			t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "60000")
			var mu sync.Mutex
			var metrics []*collectormetric.ExportMetricsServiceRequest
			var logs []*collectorlog.ExportLogsServiceRequest
			var problems []string
			metricPath, logPath := "/v1/metrics", "/v1/logs"
			if specific {
				metricPath, logPath = "/custom/metrics", "/custom/logs"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-protobuf" {
					problems = append(problems, "unexpected method or content type")
				}
				wantHeader := "common"
				if specific && r.URL.Path == metricPath {
					wantHeader = "metrics"
				}
				if specific && r.URL.Path == logPath {
					wantHeader = "logs"
				}
				if r.Header.Get("X-Example") != wantHeader {
					problems = append(problems, "wrong environment header")
				}
				var body io.Reader = r.Body
				if specific {
					if r.Header.Get("Content-Encoding") != "gzip" {
						problems = append(problems, "missing gzip encoding")
					}
					reader, err := gzip.NewReader(r.Body)
					if err != nil {
						problems = append(problems, err.Error())
						w.WriteHeader(400)
						return
					}
					defer reader.Close()
					body = reader
				}
				data, err := io.ReadAll(body)
				if err != nil {
					problems = append(problems, err.Error())
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case metricPath:
					request := new(collectormetric.ExportMetricsServiceRequest)
					if err := proto.Unmarshal(data, request); err != nil {
						problems = append(problems, err.Error())
					}
					metrics = append(metrics, request)
				case logPath:
					request := new(collectorlog.ExportLogsServiceRequest)
					if err := proto.Unmarshal(data, request); err != nil {
						problems = append(problems, err.Error())
					}
					logs = append(logs, request)
				default:
					problems = append(problems, "wrong environment endpoint: "+r.URL.Path)
					w.WriteHeader(404)
					return
				}
				// Empty protobuf is a valid successful OTLP Export response.
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer server.Close()
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
			t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-example=common")
			if specific {
				// The common endpoint must not receive either signal.
				t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL+"/unused")
				t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", server.URL+metricPath)
				t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", server.URL+logPath)
				t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "x-example=metrics")
				t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "x-example=logs")
				t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
			}
			require.NoError(t, run(t.Context()))
			mu.Lock()
			defer mu.Unlock()
			require.Empty(t, problems)
			require.Len(t, metrics, 1, "shutdown must export pending metrics")
			require.Len(t, logs, 1, "shutdown must export pending logs")
			seen := make(map[string]*metricpb.Metric)
			for _, rm := range metrics[0].ResourceMetrics {
				assertResource(t, rm.Resource.Attributes)
				for _, scope := range rm.ScopeMetrics {
					require.Equal(t, "github.com/inaciogu/go-sqs/v2", scope.Scope.Name)
					for _, m := range scope.Metrics {
						seen[m.Name] = m
					}
				}
			}
			require.Len(t, seen, 7)
			require.Equal(t, int64(3), sum(seen["messaging.client.consumed.messages"]))
			require.Equal(t, "{message}", seen["messaging.client.consumed.messages"].Unit)
			require.Equal(t, int64(1), sum(seen["gosqs.errors"]))
			require.Zero(t, sum(seen["gosqs.workers.active"]))
			require.Zero(t, sum(seen["gosqs.capacity.used"]))
			outcomes := map[string]uint64{}
			for _, p := range seen["messaging.process.duration"].GetHistogram().DataPoints {
				outcomes[stringAttr(p.Attributes, "gosqs.handler.result")] += p.Count
				require.Equal(t, "aws_sqs", stringAttr(p.Attributes, "messaging.system"))
				require.Equal(t, "otel-demo", stringAttr(p.Attributes, "messaging.destination.name"))
				require.Equal(t, []float64{.005, .01, .025, .05, .075, .1, .25, .5, .75, 1, 2.5, 5, 7.5, 10}, p.ExplicitBounds)
			}
			require.Equal(t, map[string]uint64{"success": 1, "drop": 1, "error": 1}, outcomes)
			shutdown := seen["gosqs.shutdown.duration"].GetHistogram().DataPoints
			require.Len(t, shutdown, 1)
			require.Equal(t, uint64(1), shutdown[0].Count)
			require.Equal(t, "complete", stringAttr(shutdown[0].Attributes, "gosqs.shutdown.result"))
			var records []*logpb.LogRecord
			for _, rl := range logs[0].ResourceLogs {
				assertResource(t, rl.Resource.Attributes)
				for _, scope := range rl.ScopeLogs {
					require.Equal(t, "github.com/inaciogu/go-sqs/v2", scope.Scope.Name)
					records = append(records, scope.LogRecords...)
				}
			}
			require.NotEmpty(t, records)
			var handlerWarnings int
			for _, r := range records {
				require.NotEmpty(t, r.Body.GetStringValue())
				require.NotZero(t, r.TimeUnixNano)
				if r.SeverityNumber == logpb.SeverityNumber_SEVERITY_NUMBER_WARN && stringAttr(r.Attributes, "operation") == "handler" {
					handlerWarnings++
				}
				wire := r.String()
				require.NotContains(t, wire, "example-receipt")
				require.NotContains(t, wire, "example handler failure")
			}
			require.Equal(t, 1, handlerWarnings)
		})
	}
}

func TestOTLPExportRejection(t *testing.T) {
	cleanEnvironment(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	require.Error(t, run(t.Context()), "a rejected export must not report successful validation")
}

func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_METRIC_EXPORT_INTERVAL", "OTEL_METRIC_EXPORT_TIMEOUT", "OTEL_BLRP_SCHEDULE_DELAY", "OTEL_BLRP_EXPORT_TIMEOUT"} {
		t.Setenv(key, "")
	}
	for _, signal := range []string{"", "METRICS_", "LOGS_"} {
		for _, option := range []string{"ENDPOINT", "HEADERS", "COMPRESSION", "TIMEOUT", "CERTIFICATE", "CLIENT_CERTIFICATE", "CLIENT_KEY", "INSECURE"} {
			t.Setenv("OTEL_EXPORTER_OTLP_"+signal+option, "")
		}
	}
	for _, option := range []string{"TEMPORALITY_PREFERENCE", "DEFAULT_HISTOGRAM_AGGREGATION"} {
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_"+option, "")
	}
}
func assertResource(t *testing.T, attrs []*common.KeyValue) {
	t.Helper()
	require.Equal(t, "otel-example-test", stringAttr(attrs, "service.name"))
	require.Equal(t, "local", stringAttr(attrs, "deployment.environment.name"))
	require.Equal(t, "test", stringAttr(attrs, "service.version"))
	require.Equal(t, "go", stringAttr(attrs, "telemetry.sdk.language"))
}
func stringAttr(attrs []*common.KeyValue, key string) string {
	for _, a := range attrs {
		if strings.EqualFold(a.Key, key) {
			return a.Value.GetStringValue()
		}
	}
	return ""
}
func sum(m *metricpb.Metric) int64 {
	var total int64
	for _, p := range m.GetSum().GetDataPoints() {
		total += p.GetAsInt()
	}
	return total
}
