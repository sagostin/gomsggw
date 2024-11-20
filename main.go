package main

import (
	"github.com/joho/godotenv"
	"github.com/kataras/iris/v12"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"net/http"
	_ "net/http/pprof"
	"os"
)

func main() {
	// Load environment variables
	err := godotenv.Load()

	if os.Getenv("DEBUG") == "true" {
		go func() {
			err := http.ListenAndServe(os.Getenv("PPROF_LISTEN"), nil)
			if err != nil {
				return
			}
		}()
	}

	app := iris.New()

	gateway, err := NewGateway()
	if err != nil {
		panic(err)
	}
	// init log manager for startup

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

	err = loadCarriers(gateway)
	if err != nil {
		panic(err)
	}

	for _, c := range gateway.Carriers {
		gateway.Router.AddRoute("carrier", c.Name(), c)
	}

	go func() {
		smppServer, err := initSmppServer()
		if err != nil {
			var lm = gateway.LogManager
			lm.SendLog(lm.BuildLog(
				"System.Startup.SMPP",
				"GenericError",
				logrus.ErrorLevel,
				/*		map[string]interface{}{
						"module": "Configuration",
					},*/
				nil,
				err,
			))
			panic(err)
		}
		gateway.SMPPServer = smppServer
		smppServer.gateway = gateway

		smppServer.Start(gateway)
	}()

	go func() {
		mm4Server := &MM4Server{
			Addr:    os.Getenv("MM4_LISTEN"),
			routing: gateway.Router,
		}
		gateway.MM4Server = mm4Server
		mm4Server.gateway = gateway

		err := mm4Server.Start()
		if err != nil {
			var lm = gateway.LogManager
			lm.SendLog(lm.BuildLog(
				"System.Startup.MM4",
				"GenericError",
				logrus.ErrorLevel,
				/*		map[string]interface{}{
						"module": "Configuration",
					},*/
				nil,
				err,
			))
			panic(err)
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
			var lm = gateway.LogManager
			lm.SendLog(lm.BuildLog(
				"System.Startup.Prometheus",
				"GenericError",
				logrus.ErrorLevel,
				/*		map[string]interface{}{
						"module": "Configuration",
					},*/
				nil,
				err,
			))
		}
	}()

	go gateway.Router.ClientRouter()
	go gateway.Router.CarrierRouter()
	go gateway.Router.ClientMsgConsumer()
	go gateway.Router.CarrierMsgConsumer()

	// Start server
	webListen := os.Getenv("WEB_LISTEN")
	if webListen == "" {
		webListen = "0.0.0.0:3000"
	}

	app.Post("/clients", basicAuthMiddleware, gateway.webAddClient)
	app.Post("/numbers", basicAuthMiddleware, gateway.webAddNumber)
	app.Get("/reload_data", basicAuthMiddleware, gateway.webReloadData)

	// Define the /reload_clients route
	// app.Get("/reload_clients", basicAuthMiddleware, gateway.webReloadClients)

	// Define the /media/{id} route
	app.Get("/media/{id}", gateway.webMediaFile)
	// Define the /inbound/{carrier} route
	app.Post("/inbound/{carrier}", gateway.webInboundCarrier)

	err = app.Listen(webListen)
	if err != nil {
		var lm = gateway.LogManager
		lm.SendLog(lm.BuildLog(
			"System.Startup.Web",
			"GenericError",
			logrus.ErrorLevel,
			/*		map[string]interface{}{
					"module": "Configuration",
				},*/
			nil,
			err,
		))
	}
}
