package mining

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// dialTimeout bounds how long we wait to connect to the pool.
const dialTimeout = 15 * time.Second

// WebSocketHandler returns an http.Handler that bridges a browser miner to the
// configured Monero pool. Each connection is keyed by the "id" query parameter,
// which must be the challenge id the visitor is solving.
func (m *Miner) WebSocketHandler(logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return websocket.Server{
		Handler: func(ws *websocket.Conn) {
			m.serve(ws, logger)
		},
		// Accept any origin: the browser miner is loaded from Anubis' own
		// challenge page and the connection carries no ambient authority.
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
	}
}

// Browser <-> Anubis protocol.
type clientMessage struct {
	Type   string `json:"type"`
	JobID  string `json:"job_id,omitempty"`
	Nonce  string `json:"nonce,omitempty"`
	Result string `json:"result,omitempty"`
}

type serverMessage struct {
	Type     string          `json:"type"`
	Job      json.RawMessage `json:"job,omitempty"`
	Shares   int             `json:"shares,omitempty"`
	Required int             `json:"required,omitempty"`
	Message  string          `json:"message,omitempty"`
}

// Anubis <-> pool stratum messages (newline-delimited JSON-RPC).
type poolMessage struct {
	ID     *int            `json:"id"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type loginResult struct {
	ID     string          `json:"id"`
	Job    json.RawMessage `json:"job"`
	Status string          `json:"status"`
}

type statusResult struct {
	Status string `json:"status"`
}

type session struct {
	m      *Miner
	id     string
	ws     *websocket.Conn
	pool   net.Conn
	logger *slog.Logger

	writeMu sync.Mutex // guards writes to pool

	mu        sync.Mutex // guards the fields below
	sessionID string     // pool-assigned login id
	rpcID     int
	pending   map[int]bool // rpc ids of in-flight submit calls
}

func (m *Miner) serve(ws *websocket.Conn, logger *slog.Logger) {
	defer ws.Close()

	id := ws.Request().URL.Query().Get("id")
	if id == "" {
		_ = websocket.JSON.Send(ws, serverMessage{Type: "error", Message: "missing challenge id"})
		return
	}

	lg := logger.With("subsystem", "mining", "challenge", id)

	dialer := &net.Dialer{Timeout: dialTimeout}
	var pool net.Conn
	var err error
	if m.cfg.PoolUsesTLS {
		pool, err = tls.DialWithDialer(dialer, "tcp", m.cfg.Pool, nil)
	} else {
		pool, err = dialer.Dial("tcp", m.cfg.Pool)
	}
	if err != nil {
		lg.Error("can't dial mining pool", "pool", m.cfg.Pool, "err", err)
		_ = websocket.JSON.Send(ws, serverMessage{Type: "error", Message: "pool unavailable"})
		return
	}
	defer pool.Close()

	s := &session{
		m:       m,
		id:      id,
		ws:      ws,
		pool:    pool,
		logger:  lg,
		pending: map[int]bool{},
	}

	if err := s.login(); err != nil {
		lg.Error("pool login failed", "err", err)
		_ = websocket.JSON.Send(ws, serverMessage{Type: "error", Message: "pool login failed"})
		return
	}

	// Pump pool -> browser in the background; the foreground loop pumps
	// browser -> pool. When either side closes, the connections are torn down
	// by the defers above, unblocking the other pump.
	go s.poolToBrowser()
	s.browserToPool()
}

func (s *session) writePool(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := s.pool.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	_, err = s.pool.Write(data)
	return err
}

func (s *session) login() error {
	s.mu.Lock()
	s.rpcID++
	id := s.rpcID
	s.mu.Unlock()

	return s.writePool(map[string]any{
		"id":      id,
		"jsonrpc": "2.0",
		"method":  "login",
		"params": map[string]any{
			"login": s.m.cfg.WalletAddress,
			"pass":  s.m.cfg.Password,
			"rigid": s.m.cfg.RigID,
			"agent": "anubis-miner/1.0",
			"algo":  []string{"rx/0"},
		},
	})
}

// browserToPool reads submit requests from the browser and relays them to the
// pool. It runs in the foreground until the browser disconnects.
func (s *session) browserToPool() {
	for {
		var msg clientMessage
		if err := websocket.JSON.Receive(s.ws, &msg); err != nil {
			return // browser closed
		}

		if msg.Type != "submit" {
			continue
		}

		s.mu.Lock()
		sessionID := s.sessionID
		s.rpcID++
		rpcID := s.rpcID
		s.pending[rpcID] = true
		s.mu.Unlock()

		if sessionID == "" {
			continue // not logged in yet
		}

		if err := s.writePool(map[string]any{
			"id":      rpcID,
			"jsonrpc": "2.0",
			"method":  "submit",
			"params": map[string]any{
				"id":     sessionID,
				"job_id": msg.JobID,
				"nonce":  msg.Nonce,
				"result": msg.Result,
			},
		}); err != nil {
			s.logger.Debug("can't forward share to pool", "err", err)
			return
		}
	}
}

// poolToBrowser reads stratum messages from the pool and relays jobs and share
// acknowledgements to the browser. It runs until the pool disconnects.
func (s *session) poolToBrowser() {
	defer s.ws.Close()

	reader := bufio.NewReaderSize(s.pool, 64*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		if len(line) == 0 {
			continue
		}

		var msg poolMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			s.logger.Debug("can't decode pool message", "err", err)
			continue
		}

		switch {
		case msg.Method == "job":
			// Pool pushed a new job.
			s.sendJob(msg.Params)

		case msg.ID != nil:
			s.handleResult(*msg.ID, msg.Result, msg.Error)
		}
	}
}

// handleResult processes a response to an rpc call we made (login or submit).
func (s *session) handleResult(id int, result, rpcErr json.RawMessage) {
	s.mu.Lock()
	wasSubmit := s.pending[id]
	delete(s.pending, id)
	loggedIn := s.sessionID != ""
	s.mu.Unlock()

	if !loggedIn {
		// First response with a result is the login reply.
		var lr loginResult
		if err := json.Unmarshal(result, &lr); err == nil && lr.ID != "" {
			s.mu.Lock()
			s.sessionID = lr.ID
			s.mu.Unlock()
			if len(lr.Job) > 0 {
				s.sendJob(lr.Job)
			}
			return
		}
	}

	if !wasSubmit {
		return
	}

	if len(rpcErr) > 0 && string(rpcErr) != "null" {
		s.logger.Debug("pool rejected share", "err", string(rpcErr))
		s.sendBrowser(serverMessage{Type: "rejected", Message: string(rpcErr)})
		return
	}

	var sr statusResult
	if err := json.Unmarshal(result, &sr); err != nil || sr.Status != "OK" {
		s.sendBrowser(serverMessage{Type: "rejected"})
		return
	}

	// The pool accepted the share. This is the only place progress is counted.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	shares, err := s.m.IncrementShares(ctx, s.id)
	cancel()
	if err != nil {
		s.logger.Error("can't record accepted share", "err", err)
	}

	required := s.m.RequiredShares()
	s.sendBrowser(serverMessage{Type: "accepted", Shares: shares, Required: required})

	if shares >= required {
		s.sendBrowser(serverMessage{Type: "done", Shares: shares, Required: required})
	}
}

func (s *session) sendJob(job json.RawMessage) {
	if len(job) == 0 {
		return
	}
	s.sendBrowser(serverMessage{Type: "job", Job: job})
}

func (s *session) sendBrowser(msg serverMessage) {
	if err := websocket.JSON.Send(s.ws, msg); err != nil {
		s.logger.Debug("can't send to browser", "err", err)
	}
}

// WSRoute is the API path (relative to the Anubis API prefix) the browser miner
// connects to.
func WSRoute(apiPrefix string) string {
	return fmt.Sprintf("%smining/ws", apiPrefix)
}
