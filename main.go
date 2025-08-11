package main

import (
	"encoding/json"
	"log"
	"my-websocket/services/websocket"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type broadcastReq struct {
	Message string `json:"message" binding:"required"`
}

func broadcastAPI(h *websocket.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req broadcastReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
			return
		}
		// 建議在這裡加大小限制，例如 >1MB 直接拒
		payload, _ := json.Marshal(gin.H{
			"type":    "server_broadcast",
			"message": req.Message,
			"time":    time.Now().Format(time.RFC3339),
		})
		h.Broadcast(payload)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func main() {
	addr := "127.0.0.1:8080"

	// 可選參數：SendCap / MaxMessageSize / EnableCompression / CheckOrigin
	hub := websocket.NewHub(&websocket.Options{
		SendCap:           256,
		MaxMessageSize:    8192,
		EnableCompression: true,
		// CheckOrigin: func(r *http.Request) bool { return r.Host == "your.domain" },
	})
	go hub.Run()

	r := gin.Default()

	// 靜態檔
	r.Static("/public", "./public")
	r.GET("/", func(c *gin.Context) { c.File("./public/index.html") })

	// WebSocket
	r.GET("/ws", websocket.ServeWs(hub))

	// REST 廣播
	r.POST("/api/broadcast", broadcastAPI(hub))

	log.Printf("listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
