package in

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/apundir/wsbalancer/common"
	slog "github.com/go-eden/slf4go"
	"github.com/gorilla/websocket"
)

const (
	fePipeBufferSize = 10
	bePipeBufferSize = 10
)

var (
	alog          = slog.NewLogger("in.adm")
	mlog          = slog.NewLogger("in.mgr")
	ilog          = slog.NewLogger("in.ic")
	hostPortRe, _ = regexp.Compile("://((.+?)(:\\d+)?)(/|$)")
	nonAlphaRe, _ = regexp.Compile("[^0-9a-zA-Z]")
	beIDRe, _     = regexp.Compile("(backend|frontend|session)/(.+?)/(.+)")
)

// Manager is a pool of incoming frontend client connections along with mapped
// outgoing backend connections. It's responsible for housekeeping of all proxy
// sessions in the system.
type Manager struct {
	// Connected frontend clients along with their backend connection.
	clients map[*inc]common.BackendConnection

	// for protecting concurrent access to clients map
	ctsLock *sync.RWMutex

	// for new session registration requests
	register chan *common.ProxySession

	// for un-registering terminated session against provided client.
	unregister chan *inc

	// The upgrader used for websockets
	upgrader *websocket.Upgrader

	// Dialer to use for backend connections
	backendDialer common.BackendDialer

	// external valve providers
	valveProvider []common.ValveProvider

	// Session lifecycle listeners
	sessionListeners []common.SessionListener

	// configuration of the frontend server
	config *ServerConfig

	// admin controller for serving admin requests
	admin *admin

	// true if this manager is NOT accepting NEW connection, false otherwise
	paused bool

	// general purpose identifier for this manager
	id string

	// to gracefully close down manager itself
	done chan struct{}
}

// NewManager creates a new initialized Manager with given configuration and
// given backend dialer. The manager is only initialized and it's registration
// and de-registration loops are NOT started automatically yet. The caller MUST
// call Start() method on the newly created Manager instance before using the
// Manager for any balancer purposes.
func NewManager(config *ServerConfig, dialer common.BackendDialer) *Manager {
	ug := &websocket.Upgrader{
		ReadBufferSize:  config.ReadBufferSize,
		WriteBufferSize: config.WriteBufferSize,
		// a blank slice ensure balancer is neutral about the protocol selection
		// by itself and always carry forward protocol selection (or no
		// selection) as per backend connection instead of deciding by it's own
		Subprotocols: []string{},
	}
	mgr := &Manager{
		clients:          make(map[*inc]common.BackendConnection),
		register:         make(chan *common.ProxySession, 100),
		unregister:       make(chan *inc, 100),
		upgrader:         ug,
		backendDialer:    dialer,
		valveProvider:    make([]common.ValveProvider, 0, 5),
		sessionListeners: make([]common.SessionListener, 0, 5),
		ctsLock:          &sync.RWMutex{},
		config:           config,
		paused:           false,
		id:               NormalizeForID(config.FrontendHTTP.Addr),
		done:             make(chan struct{}),
	}
	mgr.admin = &admin{
		manager: mgr,
	}
	if bkAdmin, ok := mgr.backendDialer.(common.BackendAdministrator); ok {
		mgr.admin.backendAdmin = bkAdmin
	}
	return mgr
}

// AddValveProvider adds new ValveProvider at end of the chain. Each session
// will receive it's valves by calling AddValves method of all of the available
// ValveProviders with the Manager.
func (mgr *Manager) AddValveProvider(vp common.ValveProvider) {
	mgr.valveProvider = append(mgr.valveProvider, vp)
}

// AddSessionListener adds a new SessionListener to list of existing
// sessionsListeners of this manager.
func (mgr *Manager) AddSessionListener(sessionListener common.SessionListener) {
	mgr.sessionListeners = append(mgr.sessionListeners, sessionListener)
}

// ServeWs is the main handler function for incoming websocket requests. This can be used in any
func (mgr *Manager) ServeWs(w http.ResponseWriter, r *http.Request) {
	if mgr.paused {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable),
			http.StatusServiceUnavailable)
		return
	}
	if !mgr.isWebsocketRequest(r) {
		http.Error(w, http.StatusText(http.StatusBadRequest),
			http.StatusBadRequest)
		return
	}
	// channel for sending data to backend
	send := make(chan *common.WsMessage, fePipeBufferSize)
	// channel for receiving data from backend
	receive := make(chan *common.WsMessage, bePipeBufferSize)
	upstreamHeaders := mgr.getUpstreamHeaders(r)
	beCon, err := mgr.backendConnection(*r.URL, upstreamHeaders,
		send,    // frontend send channel will be receiver for backend
		receive, // frontend receive channel will be send for backend
		nil,     // no existing connection
	)
	if err != nil {
		mgr.sendError(w, err)
		return
	}
	conn, err := mgr.upgraderForSubprotocol(
		beCon.Subprotocol()).Upgrade(w, r, beCon.AdditionalHeaders())
	if conn == nil || err != nil {
		mlog.Error(err)
		beCon.Close()
		return
	}
	feCon := newInc(conn, upstreamHeaders, mgr.id, r.URL, mgr.unregister)
	feCon.send = send
	feCon.receive = receive
	mgr.addValves(feCon, beCon)
	mgr.register <- &common.ProxySession{FEConnection: feCon, BEConnection: beCon}
	feCon.Start()
	beCon.Start()
}

func (mgr *Manager) isWebsocketRequest(r *http.Request) bool {
	// validate that incoming request meets ALL the websocket criteria
	if r.Method != "GET" {
		return false
	}
	if strings.ToLower(r.Header.Get("Connection")) != "upgrade" {
		return false
	}
	if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
		return false
	}
	return true
}

func (mgr *Manager) upgraderForSubprotocol(proto string) *websocket.Upgrader {
	if proto == "" {
		return mgr.upgrader
	}
	origUpgrader := *mgr.upgrader
	newUpgrader := origUpgrader
	newUpgrader.Subprotocols = []string{proto}
	return &newUpgrader
}

func (mgr *Manager) sendError(w http.ResponseWriter, err error) {
	mlog.Error("Aborting connection, B/E error: ", err)
	if decErr, decErrOk := err.(*common.ConnectionDeclinedError); decErrOk {
		http.Error(w, http.StatusText(decErr.Code()), decErr.Code())
		return
	}
	http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
}

func (mgr *Manager) addValves(client *inc, usCon common.BackendConnection) {
	if len(mgr.valveProvider) < 1 {
		return
	}
	session := &common.ProxySession{FEConnection: client, BEConnection: usCon}
	msgValves := make([]common.MessageValve, 0, len(mgr.valveProvider))
	for _, vp := range mgr.valveProvider {
		msgValves = vp.AddValves(session, msgValves)
	}
	client.messageValves = msgValves
}

func (mgr *Manager) getUpstreamHeaders(r *http.Request) *http.Header {
	upstreamHeaders := r.Header.Clone()
	upstreamHeaders.Set(common.ForwardedForHeader, mgr.getForwardedFor(r))
	upstreamHeaders.Del("Connection")
	upstreamHeaders.Del("Upgrade")
	upstreamHeaders.Del("Sec-Websocket-Key")
	upstreamHeaders.Del("Sec-Websocket-Version")
	if mgr.config.IgnoreHeaders != nil {
		for _, hdr := range mgr.config.IgnoreHeaders {
			upstreamHeaders.Del(hdr)
		}
	}
	// make sure to NOT pass any existing ReconnectHeader. This can potentially
	// be exploited as a security loop hole where a malicious user can fool
	// backend systems to presume that the incoming connection is a reconnect
	// from proxy and NOT a fresh client connection.
	if mgr.config.ReconnectHeader != "" {
		upstreamHeaders.Del(mgr.config.ReconnectHeader)
	}
	return &upstreamHeaders
}

func (mgr *Manager) backendConnection(URL url.URL,
	headers *http.Header,
	rcv <-chan *common.WsMessage,
	snd chan<- *common.WsMessage,
	oldConnection common.BackendConnection) (common.BackendConnection, error) {
	return mgr.backendDialer.DialUpstream(&common.BackendDialContext{
		URL:           URL,
		DestHeaders:   headers,
		Receive:       rcv,
		Send:          snd,
		OldConnection: oldConnection,
	})
}

func (mgr *Manager) getForwardedFor(r *http.Request) string {
	if !mgr.config.TrustReceivedXff {
		return mgr.getRemoteIP(r)
	}
	forwarded := r.Header.Get(common.ForwardedForHeader)
	if forwarded != "" {
		return forwarded + ", " + mgr.getRemoteIP(r)
	}
	return mgr.getRemoteIP(r)
}

func (mgr *Manager) getRemoteIP(r *http.Request) string {
	ra := r.RemoteAddr
	cIdx := strings.LastIndex(ra, ":")
	if cIdx < 0 {
		return ra
	}
	return ra[0:cIdx]
}

// Start the registration and de-registration loop in separate goroutine
func (mgr *Manager) Start() {
	go func() {
		for {
			select {
			case session := <-mgr.register:
				mgr.addClient(session.FEConnection.(*inc), session.BEConnection)
			case client := <-mgr.unregister:
				go mgr.handleUnregistration(client)
			case <-mgr.done:
				return
			}
		}
	}()
}

// Stop the Manager and associated sessions by (a) closing all active sessions
// and (b) terminating the registration and de-registration routine
func (mgr *Manager) Stop() {
	mgr.ctsLock.RLock()
	allSessions := make([]*inc, 0, len(mgr.clients))
	for ss := range mgr.clients {
		allSessions = append(allSessions, ss)
	}
	mgr.ctsLock.RUnlock()
	for _, as := range allSessions {
		as.Close()
	}
	mgr.done <- struct{}{}
}

func (mgr *Manager) handleUnregistration(client *inc) {
	if !mgr.clientExists(client) {
		return
	}
	if !client.isClientActive() {
		client.Close()
		mgr.removeClient(client)
		return
	}
	// client is still active, initiate another upstream
	// connection and resume proxy operation
	currentBkConn := mgr.getBKConnection(client)
	client.header.Set(mgr.config.ReconnectHeader, "1")
	usCon, err := mgr.backendConnection(*client.url, client.header,
		client.send,    // incoming send channel will be receiver for upstream
		client.receive, // incoming receive channel will be send for upstream
		currentBkConn,
	)
	if err != nil {
		mlog.Error("Unable to resume, error connecting to backend: ", err)
		client.Close()
		mgr.removeClient(client)
		return
	}
	mgr.addClient(client, usCon)
	client.resume()
	usCon.Start()
}

// extracted for locked access
func (mgr *Manager) clientExists(client *inc) bool {
	mgr.ctsLock.RLock()
	_, ok := mgr.clients[client]
	mgr.ctsLock.RUnlock()
	return ok
}

// concurrency safe get method
func (mgr *Manager) getBKConnection(client *inc) common.BackendConnection {
	mgr.ctsLock.RLock()
	defer mgr.ctsLock.RUnlock()
	return mgr.clients[client]
}

func (mgr *Manager) removeClient(client *inc) {
	mgr.ctsLock.Lock()
	defer mgr.ctsLock.Unlock()
	us, ok := mgr.clients[client]
	if !ok {
		return // already deleted
	}
	for _, lstnr := range mgr.sessionListeners {
		lstnr.SessionTerminated(&common.ProxySession{FEConnection: client, BEConnection: us})
	}
	delete(mgr.clients, client)
}

func (mgr *Manager) addClient(client *inc, usCon common.BackendConnection) {
	mgr.ctsLock.Lock()
	defer mgr.ctsLock.Unlock()
	_, exists := mgr.clients[client]
	for _, lstnr := range mgr.sessionListeners {
		if exists {
			lstnr.SessionTerminated(&common.ProxySession{FEConnection: client, BEConnection: mgr.clients[client]})
		}
		lstnr.SessionCreated(&common.ProxySession{FEConnection: client, BEConnection: usCon})
	}
	mgr.clients[client] = usCon
}

// abortAllBackendConnections aborts ALL connection from given backend, this
// will initiating failover to other backend.
//
// During many of following operations we don't want to hold Read lock on
// clients map for long since the connections will be closed and updated in the
// clients map in concurrent goroutines. For this reason, we are enumerating all
// matching backend connections in separate slice and then closing these
// connections *after* releasing the Read Lock on client
func (mgr *Manager) abortAllBackendConnections(backendID string) (int, error) {
	// lock, copy and release lock first before further operations
	mgr.ctsLock.RLock()
	matchingSessions := make([]*inc, 0, len(mgr.clients))
	for fe, be := range mgr.clients {
		if be.BackendID() == backendID {
			matchingSessions = append(matchingSessions, fe)
		}
	}
	// release Read lock before actually closing the backend connection
	mgr.ctsLock.RUnlock()
	mlog.Infof("aborting %v backend connections for %v", len(matchingSessions), backendID)
	for _, fe := range matchingSessions {
		fe.abortBackend()
	}
	if len(matchingSessions) > 0 {
		return common.ResultSuccess, nil
	}
	return common.ResultNoActionReqd, nil
}

// abortFrontendConnection close incoming connections with given id
func (mgr *Manager) abortFrontendConnection(sessionID string) (int, error) {
	// lock, copy and release lock first before further operations
	mgr.ctsLock.RLock()
	var matchingSession *inc = nil
	for fe := range mgr.clients {
		if fe.GetID() == sessionID {
			matchingSession = fe
		}
	}
	// release Read lock before actually closing the connection
	mgr.ctsLock.RUnlock()
	if matchingSession == nil {
		return common.ResultNoActionReqd, nil
	}
	matchingSession.Close()
	return common.ResultSuccess, nil
}

// abortBackendConnection closes backend connection for given session thus
// initiating a failover to another backend as per general availability
// rules of available backends.
func (mgr *Manager) abortBackendConnection(sessionID string) (int, error) {
	// lock, copy and release lock first before further operations
	mgr.ctsLock.RLock()
	var matchingSession *inc = nil
	for fe := range mgr.clients {
		if fe.GetID() == sessionID {
			matchingSession = fe
		}
	}
	// release Read lock before actually closing the connection
	mgr.ctsLock.RUnlock()
	if matchingSession == nil {
		return common.ResultFailed, nil
	}
	matchingSession.abortBackend()
	return common.ResultSuccess, nil
}

// RegisterHTTPHandlers registers actionable endpoints to supplied mux
func (mgr *Manager) RegisterHTTPHandlers(http *http.ServeMux) {
	mgr.admin.registerHTTPHandlers(http)
}

func (mgr *Manager) pauseFrontend(fid string) (int, error) {
	if mgr.id != fid {
		return common.ResultFailed, nil
	}
	if mgr.paused {
		return common.ResultNoActionReqd, nil
	}
	mgr.paused = true
	mlog.Info("Frontend paused, no new connection will be accepted")
	return common.ResultSuccess, nil
}

func (mgr *Manager) resumeFrontend(fid string) (int, error) {
	if mgr.id != fid {
		return common.ResultFailed, nil
	}
	if !mgr.paused {
		return common.ResultNoActionReqd, nil
	}
	mgr.paused = false
	mlog.Info("Frontend resumed, new connections will be accepted normally")
	return common.ResultSuccess, nil
}
