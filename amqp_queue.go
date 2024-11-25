package main

import (
	"context"
	"fmt"
	log "github.com/sirupsen/logrus"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// todo implement carrier delivery status queue in rabbitmq?

type MsgQueueType string

var MsgQueueItemType = struct {
	SMS MsgQueueType
	MMS MsgQueueType
}{
	SMS: "sms",
	MMS: "mms",
}

type MsgQueueItem struct {
	To                string       `json:"to_number"`
	From              string       `json:"from_number"`
	ReceivedTimestamp time.Time    `json:"received_timestamp"`
	QueuedTimestamp   time.Time    `json:"queued_timestamp"`
	Type              MsgQueueType `json:"type"`  // mms or sms
	Files             []MsgFile    `json:"files"` // urls or encoded base64 strings
	Message           string       `json:"message"`
	SkipNumberCheck   bool
	LogID             string `json:"log_id"`
	Delivery          *amqp.Delivery
}

// MsgFile represents an individual file extracted from the MIME multipart message.
type MsgFile struct {
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Content     []byte `json:"content,omitempty"`
	Base64Data  string `json:"base64_data,omitempty"`
}

// AMPQClient is the base struct for handling connection recovery, consumption, and publishing.
type AMPQClient struct {
	m               *sync.Mutex
	queues          []string
	logger          *log.Logger
	connection      *amqp.Connection
	channel         *amqp.Channel
	done            chan bool
	notifyConnClose chan *amqp.Error
	notifyChanClose chan *amqp.Error
	notifyConfirm   chan amqp.Confirmation
	isReady         bool
}

const (
	reconnectDelay = 5 * time.Second
	reInitDelay    = 2 * time.Second
)

// Close will cleanly shut down the channel and connection.
func (client *AMPQClient) Close() error {
	client.m.Lock()
	// we read and write isReady in two locations, so we grab the lock and hold onto
	// it until we are finished
	defer client.m.Unlock()

	if !client.isReady {
		return fmt.Errorf("connection already closed")
	}
	close(client.done)
	err := client.channel.Close()
	if err != nil {
		return err
	}
	err = client.connection.Close()
	if err != nil {
		return err
	}

	client.isReady = false
	return nil
}

// NewMsgQueueClient creates a new AMPQClient instance and attempts to connect to the server.
func NewMsgQueueClient(addr string, queues []string) *AMPQClient {
	logger := log.New()
	logger.SetFormatter(&log.TextFormatter{FullTimestamp: true})
	logger.SetLevel(log.InfoLevel)

	client := AMPQClient{
		m:      &sync.Mutex{},
		queues: queues,
		logger: logger,
		done:   make(chan bool),
	}

	go client.handleReconnect(addr)
	return &client
}

// handleReconnect handles reconnection logic
func (client *AMPQClient) handleReconnect(addr string) {
	for {
		client.m.Lock()
		client.isReady = false
		client.m.Unlock()

		client.logger.Println("Attempting to connect")
		conn, err := client.connect(addr)
		if err != nil {
			client.logger.Println("Failed to connect. Retrying...")
			select {
			case <-client.done:
				return
			case <-time.After(reconnectDelay):
			}
			continue
		}

		if done := client.handleReInit(conn); done {
			break
		}
	}
}

// connect creates a new AMQP connection
func (client *AMPQClient) connect(addr string) (*amqp.Connection, error) {
	conn, err := amqp.Dial(addr)
	if err != nil {
		return nil, err
	}
	client.changeConnection(conn)
	client.logger.Println("Connected!")
	return conn, nil
}

// handleReInit handles channel re-initialization
func (client *AMPQClient) handleReInit(conn *amqp.Connection) bool {
	for {
		client.m.Lock()
		client.isReady = false
		client.m.Unlock()

		err := client.init(conn)
		if err != nil {
			client.logger.Println("Failed to initialize channel. Retrying...")
			select {
			case <-client.done:
				return true
			case <-client.notifyConnClose:
				client.logger.Println("Connection closed. Reconnecting...")
				return false
			case <-time.After(reInitDelay):
			}
			continue
		}

		select {
		case <-client.done:
			return true
		case <-client.notifyConnClose:
			client.logger.Println("Connection closed. Reconnecting...")
			return false
		case <-client.notifyChanClose:
			client.logger.Println("Channel closed. Re-initializing...")
		}
	}
}

// init initializes channel and declares all queues
func (client *AMPQClient) init(conn *amqp.Connection) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}

	err = ch.Confirm(false)
	if err != nil {
		return err
	}

	for _, queue := range client.queues {
		_, err := ch.QueueDeclare(
			queue,
			true,  // Durable
			false, // Delete when unused
			false, // Exclusive
			false, // No-wait
			nil,   // Arguments
		)
		if err != nil {
			return fmt.Errorf("failed to declare queue '%s': %w", queue, err)
		}
		client.logger.Printf("Declared queue: %s", queue)
	}

	client.changeChannel(ch)
	client.m.Lock()
	client.isReady = true
	client.m.Unlock()
	client.logger.Println("Channel setup complete!")
	return nil
}

func (client *AMPQClient) changeConnection(conn *amqp.Connection) {
	client.connection = conn
	client.notifyConnClose = make(chan *amqp.Error, 1)
	client.connection.NotifyClose(client.notifyConnClose)
}

func (client *AMPQClient) changeChannel(ch *amqp.Channel) {
	client.channel = ch
	client.notifyChanClose = make(chan *amqp.Error, 1)
	client.notifyConfirm = make(chan amqp.Confirmation, 1)
	client.channel.NotifyClose(client.notifyChanClose)
	client.channel.NotifyPublish(client.notifyConfirm)
}

// Publish sends a message to the specified queue
func (client *AMPQClient) Publish(queueName string, data []byte) error {
	for {
		client.m.Lock()
		if !client.isReady || client.channel == nil {
			client.m.Unlock()
			time.Sleep(2 * time.Second)
			continue
		}
		client.m.Unlock()

		err := client.UnsafePublish(queueName, data)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		confirm := <-client.notifyConfirm
		if confirm.Ack {
			client.logger.Printf("Message published to %s", queueName)
			return nil
		}
	}
}

// UnsafePublish publishes a message without confirmation
func (client *AMPQClient) UnsafePublish(queueName string, data []byte) error {
	client.m.Lock()
	defer client.m.Unlock()

	if client.isReady && client.channel == nil || client.channel == nil {
		return fmt.Errorf("not connected")
	}

	if !client.isReady {
		return fmt.Errorf("not ready")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return client.channel.PublishWithContext(
		ctx,
		"",        // Exchange
		queueName, // Router key
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        data,
		},
	)
}

// ConsumeMessages starts consuming messages from a specified queue
func (client *AMPQClient) ConsumeMessages(queueName string) (<-chan amqp.Delivery, error) {
	client.m.Lock()
	defer client.m.Unlock()

	if client.isReady && client.channel == nil || client.channel == nil {
		return nil, fmt.Errorf("not connected")
	}

	if !client.isReady {
		return nil, fmt.Errorf("not ready")
	}

	if err := client.channel.Qos(1, 0, false); err != nil {
		return nil, fmt.Errorf("failed to set QoS: %w", err)
	}

	return client.channel.Consume(
		queueName,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
}
