package main

import (
	"fmt"
	"github.com/sirupsen/logrus"
)

type Client struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	Username   string         `gorm:"unique;not null" json:"username"`
	Password   string         `gorm:"not null" json:"password"` // this can also be used for api key for authenticating for web hook integration?
	Address    string         `json:"address"`
	Name       string         `json:"name"`
	LogPrivacy bool           `json:"log_privacy"`
	Numbers    []ClientNumber `gorm:"foreignKey:ClientID" json:"numbers"`
}

type ClientNumber struct {
	ID                   uint   `gorm:"primaryKey" json:"id"`
	ClientID             uint   `gorm:"index;not null" json:"client_id"`
	Number               string `gorm:"unique;not null" json:"number"`
	Carrier              string `json:"carrier"`
	IgnoreStopCmdSending bool   `json:"ignore_stop_cmd_sending"`
	WebHook              string `json:"webhook"` // this is the spot to send the web hook request for if we "receive" from the carrier
}

// loadClients loads clients from the database, decrypts their credentials, and populates the in-memory map.
func (gateway *Gateway) loadClients() error {
	var clients []Client
	if err := gateway.DB.Preload("Numbers").Find(&clients).Error; err != nil {
		return err
	}

	clientMap := make(map[string]*Client)
	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	for _, client := range clients {
		// Decrypt Username and Password
		decryptedUsername, err := DecryptPassword(client.Username, gateway.EncryptionKey)
		if err != nil {
			return fmt.Errorf("failed to decrypt username for client %s: %w", client.Name, err)
		}
		decryptedPassword, err := DecryptPassword(client.Password, gateway.EncryptionKey)
		if err != nil {
			return fmt.Errorf("failed to decrypt password for client %s: %w", client.Name, err)
		}

		// Update client struct with decrypted credentials
		client.Username = decryptedUsername
		client.Password = decryptedPassword

		c := client // create a copy to avoid referencing the loop variable
		clientMap[client.Username] = &c
	}

	gateway.Clients = clientMap
	return nil
}

func (gateway *Gateway) loadNumbers() error {
	var numbers []ClientNumber
	if err := gateway.DB.Find(&numbers).Error; err != nil {
		return err
	}

	numberMap := make(map[string]*ClientNumber)
	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	for _, number := range numbers {
		n := number // create a copy to avoid referencing the loop variable
		numberMap[number.Number] = &n
	}

	gateway.Numbers = numberMap
	return nil
}

// addClient encrypts the client's credentials and stores the client in the database and in-memory map.
func (gateway *Gateway) addClient(client *Client) error {
	// Encrypt Username and Password
	encryptedUsername, err := EncryptPassword(client.Username, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt username: %w", err)
	}
	encryptedPassword, err := EncryptPassword(client.Password, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt password: %w", err)
	}

	client.Username = encryptedUsername
	client.Password = encryptedPassword

	// Store in the database
	if err := gateway.DB.Create(client).Error; err != nil {
		return err
	}

	// Decrypt credentials for in-memory map
	decryptedUsername, err := DecryptPassword(client.Username, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt username after encryption: %w", err)
	}
	decryptedPassword, err := DecryptPassword(client.Password, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt password after encryption: %w", err)
	}

	// Update client struct with decrypted credentials
	client.Username = decryptedUsername
	client.Password = decryptedPassword

	gateway.mu.Lock()
	gateway.Clients[client.Username] = client
	gateway.mu.Unlock()

	return nil
}

// addNumber encrypts and adds a new number to a client.
func (gateway *Gateway) addNumber(clientUsername string, number *ClientNumber) error {
	// Check if the client exists
	gateway.mu.RLock()
	client, exists := gateway.Clients[clientUsername]
	gateway.mu.RUnlock()
	if !exists {
		return fmt.Errorf("client with username %s does not exist", client.Username)
	}

	// Validate if the carrier exists
	gateway.mu.RLock()
	_, carrierExists := gateway.Carriers[number.Carrier]
	gateway.mu.RUnlock()
	if !carrierExists {
		return fmt.Errorf("carrier %s does not exist", number.Carrier)
	}

	// Check if the number already exists
	gateway.mu.RLock()
	_, numberExists := gateway.Numbers[number.Number]
	gateway.mu.RUnlock()
	if numberExists {
		return fmt.Errorf("number %s already exists", number.Number)
	}

	number.ClientID = client.ID

	// Create the number in the database
	if err := gateway.DB.Create(number).Error; err != nil {
		return fmt.Errorf("failed to add number to database: %w", err)
	}

	// Add the number to the in-memory map
	gateway.mu.Lock()
	gateway.Numbers[number.Number] = number
	gateway.mu.Unlock()

	// Log the addition
	gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
		"Client.AddNumber",
		fmt.Sprintf("Added number %s to client %s", number.Number, client.Username),
		logrus.InfoLevel,
		map[string]interface{}{
			"client_id": client.ID,
			"number":    number.Number,
			"carrier":   number.Carrier,
		},
	))

	return nil
}

// reloadClientsAndNumbers reloads clients and numbers from the database.
func (gateway *Gateway) reloadClientsAndNumbers() error {
	if err := gateway.loadClients(); err != nil {
		return err
	}
	if err := gateway.loadNumbers(); err != nil {
		return err
	}
	return nil
}

func (gateway *Gateway) authClient(username string, password string) (bool, error) {
	gateway.mu.RLock()
	defer gateway.mu.RUnlock()

	client, exists := gateway.Clients[username]
	if !exists {
		return false, nil
	}
	return client.Password == password, nil
}
