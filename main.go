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

	gateway, err := NewGateway(os.Getenv("MONGODB_URI"))
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = err
		logf.Message = "failed to create gateway"
		logf.Print()
		os.Exit(1)
	}

	err = loadCarriers(gateway)
	if err != nil {
		logf.Level = logrus.ErrorLevel
		logf.Error = err
		logf.Message = "failed to load carriers"
		logf.Print()
		os.Exit(1)
	}

	for _, c := range gateway.Carriers {
		gateway.Routing.AddRoute("carrier", c.Name(), c)
	}

	go func() {
		smppServer, err := initSmppServer()
		if err != nil {
			logf.Level = logrus.ErrorLevel
			logf.Error = err
			logf.Message = "failed to create MM4 server"
			logf.Print()
			os.Exit(1)
		}
		smppServer.routing = gateway.Routing
		gateway.SMPPServer = smppServer

		smppServer.Start(gateway)
	}()

	go func() {
		mm4Server := &MM4Server{
			Addr:    os.Getenv("MM4_LISTEN"),
			mongo:   gateway.MongoClient,
			routing: gateway.Routing,
		}
		gateway.MM4Server = mm4Server

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
