package main

import (
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/kataras/iris/v12"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

var trustedProxies []string

func main() {
	// Load environment variables
	err := godotenv.Load()

	// Set log level from environment (default to info)
	logLevel := os.Getenv("LOG_LEVEL")
	switch strings.ToLower(logLevel) {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "warn", "warning":
		logrus.SetLevel(logrus.WarnLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel)
	}

	if os.Getenv("DEBUG") == "true" {
		go func() {
			err := http.ListenAndServe(os.Getenv("PPROF_LISTEN"), nil)
			if err != nil {
				return
			}
		}()
	}

	encryptionKey := os.Getenv("ENCRYPTION_KEY")
	if encryptionKey == "" {
		log.Fatal("ENCRYPTION_KEY environment variable not set")
	}

	if os.Getenv("TRUSTED_PROXIES") == "" {
		trustedProxies = []string{"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"fc00::/7"}
	} else {
		trustedProxies = strings.Split(os.Getenv("TRUSTED_PROXIES"), ",")
	}

	app := iris.New()

	gateway, err := NewGateway()
	if err != nil {
		panic(err)
	}
	// init log manager for startup

	/*amqpServerURL := os.Getenv("AMQP_SERVER_URL")
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

	gateway.AMPQClient = ampqClient*/

	err = gateway.loadCarriers()
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

	go gateway.Router.UnifiedRouter()
	go gateway.processMsgRecords()

	go gateway.cleanUpExpiredMediaFiles(15 * time.Minute)

	// Start server
	webListen := os.Getenv("WEB_LISTEN")
	if webListen == "" {
		webListen = "0.0.0.0:3000"
	}

	app.Use(ProxyIPMiddleware)

	SetupCarrierRoutes(app, gateway)
	SetupClientRoutes(app, gateway)
	SetupNumberRoutes(app, gateway)
	SetupMessageRoutes(app, gateway)
	SetupStatsRoutes(app, gateway)
	app.Get("/health", func(ctx iris.Context) {
		ctx.StatusCode(200)
		return
	})

	// Define the /reload_clients route
	// app.Get("/reload_clients", basicAuthMiddleware, gateway.webReloadClients)

	// Define the /media/{token} route (UUID-based access token for security)
	app.Get("/media/{token}", gateway.webMediaFile)
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

// isTrustedProxy checks if an IP address is in any of the trusted subnets or IPs
func isTrustedProxy(ip string, trustedProxies []string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false // Invalid IP
	}

	for _, cidr := range trustedProxies {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return false // Invalid CIDR
		}

		if network.Contains(parsedIP) {
			return true
		}
	}

	return false
}
