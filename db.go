package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// DB is the struct that holds the database connection pool
type DB struct {
	pool *pgxpool.Pool
}

// NewDB is a factory function to initialize a new DB instance
func NewDB() (*DB, error) {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Printf("Error loading .env file: %v", err)
	}

	// Construct the database URL
	dbURL := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s",
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"),
	)

	// Parse configuration for pgxpool
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("unable to parse database URL: %w", err)
	}

	config.MaxConns = 10
	config.MinConns = 2
	config.HealthCheckPeriod = 5 * time.Minute
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 15 * time.Minute

	// Create the connection pool
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to database: %w", err)
	}

	// Test the connection
	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("unable to ping database: %w", err)
	}

	// Return the initialized DB instance
	return &DB{pool: pool}, nil
}

// Close releases the database connection pool resources
func (db *DB) Close() {
	db.pool.Close()
}

// QueryRow is an example method to run a query
func (db *DB) QueryRow(query string) (time.Time, error) {
	var currentTime time.Time
	err := db.pool.QueryRow(context.Background(), query).Scan(&currentTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("query failed: %w", err)
	}
	return currentTime, nil
}

// InsertStruct inserts a struct into the database
func (db *DB) InsertStruct(table string, data interface{}) error {
	v := reflect.ValueOf(data)
	t := reflect.TypeOf(data)

	// Generate column names and values dynamically
	columns := []string{}
	values := []interface{}{}
	placeholders := []string{}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		columns = append(columns, field.Tag.Get("json"))
		values = append(values, v.Field(i).Interface())
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, joinStrings(columns, ", "), joinStrings(placeholders, ", "))

	_, err := db.pool.Exec(context.Background(), query, values...)
	if err != nil {
		return fmt.Errorf("insert struct failed: %w", err)
	}
	return nil
}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
