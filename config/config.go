package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AppPort    string
	BinanceKey string
	BybitKey   string
	MexcKey    string
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, reading from environment directly")
	}

	return &Config{
		AppPort:    getEnv("APP_PORT", "3000"),
		BinanceKey: getEnv("BINANCE_API_KEY", ""),
		BybitKey:   getEnv("BYBIT_API_KEY", ""),
		MexcKey:    getEnv("MEXC_API_KEY", ""),
	}
}

func getEnv(key string, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
