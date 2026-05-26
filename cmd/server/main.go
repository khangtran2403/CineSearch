package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/khangtran2403/searchengine/internal/config"
	"github.com/khangtran2403/searchengine/internal/handler"
	"github.com/khangtran2403/searchengine/internal/repository"
	"github.com/khangtran2403/searchengine/internal/service"
	"github.com/khangtran2403/searchengine/internal/embedding"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

func main() {
	// Load .env
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	// Logger
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	// Config
	cfg := config.Load()

	// MongoDB + Redis connections
	mongoClient, err := repository.NewMongoClient(cfg.MongoURI)
	if err != nil {
		logger.Fatal("Failed to connect MongoDB", zap.Error(err))
	}
	defer mongoClient.Disconnect(context.Background())

	redisClient := repository.NewRedisClient(cfg.RedisAddr)
	defer redisClient.Close()

	// Repositories
	db := mongoClient.Database(cfg.DBName)
	movieRepo    := repository.NewMovieRepository(db)
	eventRepo    := repository.NewEventRepository(db)
	profileRepo  := repository.NewProfileRepository(db)

	// Embedding client
	embedClient := embedding.NewGeminiClient(cfg.GeminiKey)

	// Services
	recommendSvc := service.NewRecommendService(movieRepo, eventRepo, profileRepo, redisClient, logger)
	ingestSvc    := service.NewIngestService(eventRepo,movieRepo, profileRepo, embedClient, logger)

	// HTTP Router
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger(logger))

	api := r.Group("/api/v1")
	handler.RegisterRoutes(api, recommendSvc, ingestSvc, logger)

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Graceful shutdown
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}
	go func() {
		logger.Info("CineAI server started", zap.String("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	logger.Info("Server shutdown gracefully")
}

func requestLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
		)
	}
}