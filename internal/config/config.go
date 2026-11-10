package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

const (
	defaultMaxConns int32 = 40
	defaultMinConns int32 = 10
)

type Config struct {
	HTTPAddr   string
	DBHost     string
	DBPort     string
	DBDatabase string
	DBUsername string
	DBPassword string
	DBMaxConns int32
	DBMinConns int32
}

func Load(path string) (Config, error) {
	return loadWithEnvLookup(path, os.LookupEnv)
}

func loadWithEnvLookup(path string, lookupEnv func(string) (string, bool)) (Config, error) {
	fileValues, err := godotenv.Read(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	maxConns, err := parseNonNegativeInt32(envValueOrFileValue(fileValues, lookupEnv, "DB_MAX_CONNS"), "DB_MAX_CONNS", defaultMaxConns)
	if err != nil {
		return Config{}, err
	}
	minConns, err := parseNonNegativeInt32(envValueOrFileValue(fileValues, lookupEnv, "DB_MIN_CONNS"), "DB_MIN_CONNS", defaultMinConns)
	if err != nil {
		return Config{}, err
	}
	if maxConns <= 0 {
		return Config{}, fmt.Errorf("config: DB_MAX_CONNS must be positive")
	}
	if minConns > maxConns {
		return Config{}, fmt.Errorf("config: DB_MIN_CONNS must not exceed DB_MAX_CONNS")
	}

	cfg := Config{
		HTTPAddr:   envValueOrFileValue(fileValues, lookupEnv, "HTTP_ADDR"),
		DBHost:     envValueOrFileValue(fileValues, lookupEnv, "DB_HOST"),
		DBPort:     envValueOrFileValue(fileValues, lookupEnv, "DB_PORT"),
		DBDatabase: envValueOrFileValue(fileValues, lookupEnv, "DB_DATABASE"),
		DBUsername: envValueOrFileValue(fileValues, lookupEnv, "DB_USERNAME"),
		DBPassword: envValueOrFileValue(fileValues, lookupEnv, "DB_PASSWORD"),
		DBMaxConns: maxConns,
		DBMinConns: minConns,
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}
	if cfg.DBHost == "" || cfg.DBPort == "" || cfg.DBDatabase == "" || cfg.DBUsername == "" {
		return Config{}, fmt.Errorf("config: DB_HOST, DB_PORT, DB_DATABASE and DB_USERNAME are required")
	}
	return cfg, nil
}

func envValueOrFileValue(fileValues map[string]string, lookupEnv func(string) (string, bool), key string) string {
	if value, ok := lookupEnv(key); ok {
		return value
	}
	return fileValues[key]
}

func (c Config) PostgresDSN() string {
	postgresURL := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUsername, c.DBPassword),
		Host:   net.JoinHostPort(c.DBHost, c.DBPort),
		Path:   c.DBDatabase,
	}
	query := postgresURL.Query()
	query.Set("sslmode", "disable")
	postgresURL.RawQuery = query.Encode()
	return postgresURL.String()
}

func parseNonNegativeInt32(raw, name string, fallback int32) (int32, error) {
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("config: %s must be a non-negative integer", name)
	}
	return int32(n), nil
}
