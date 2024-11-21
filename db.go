package main

import "fmt"

func (gateway *Gateway) createIndexes() error {
	// Create index on expires_at column
	err := gateway.DB.Migrator().CreateIndex(&MediaFile{}, "ExpiresAt")
	if err != nil {
		return fmt.Errorf("failed to create index on expires_at: %v", err)
	}
	return nil
}
