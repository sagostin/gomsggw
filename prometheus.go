package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"time"
)

// PrometheusExporter is a general structure to expose metrics on specified paths.
type PrometheusExporter struct {
	Path      string // e.g., "/metrics"
	Listen    string // e.g., ":2550"
	startTime time.Time
}

// Start begins the HTTP server to serve Prometheus metrics.
func (e *PrometheusExporter) Start() error {
	http.Handle(e.Path, promhttp.Handler())
	return http.ListenAndServe(e.Listen, nil)
}

// MetricExporter for managing and exposing Prometheus metrics.
type MetricExporter struct {
	desc    map[string]*prometheus.Desc
	id      string
	gateway *Gateway // Reference to the Gateway to access metrics data
}

// NewMetricExporter initializes the MetricExporter with descriptions for each required metric.
func NewMetricExporter(id string, gateway *Gateway) *MetricExporter {
	metricDesc := map[string]*prometheus.Desc{
		"connected_clients": prometheus.NewDesc("connected_clients", "Number of connected clients", []string{"protocol"}, nil),
		"total_clients":     prometheus.NewDesc("total_clients", "Total number of clients", []string{"protocol"}, nil),
		"message_retries":   prometheus.NewDesc("message_retries", "Count of message retries", []string{"protocol"}, nil),
		"messages_sent":     prometheus.NewDesc("messages_sent", "Messages sent in the last minute", []string{"protocol", "direction"}, nil),
		"server_status":     prometheus.NewDesc("server_status", "General OK status of the server", []string{"service"}, nil),
		"client_stats":      prometheus.NewDesc("client_stats", "Total clients and numbers", []string{"protocol", "stat"}, nil),
	}

	return &MetricExporter{
		desc:    metricDesc,
		id:      id,
		gateway: gateway,
	}
}

// Describe sends all metric descriptions to the Prometheus channel.
func (e *MetricExporter) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range e.desc {
		ch <- desc
	}
}

// Collect gathers metrics by examining the state of the Gateway.
func (e *MetricExporter) Collect(ch chan<- prometheus.Metric) {
	e.collectConnectedClients(ch)
	e.collectClientStats(ch)
	e.collectMessageMetrics(ch)
	e.collectServerStatus(ch)
}

// collectConnectedClients collects the count of connected clients for both SMPP and MM4.
func (e *MetricExporter) collectConnectedClients(ch chan<- prometheus.Metric) {
	connectedClientsSMPP := len(e.gateway.SMPPServer.conns)

	ch <- prometheus.MustNewConstMetric(e.desc["connected_clients"], prometheus.GaugeValue, float64(connectedClientsSMPP), "SMPP")
	/*ch <- prometheus.MustNewConstMetric(e.desc["connected_clients"], prometheus.GaugeValue, float64(connectedClientsMM4), "MM4")*/
}

// collectClientStats collects stats related to the clients and numbers for both SMPP and MM4.
func (e *MetricExporter) collectClientStats(ch chan<- prometheus.Metric) {
	totalClientsSMPP := len(e.gateway.SMPPServer.conns)
	totalNumbersSMPP := 0
	for _, client := range e.gateway.Clients {
		totalNumbersSMPP += len(client.Numbers)
	}

	totalClientsMM4 := len(e.gateway.MM4Server.GatewayClients)
	totalNumbersMM4 := 0
	for _, client := range e.gateway.MM4Server.GatewayClients {
		totalNumbersMM4 += len(client.Numbers)
	}

	ch <- prometheus.MustNewConstMetric(e.desc["client_stats"], prometheus.GaugeValue, float64(totalClientsSMPP), "SMPP", "total_clients")
	ch <- prometheus.MustNewConstMetric(e.desc["client_stats"], prometheus.GaugeValue, float64(totalNumbersSMPP), "SMPP", "total_numbers")
	ch <- prometheus.MustNewConstMetric(e.desc["client_stats"], prometheus.GaugeValue, float64(totalClientsMM4), "MM4", "total_clients")
	ch <- prometheus.MustNewConstMetric(e.desc["client_stats"], prometheus.GaugeValue, float64(totalNumbersMM4), "MM4", "total_numbers")
}

// collectMessageMetrics gathers retry and message sent metrics for both SMPP and MM4.
func (e *MetricExporter) collectMessageMetrics(ch chan<- prometheus.Metric) {
	// Placeholder message retries for SMPP and MM4
	messageRetriesSMPP := 10 // Example value for SMS retries
	messageRetriesMM4 := 5   // Example value for MMS retries

	ch <- prometheus.MustNewConstMetric(e.desc["message_retries"], prometheus.CounterValue, float64(messageRetriesSMPP), "SMPP")
	ch <- prometheus.MustNewConstMetric(e.desc["message_retries"], prometheus.CounterValue, float64(messageRetriesMM4), "MM4")

	// Placeholder messages sent counts for SMPP and MM4 (inbound and outbound)
	messagesSentInboundSMPP := 100 // Example count
	messagesSentOutboundSMPP := 80 // Example count
	messagesSentInboundMM4 := 60   // Example count
	messagesSentOutboundMM4 := 50  // Example count

	ch <- prometheus.MustNewConstMetric(e.desc["messages_sent"], prometheus.CounterValue, float64(messagesSentInboundSMPP), "SMPP", "inbound")
	ch <- prometheus.MustNewConstMetric(e.desc["messages_sent"], prometheus.CounterValue, float64(messagesSentOutboundSMPP), "SMPP", "outbound")
	ch <- prometheus.MustNewConstMetric(e.desc["messages_sent"], prometheus.CounterValue, float64(messagesSentInboundMM4), "MM4", "inbound")
	ch <- prometheus.MustNewConstMetric(e.desc["messages_sent"], prometheus.CounterValue, float64(messagesSentOutboundMM4), "MM4", "outbound")
}

// collectServerStatus checks health of each server (SMPP and MM4).
func (e *MetricExporter) collectServerStatus(ch chan<- prometheus.Metric) {
	// Placeholder health status: 1 indicates OK
	smppStatus := 1 // Example OK status for SMPP server
	mm4Status := 1  // Example OK status for MM4 server

	ch <- prometheus.MustNewConstMetric(e.desc["server_status"], prometheus.GaugeValue, float64(smppStatus), "SMPPServer")
	ch <- prometheus.MustNewConstMetric(e.desc["server_status"], prometheus.GaugeValue, float64(mm4Status), "MM4Server")
}
