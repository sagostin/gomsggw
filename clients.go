package main

import "fmt"

type Client struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	Username   string         `gorm:"unique;not null" json:"username"`
	Password   string         `gorm:"not null" json:"password"`
	Address    string         `json:"address"`
	Name       string         `json:"name"`
	LogPrivacy bool           `json:"log_privacy"`
	Numbers    []ClientNumber `gorm:"foreignKey:ClientID" json:"numbers"`
}

type ClientNumber struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	ClientID uint   `gorm:"index;not null" json:"client_id"`
	Number   string `gorm:"unique;not null" json:"number"`
	Carrier  string `json:"carrier"`
}

func (gateway *Gateway) migrateSchema() error {
	if err := gateway.DB.AutoMigrate(&Client{}, &ClientNumber{}, &MediaFile{}); err != nil {
		return err
	}
	err := gateway.createIndexes()
	if err != nil {
		return err
	}
	return nil
}

func (gateway *Gateway) createIndexes() error {
	// Create index on expires_at column
	err := gateway.DB.Migrator().CreateIndex(&MediaFile{}, "ExpiresAt")
	if err != nil {
		return fmt.Errorf("failed to create index on expires_at: %v", err)
	}
	return nil
}

func (gateway *Gateway) loadClients() error {
	var clients []Client
	if err := gateway.DB.Preload("Numbers").Find(&clients).Error; err != nil {
		return err
	}

	clientMap := make(map[string]*Client)
	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	for _, client := range clients {
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

func (gateway *Gateway) addClient(client *Client) error {
	if err := gateway.DB.Create(client).Error; err != nil {
		return err
	}

	gateway.mu.Lock()
	gateway.Clients[client.Username] = client
	gateway.mu.Unlock()

	return nil
}

func (gateway *Gateway) addNumber(number *ClientNumber) error {
	if err := gateway.DB.Create(number).Error; err != nil {
		return err
	}

	gateway.mu.Lock()
	gateway.Numbers[number.Number] = number
	gateway.mu.Unlock()

	return nil
}

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
