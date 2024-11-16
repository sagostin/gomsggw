package main

import (
	"github.com/joho/godotenv"
	"github.com/kataras/iris/v12"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"log"
	"os"
)

func main() {
	logf := LoggingFormat{Type: LogType.Startup}

	// Load environment variables
	err := godotenv.Load()
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = err
	}

	app := iris.New()

	gateway, err := NewGateway()
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = err
		logf.Message = "failed to create gateway"
		logf.Print()
		os.Exit(1)
	}

	// Create a new RabbitMQ connection.
	// Define RabbitMQ server URL.
	amqpServerURL := os.Getenv("AMQP_SERVER_URL")
	addr := amqpServerURL
	// we don't need a from client because we directly place it in the to_carrier one
	queues := []string{"carrier", "client"}

	ampqClient := NewMsgQueueClient(addr, queues)
	defer func(client *AMPQClient) {
		err := client.Close()
		if err != nil {
			panic(err)
		}
	}(ampqClient)

	gateway.AMPQClient = ampqClient

	go gateway.Router.ClientRouter()
	go gateway.Router.ClientMsgConsumer()
	//go gateway.Router.CarrierMsgConsumer()

	// todo start router

	// Start consumers for inbound queues
	/*go GenericConsumerHandler(client, "outbound_to_carrier")
	go GenericConsumerHandler(client, "outbound_to_client")*/

	// Simulate publishing messages
	/*queueItem := MsgQueueItem{
		To:                "17789070844", // support e.164??
		From:              "12509796725", // support e.164??
		ReceivedTimestamp: time.Now(),
		QueuedTimestamp:   time.Now(),
		Type:              "sms",
		Files:             nil,
		Message:           "Hello World!",
	}

	testMessage, _ := json.Marshal(queueItem)

	go func() {
		for {
			ampqClient.Publish("client", testMessage)
			time.Sleep(15 * time.Second)
		}
	}()*/

	go func() {
		smppServer, err := initSmppServer()
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Error = err
			logf.Message = "failed to create MM4 server"
			logf.Print()
			os.Exit(1)
		}
		gateway.SMPPServer = smppServer
		smppServer.gateway = gateway

		smppServer.Start(gateway)
	}()

	go func() {
		mm4Server := &MM4Server{
			Addr:    os.Getenv("MM4_LISTEN"),
			mongo:   gateway.MongoClient,
			routing: gateway.Router,
		}
		gateway.MM4Server = mm4Server
		mm4Server.gateway = gateway

		err := mm4Server.Start()
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Error = err
			logf.Message = "failed to create MM4 server"
			logf.Print()
			os.Exit(1)
		}
	}()

	// Create and register the exporter with Prometheus
	exporter := NewMetricExporter("gateway_metrics", gateway)
	prometheus.MustRegister(exporter)

	// Start the Prometheus HTTP server
	prometheusExporter := PrometheusExporter{
		Path:   os.Getenv("PROMETHEUS_PATH"),
		Listen: os.Getenv("PROMETHEUS_LISTEN"),
	}

	go func() {
		if err := prometheusExporter.Start(); err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Error = err
			logf.Message = "failed to start prometheus exporter"
			logf.Print()
		}
	}()

	// Start server
	webListen := os.Getenv("WEB_LISTEN")
	if webListen == "" {
		webListen = "0.0.0.0:3000"
	}

	// Define the /reload_clients route
	app.Get("/reload_clients", basicAuthMiddleware, gateway.webReloadClients)

	// Define the /media/{id} route
	app.Get("/media/{id}", gateway.webMediaFile)
	// Define the /inbound/{carrier} route
	app.Post("/inbound/{carrier}", gateway.webInboundCarrier)

	err = app.Listen(webListen)
	if err != nil {
		log.Println(err)
	}
}
