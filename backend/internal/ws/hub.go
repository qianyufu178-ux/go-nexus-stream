// Package ws 实现向 Vue 3 大屏广播实时指标 WebSocket 服务。
package ws

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// writeWait 单条写超时。
	writeWait = 10 * time.Second
	// pongWait 读超时（超过则判定掉线）。
	pongWait = 60 * time.Second
	// pingPeriod 心跳间隔，必须小于 pongWait。
	pingPeriod = (pongWait * 9) / 10
	// maxMessageSize 前端上行消息上限（本协议前端基本不发消息）。
	maxMessageSize = 1024
	// clientBuffer 每个客户端的发送缓冲，满了说明是慢消费者，直接断开。
	clientBuffer = 16
)

// Hub 管理所有 WebSocket 连接并提供广播能力。
type Hub struct {
	upgrader websocket.Upgrader
	logger   *slog.Logger

	mu      sync.RWMutex
	clients map[*client]struct{}
	closed  bool
}

// NewHub 构造广播中心。
func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		logger:  logger,
		clients: make(map[*client]struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 4096,
			// MVP 演示环境允许任意来源（局域网大屏、Vite Dev Server 均可直连）。
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Handler 返回 GET /ws/telemetry 的处理函数。
func (h *Hub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			h.logger.Warn("WebSocket 升级失败", "err", err)
			return
		}

		c := &client{hub: h, conn: conn, send: make(chan []byte, clientBuffer), done: make(chan struct{})}
		h.add(c)
		h.logger.Info("大屏已连接", "remote", r.RemoteAddr, "clients", h.ClientCount())

		go c.writePump()
		c.readPump()
	}
}

// BroadcastJSON 把任意结构体作为一条 JSON 消息广播给所有客户端。
func (h *Hub) BroadcastJSON(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		h.logger.Error("序列化遥测数据失败", "err", err)
		return
	}

	h.mu.RLock()
	targets := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.send <- payload:
		default:
			// 慢消费者：丢弃该连接，避免拖垮整个广播。
			h.drop(c)
		}
	}
}

// ClientCount 返回当前连接数。
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Close 关闭所有连接。
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = map[*client]struct{}{}
	h.mu.Unlock()

	for _, c := range clients {
		c.close()
	}
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		c.close()
		return
	}
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) drop(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.close()
}

// client 代表一个大屏连接。
type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	done chan struct{}

	once sync.Once
}

// readPump 只负责探测断连（前端不需要上行数据）。
func (c *client) readPump() {
	defer c.hub.drop(c)

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	}
}

// writePump 负责下发指标与心跳。
func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.close()
	}()

	for {
		select {
		case payload := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-c.done:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// close 幂等地关闭连接。
//
// 注意：这里刻意不 close(c.send)，否则并发 Broadcast 可能向已关闭通道发送而 panic。
// writePump 通过 done 通道得知退出信号。
func (c *client) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}
