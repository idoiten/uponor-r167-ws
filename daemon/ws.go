package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Minimal RFC 6455 WebSocket server (text frames, ping/pong, close).
// Written by hand so the daemon has no external dependencies.

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsConn struct {
	conn net.Conn
	rw   *bufio.ReadWriter
	mu   sync.Mutex
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func wsUpgrade(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("not a websocket request")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("hijacking not supported")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	h := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(h[:])
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n")
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return &wsConn{conn: conn, rw: rw, out: make(chan []byte, 64), done: make(chan struct{})}, nil
}

func (c *wsConn) writeFrame(op byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	hdr := []byte{0x80 | op}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 65536:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		hdr = append(hdr, b[:]...)
	}
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.rw.Write(hdr); err != nil {
		return err
	}
	if _, err := c.rw.Write(payload); err != nil {
		return err
	}
	return c.rw.Flush()
}

// readFrame returns opcode and (unmasked) payload of the next frame.
func (c *wsConn) readFrame() (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(c.rw, h[:]); err != nil {
		return 0, nil, err
	}
	op := h[0] & 0x0F
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(c.rw, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(c.rw, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > 1<<16 {
		return 0, nil, errors.New("frame too large")
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.rw, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(c.rw, p); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range p {
			p[i] ^= mask[i%4]
		}
	}
	return op, p, nil
}

func (c *wsConn) close() {
	c.once.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

// Hub fans messages out to all connected clients.
type Hub struct {
	mu      sync.Mutex
	clients map[*wsConn]bool
	log     func(string, ...any)
}

func NewHub(logf func(string, ...any)) *Hub {
	return &Hub{clients: map[*wsConn]bool{}, log: logf}
}

type message struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func encode(msgType string, data any) []byte {
	b, _ := json.Marshal(message{Type: msgType, Data: data})
	return b
}

// Publish marshals immediately (callers may hold locks on data).
func (h *Hub) Publish(msgType string, data any) {
	b := encode(msgType, data)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.out <- b:
		default:
			h.log("websocket client too slow, dropping it")
			c.close()
		}
	}
}

// Serve handles one websocket connection until it closes. snapshot is
// sent first; commands from the client go to onCommand.
func (h *Hub) Serve(c *wsConn, snapshot []byte, onCommand func(*wsConn, []byte)) {
	h.mu.Lock()
	h.clients[c] = true
	n := len(h.clients)
	h.mu.Unlock()
	h.log("websocket client connected from %s (%d connected)", c.conn.RemoteAddr(), n)
	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		c.close()
		h.log("websocket client %s disconnected", c.conn.RemoteAddr())
	}()

	// writer
	go func() {
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		if err := c.writeFrame(0x1, snapshot); err != nil {
			c.close()
			return
		}
		for {
			select {
			case b := <-c.out:
				if err := c.writeFrame(0x1, b); err != nil {
					c.close()
					return
				}
			case <-ping.C:
				if err := c.writeFrame(0x9, nil); err != nil {
					c.close()
					return
				}
			case <-c.done:
				return
			}
		}
	}()

	// reader
	for {
		c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		op, p, err := c.readFrame()
		if err != nil {
			return
		}
		switch op {
		case 0x1: // text
			onCommand(c, p)
		case 0x8: // close
			c.writeFrame(0x8, nil)
			return
		case 0x9: // ping
			c.writeFrame(0xA, p)
		}
	}
}
