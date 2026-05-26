package handler

import (
	"net/http"
	"strconv"

	"github.com/khangtran2403/searchengine/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func RegisterRoutes(
	r *gin.RouterGroup,
	recommend *service.RecommendService,
	ingest *service.IngestService,
	log *zap.Logger,
) {
	r.GET("/recommend/:user_id", recommendHandler(recommend, log))
	r.POST("/events", ingestHandler(ingest, log))
}
func recommendHandler(svc *service.RecommendService, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.Param("user_id")
		if userID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_id required"})
			return
		}

		limit := 10
		if l := c.Query("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 50 {
				limit = v
			}
		}

		device := c.DefaultQuery("device", "desktop")

		resp, err := svc.Recommend(c.Request.Context(), userID, limit, device)
		if err != nil {
			log.Error("recommend failed", zap.String("user", userID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "recommendation failed"})
			return
		}

		c.JSON(http.StatusOK, resp)
	}
}

func ingestHandler(svc *service.IngestService, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.IngestRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err := svc.Ingest(c.Request.Context(), &req); err != nil {
			log.Error("ingest failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "ingest failed"})
			return
		}

		c.JSON(http.StatusAccepted, gin.H{"status": "ok"})
	}
}