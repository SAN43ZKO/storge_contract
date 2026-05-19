package config

import "os"

type Config struct {
	ServerPort string
	FilesDir   string
}

func Load() *Config {
	return &Config{
		ServerPort: getEnv("SERVER_PORT", "8081"),
		FilesDir:   getEnv("FILE_DIR", "./files"),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}
