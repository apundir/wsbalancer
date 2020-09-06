package internal_test

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apundir/wsbalancer/common"
	"github.com/gorilla/websocket"
)

// TestBackendConnections A lock protected map for safer concurrent access
type TestBackendConnections struct {
	connections map[*websocket.Conn]bool
	lock        *sync.RWMutex
}

func newTestBackendConnections() *TestBackendConnections {
	return &TestBackendConnections{
		connections: make(map[*websocket.Conn]bool),
		lock:        &sync.RWMutex{},
	}
}

// Add an item to map
func (tbc *TestBackendConnections) Add(con *websocket.Conn) {
	tbc.lock.Lock()
	defer tbc.lock.Unlock()
	tbc.connections[con] = true
}

// Delete an item from map
func (tbc *TestBackendConnections) Delete(con *websocket.Conn) {
	tbc.lock.Lock()
	defer tbc.lock.Unlock()
	delete(tbc.connections, con)
}

// Len size of the map
func (tbc *TestBackendConnections) Len() int {
	tbc.lock.RLock()
	defer tbc.lock.RUnlock()
	return len(tbc.connections)
}

// Contains determines if map contains given item
func (tbc *TestBackendConnections) Contains(con *websocket.Conn) bool {
	tbc.lock.RLock()
	defer tbc.lock.RUnlock()
	_, ok := tbc.connections[con]
	return ok
}

// List determines if map contains given item
func (tbc *TestBackendConnections) List() []*websocket.Conn {
	tbc.lock.RLock()
	defer tbc.lock.RUnlock()
	lst := make([]*websocket.Conn, len(tbc.connections))
	i := 0
	for con := range tbc.connections {
		lst[i] = con
		i++
	}
	return lst
}

// TestBackend a Websocket server for unit/integration testing
type TestBackend struct {
	// Underlying HTTP Server
	HTTPServer *http.Server

	// for upgrading to websockets
	Upgrader *websocket.Upgrader

	// total messages received
	received uint64

	// total messages sent
	sent uint64

	// headers to include while accepting connection
	ResponseHeader http.Header

	// true if the server is already listening
	Listening bool

	// all active connections from this server
	Connections *TestBackendConnections

	// function used to respond to incoming messages
	Responder func(*common.WsMessage) *common.WsMessage

	// function for accepting incoming requests
	CustomAcceptor func(w http.ResponseWriter, r *http.Request)

	// If false, all lastXXXX variables are NOT updated
	IgnoreLastTrackers bool

	// Last message received by this server
	lastReceived *common.WsMessage
	lrLock       *sync.RWMutex

	// Last message sent by this server.
	lastSent *common.WsMessage
	lsLock   *sync.RWMutex

	// Last ping received at
	lastPingAt time.Time
	lpLock     *sync.RWMutex

	// Last connection's headers, uses lcLock
	lastHeader http.Header

	// Last connection made by this server
	lastConnection  *websocket.Conn
	lastConnectedAt time.Time
	lcLock          *sync.RWMutex
}

// newTestBackend create a new instance of server as per given config
func newTestBackend(cfg *TestBackendConfig) *TestBackend {
	mux := http.NewServeMux()
	hs := &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
	}
	upg := &websocket.Upgrader{
		ReadBufferSize:  cfg.BufferSize,
		WriteBufferSize: cfg.BufferSize,
	}
	if len(cfg.Subprotocol) > 0 {
		upg.Subprotocols = cfg.Subprotocol
	}
	tbs := &TestBackend{
		HTTPServer:         hs,
		Upgrader:           upg,
		Connections:        newTestBackendConnections(),
		IgnoreLastTrackers: cfg.IgnoreLastTrackers,
		lrLock:             &sync.RWMutex{},
		lpLock:             &sync.RWMutex{},
		lsLock:             &sync.RWMutex{},
		lcLock:             &sync.RWMutex{},
		lastPingAt:         time.Time{},
		lastConnectedAt:    time.Time{},
	}
	mux.HandleFunc("/", tbs.ServeWS)
	tbs.Responder = tbs.EchoResponder
	return tbs
}

// LastHeader returns the incoming headers of the latest accepted
// client connection by this server
func (ts *TestBackend) LastHeader() http.Header {
	ts.lcLock.RLock()
	defer ts.lcLock.RUnlock()
	return ts.lastHeader
}

// LastConnection returns the latest client connection accepted by this server
func (ts *TestBackend) LastConnection() *websocket.Conn {
	ts.lcLock.RLock()
	defer ts.lcLock.RUnlock()
	return ts.lastConnection
}

// LastConnectedAt returns the time at which latest client connection
// was accepted by this server
func (ts *TestBackend) LastConnectedAt() time.Time {
	ts.lcLock.RLock()
	defer ts.lcLock.RUnlock()
	return ts.lastConnectedAt
}

// LastPingAt returns the time at which latest ping was received
// by this server across any of it's connection
func (ts *TestBackend) LastPingAt() time.Time {
	ts.lpLock.RLock()
	defer ts.lpLock.RUnlock()
	return ts.lastPingAt
}

// LastSent returns the last message sent by this
// server across any of it's connection
func (ts *TestBackend) LastSent() *common.WsMessage {
	ts.lsLock.RLock()
	defer ts.lsLock.RUnlock()
	return ts.lastSent
}

// LastReceived returns the last message received by this
// server on any of it's connection
func (ts *TestBackend) LastReceived() *common.WsMessage {
	ts.lrLock.RLock()
	defer ts.lrLock.RUnlock()
	return ts.lastReceived
}

// Start - starts listening for connections
func (ts *TestBackend) Start() {
	go func() {
		defer func() { ts.Listening = false }()
		ts.Listening = true
		ts.HTTPServer.ListenAndServe()
	}()
}

// ResetStats Reset all internal stat counters to zero or nil values
func (ts *TestBackend) ResetStats() {
	atomic.StoreUint64(&ts.received, 0)
	atomic.StoreUint64(&ts.sent, 0)
	if ts.IgnoreLastTrackers {
		return
	}
	ts.lsLock.Lock()
	ts.lastSent = nil
	ts.lsLock.Unlock()
	ts.lrLock.Lock()
	ts.lastReceived = nil
	ts.lrLock.Unlock()
	ts.lpLock.Lock()
	ts.lastPingAt = time.Time{}
	ts.lpLock.Unlock()
	ts.lcLock.Lock()
	ts.lastConnection = nil
	ts.lastHeader = nil
	ts.lastConnectedAt = time.Time{}
	ts.lcLock.Unlock()
}

// ConnectionCount returns connection count estbalished with this server right now
func (ts *TestBackend) ConnectionCount() int {
	return ts.Connections.Len()
}

// TerminateAllConnections closes all open connections without sending
// any close message
func (ts *TestBackend) TerminateAllConnections() {
	ts.CloseAllConnectionsWithCode(-1)
}

// CloseAllConnections closes all open connections after sending
// NormalClose close message
func (ts *TestBackend) CloseAllConnections() {
	ts.CloseAllConnectionsWithCode(websocket.CloseNormalClosure)
}

// CloseAllConnectionsWithCode closes all open connections
// after sending close message with given closeCode
func (ts *TestBackend) CloseAllConnectionsWithCode(closeCode int) {
	for _, ac := range ts.Connections.List() {
		if closeCode >= 0 {
			ac.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(closeCode, ""),
				time.Now().Add(time.Second))
		}
		ac.Close()
	}
}

// ServeWS acts as a HTTP handler for incoming requests
func (ts *TestBackend) ServeWS(w http.ResponseWriter, r *http.Request) {
	if ts.CustomAcceptor != nil {
		ts.CustomAcceptor(w, r)
		return
	}
	c, err := ts.Upgrader.Upgrade(w, r, ts.ResponseHeader)
	if err != nil {
		return // connection error
	}
	if !ts.IgnoreLastTrackers {
		ts.lcLock.Lock()
		ts.lastHeader = r.Header
		ts.lastConnection = c
		ts.lastConnectedAt = time.Now()
		ts.lcLock.Unlock()
	}
	ts.Connections.Add(c)
	ts.setPingHandler(c)
	go ts.readMessages(c)
}

func (ts *TestBackend) setPingHandler(con *websocket.Conn) {
	con.SetPingHandler(func(appData string) error {
		con.WriteMessage(websocket.PongMessage, []byte(appData))
		if !ts.IgnoreLastTrackers {
			ts.lpLock.Lock()
			ts.lastPingAt = time.Now()
			ts.lpLock.Unlock()
		}
		return nil
	})
}

func (ts *TestBackend) readMessages(con *websocket.Conn) {
	defer func() {
		ts.Connections.Delete(con)
		con.Close()
	}()
	for {
		mt, message, err := con.ReadMessage()
		if err != nil {
			return
		}
		atomic.AddUint64(&ts.received, 1)
		msg := &common.WsMessage{MessageType: mt, Data: message}
		if !ts.IgnoreLastTrackers {
			ts.lrLock.Lock()
			ts.lastReceived = msg
			ts.lrLock.Unlock()
		}
		resp := ts.Responder(msg)
		if resp != nil {
			err = con.WriteMessage(resp.MessageType, resp.Data)
			if err != nil {
				return
			}
			atomic.AddUint64(&ts.sent, 1)
			if !ts.IgnoreLastTrackers {
				ts.lsLock.Lock()
				ts.lastSent = resp
				ts.lsLock.Unlock()
			}
		}
	}
}

// EchoResponder Responds with same message as being received
func (ts *TestBackend) EchoResponder(msg *common.WsMessage) *common.WsMessage {
	return msg
}

// NilResponder returns nil, essentially resulting in no response
func (ts *TestBackend) NilResponder(msg *common.WsMessage) *common.WsMessage {
	return nil
}
