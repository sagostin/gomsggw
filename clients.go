package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
)

type Client struct {
	Username string         `json:"username"`
	Password string         `json:"password"`
	Numbers  []ClientNumber `json:"numbers"`
}

type ClientNumber struct {
	Number  string `json:"number"`
	Carrier string `json:"carrier"`
}

func loadClients() ([]Client, error) {
	data, err := ioutil.ReadFile(os.Getenv("CLIENTS_CONFIG_PATH"))
	if err != nil {
		return nil, err
	}

	var clients []Client
	err = json.Unmarshal(data, &clients)
	if err != nil {
		return nil, err
	}

	return clients, nil
}

func authClient(username string, password string, clients map[string]*Client) (bool, error) {
	client, exists := clients[username]
	if !exists {
		return false, nil
	}
	return client.Password == password, nil
}
