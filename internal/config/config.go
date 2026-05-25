package config

import "os"

type Config struct {
	MongoURI  string
	DBName    string
	RedisAddr string
	GeminiKey string
	Port      string
}

func Load() *Config {
	return &Config{
		MongoURI:  getEnv("MONGO_URI", "mongodb://localhost:27017"),
		DBName:    getEnv("DB_NAME", "mydb"),
		RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),
		GeminiKey: getEnv("GEMINI_API_KEY", ""),
		Port:      getEnv("PORT", "8080"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}