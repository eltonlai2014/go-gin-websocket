package websocket

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

// Options 允許調整佇列大小、最大訊息大小、壓縮與跨網域驗證
type Options struct {
	SendCap           int
	MaxMessageSize    int
	EnableCompression bool
	CheckOrigin       func(r *http.Request) bool
}

func (o *Options) withDefaults() {
	if o.SendCap <= 0 {
		o.SendCap = 128
	}
	if o.MaxMessageSize <= 0 {
		o.MaxMessageSize = 8192
	}
	if o.CheckOrigin == nil {
		o.CheckOrigin = func(r *http.Request) bool { return true }
	}
}

// Hub: 管理所有連線
type Hub struct {
	clients    map[*client]bool
	broadcast  chan []byte
	register   chan *client
	unregister chan *client

	// 設定
	opts Options
}

func NewHub(opts *Options) *Hub {
	o := Options{}
	if opts != nil {
		o = *opts
	}
	o.withDefaults()
	return &Hub{
		clients:    make(map[*client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *client),
		unregister: make(chan *client),
		opts:       o,
	}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
		case c := <-h.unregister:
			if h.clients[c] {
				delete(h.clients, c)
				close(c.send)
			}
		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// 背壓：丟掉最舊一筆再試；仍滿則視為過慢，斷線
					select {
					case <-c.send:
					default:
					}
					select {
					case c.send <- msg:
					default:
						close(c.send)
						delete(h.clients, c)
					}
				}
			}
		}
	}
}

// 對外提供安全的廣播入口
func (h *Hub) Broadcast(b []byte) {
	h.broadcast <- b
}

// --- client ---

type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(int64(c.hub.opts.MaxMessageSize))
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		c.hub.broadcast <- message
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			// 一則訊息一個 frame，避免越併越大
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// --- WebSocket handler ---

func ServeWs(h *Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		upgrader := websocket.Upgrader{
			ReadBufferSize:    1024,
			WriteBufferSize:   1024,
			EnableCompression: h.opts.EnableCompression,
			CheckOrigin:       h.opts.CheckOrigin,
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("upgrade error: %v", err)
			return
		}
		cl := &client{
			hub:  h,
			conn: conn,
			send: make(chan []byte, h.opts.SendCap),
		}
		h.register <- cl

		go cl.writePump()
		go cl.readPump()
	}
}
