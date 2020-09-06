package common

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/websocket"
)

// Starter - can start reading and writing loops
type Starter interface {
	Start()
}

// StartCloser - can be started as well as closed for reading/writing
type StartCloser interface {
	Starter
	io.Closer
}

// Identifier - provides a unique id for the object that can be used
// for general purpose admin management operations, reporting and logging
type Identifier interface {
	GetID() string
}

// StartCloseIdentifier - combination of Starter, Closer and Identifier
type StartCloseIdentifier interface {
	StartCloser
	Identifier
}

// FrontendConnection is the client connection obtained from Frontend server
// against an incoming request.
type FrontendConnection interface {
	StartCloseIdentifier
	FrontendID() string
}

// BackendConnection is the client connection created from one of the backend
type BackendConnection interface {
	StartCloseIdentifier
	BackendID() string
	Subprotocol() string
	AdditionalHeaders() http.Header
}

// BackendDialContext - an encapsulated context type which contains all the
// information using which a backend connection shall be made to one of the
// available backend. This encapsulation is expected to grow as more and more
// features are added to the balancer.
type BackendDialContext struct {
	// URL onto which the incoming websocket connection was made on frontend
	URL url.URL

	// All headers received by frontend excluding ignored headers
	DestHeaders *http.Header

	// Channel which will be populated with incoming frontend websocket messages.
	// These messages shall be processed and sent to the backend by backend connection.
	// In addition to data messages, this channel will also contain control messages
	// to communicate frontend connection state to backend OR asking backend to
	// shut itself down should there be an administrative request.
	Receive <-chan *WsMessage

	// Channel onto which the backend shall write messages which it intends to send
	// to the frontend connection over a proxy session. In addition to the data
	// messages, this channel will also be used to transmit control messages to
	// communicate backend connection state to frontend.
	Send chan<- *WsMessage

	// If a connection request to backend is being made against an existing connection which
	// needs re-establishment (say due to existing backend connection gone bad) then
	// existing connection object will be sent across in the context. This object and it's
	// internal state is totally opaque to frontend and it assumes that backend will use it
	// if it needs to for any purpose. In current implementation, the existing old connection
	// is used to fetch any unsent messages which MUST be sent first on the newly established
	// backend connection.
	OldConnection BackendConnection
}

// BackendDialer is used for requesting a fresh connection to any of the
// available backend servers. It is up-to the dialer to decide which backend
// server to use for connection. Once the connection is established and
// returned, the BackendDialer no longer control the connection instead it is
// completely governed by the frontend during it's entire life cycle.
type BackendDialer interface {
	// Creates a new BackendConnection against given context and returns
	// the connection if successful.
	//
	// If, due to any reason, the Dialer is not able to establish a connection
	// than the returned error indicates what the error was, frontned will
	// decide appropriately what response to send to remote client based
	// on the error returned. Frontend decision is much more deterministic
	// if the error is an instance of RemoteSystemError or it's subtype.
	DialUpstream(dialContext *BackendDialContext) (BackendConnection, error)
}

// BackendAdministrator provides administrative capabilities over available
// backends. As such the entire implementation of this interface is OPTIONAL. If
// provided, the implementation will be used to expose administrative API
// endpoints for managing available backends.
//
// The implementation of this interface should ONLY affect backends state and
// MUST NOT administer any existing established backend connections which has
// been handed over to frontend already. All established backend connections,
// once handed over to frontend, are administrated by frontend only. As an
// example - calling DeleteBackend should delete the backend thus system will no
// longer have knowledge of this backend and this backend will NOT participate
// in any new connection request. However, any existing established connections
// to this backend MUST NOT be terminated by the implementation. These
// connections will be appropriately terminated by frontend on delete request
// successful completion.
type BackendAdministrator interface {
	BackendDialer

	// Prepares a list of backends and return as string. Returned string is
	// completely opaque to frontend and this is sent AS-IS over the
	// administrative API interface. The only assumption made by this interface
	// is that this data is JSON. Implementations are free to choose any
	// structure of this JSON as they seem appropriate. For best usage, it is
	// expected that the returned data at least contains the id of the backend
	// that can be further utilized in subsequent administrative calls. This is
	// further advised that the list also contains circuit status of the backend
	// if it supports one. This way it'll be possible for admin API consumers to
	// know if a specific backend is tripped due to which no new connection is
	// being sent to the backend.
	ListBackends() string

	// Add a new backend to the system. The data is completely opaque to
	// frontend and the data received over administrative endpoint will be
	// passed on AS-IS to this method. Decoding/Un-Marshalling this data
	// appropriately and bulding backend definition is the responsibility of the
	// implementation. The result, success or failure, shall be returned as a
	// JSON message by the implementation in any structure as they find
	// appropriate.
	AddBackend(data []byte) string

	// Returns the current configuration of backend with given id. Apart from
	// assuming that returned data is JSON, frontend do not expect any structure
	// of the JSON and pass it AS-IS to caller over the administrative API
	// interface.
	BackendConfig(id string) string

	// Disables the backend with given id. A disabled backend is retained in the
	// system but do not participate in any new connection attempt. All existing
	// connections to a disabled backend shall continue to work AS-IS without
	// any trouble. A disabled backend can be enabled again. This is typically
	// required while debugging specific issues with a backend OR while
	// switching from one backend to new backend. The returned status must be
	// one of the ResultXXX constant as defined in this package.
	DisableBackend(id string) (int, error)

	// Enables the backend with the given id. A disabled backend can be enabled
	// so that it starts participating in new connection requests normally. The
	// returned status must be one of the ResultXXX constant as defined in this
	// package.
	EnableBackend(id string) (int, error)

	// Deletes the backend with given id. A deleted backend practically do not
	// exists in the system any longer. The implementation must NOT terminate
	// any existing connection during the delete operation. Frontend will take
	// care of terminating respective connections once this API returns
	// successful response. The returned status must be one of the ResultXXX
	// constant as defined in this package.
	DeleteBackend(id string) (int, error)

	// Resets a tripped backend. A backend goes into tripped state the moment
	// consecutive number of failures go beyond configured threshold.The
	// returned status must be one of the ResultXXX constant as defined in this
	// package.
	ResetBackend(id string) (int, error)
}

// ProxySession is an established live session with an incoming frontend
// connection and respective backend connection.
type ProxySession struct {
	FEConnection FrontendConnection
	BEConnection BackendConnection
}

// MessageValve - a valve acts as a filter through which the message is passed
// before it is sent to backend server and after it's received from backend
// server. A valve can potentially modify a message and return modified message.
// If the return message is nil OR if the error is not nil then the message is
// discarded and no message is sent to the destination.
type MessageValve interface {
	BeforeTransmit(*WsMessage) (*WsMessage, error)
	AfterReceive(*WsMessage) (*WsMessage, error)
}

// ValveProvider - An interface that can be used to add 0 or more
// MessageValve for every new session.
type ValveProvider interface {
	AddValves(session *ProxySession, currentValves []MessageValve) []MessageValve
}

// RemoteSystemError An error thrown by balancer indicating remote backend system
// is not able to accept a new connection
type RemoteSystemError struct {
	id   string // unique id of the remote system
	code int    // HTTP status code, if applicable
	err  string // error message of the system
}

// GetID returns remote system unique identifier
func (rsu *RemoteSystemError) GetID() string {
	return rsu.id
}

// Code returns the error code against this error
func (rsu *RemoteSystemError) Code() int {
	return rsu.code
}

// Error - format the error for reporting and logging
func (rsu *RemoteSystemError) Error() string {
	if rsu.code > 0 {
		return fmt.Sprintf("%s reported status code %d with %s", rsu.id, rsu.code, rsu.err)
	}
	return fmt.Sprintf("%s reported %s", rsu.id, rsu.err)
}

// SystemUnavailableError indicate backend system unavailability. This could happen due
// to network error OR due to 5xx HTTP error during connection. balancer system will
// attempt next available backend in this case.
type SystemUnavailableError struct {
	RemoteSystemError
}

// NewSystemUnavailableError create new error without code
func NewSystemUnavailableError(id string, err string) *SystemUnavailableError {
	return &SystemUnavailableError{
		RemoteSystemError: RemoteSystemError{
			id:  id,
			err: err,
		},
	}
}

// NewSystemUnavailableErrorWithCode create new error with provided code
func NewSystemUnavailableErrorWithCode(id string, err string, code int) *SystemUnavailableError {
	return &SystemUnavailableError{
		RemoteSystemError: RemoteSystemError{
			id:   id,
			err:  err,
			code: code,
		},
	}
}

// ConnectionDeclinedError indicates that remote system is available but when attempted
// to connect, the remote system purposefully declined the connection, this could be
// due to various reasons like authentication failure etc. Balancer frontend system
// will terminate the client connection as well in this case aborting the session
// altogether.
type ConnectionDeclinedError struct {
	RemoteSystemError
}

// NewConnectionDeclinedError create new error with provided code
func NewConnectionDeclinedError(id string, err string, code int) *ConnectionDeclinedError {
	return &ConnectionDeclinedError{
		RemoteSystemError: RemoteSystemError{
			id:   id,
			err:  err,
			code: code,
		},
	}
}

// SessionListener is notified on creation and termination of every proxy
// sessions. A reconnect results into SessionTerminated followed by
// SessionCreated calls.
type SessionListener interface {
	SessionCreated(*ProxySession)
	SessionTerminated(*ProxySession)
}

// WsMessage - message being exchanged b/w frontend and backend connections
type WsMessage struct {
	MessageType int
	Data        []byte
}

// String returns a human readable form of the given message for logging
// purposes.
func (wm *WsMessage) String() string {
	var pd string
	if wm.MessageType == websocket.BinaryMessage {
		pd = strconv.Itoa(len(wm.Data)) + " bytes"
	} else {
		pd = string(wm.Data)
	}
	return fmt.Sprintf("[type: %v, data: %v]", wm.MessageType, pd)
}

// IsControl returns true if this is a control message, false otherwise.
func (wm *WsMessage) IsControl() bool {
	if wm.MessageType == websocket.TextMessage ||
		wm.MessageType == websocket.BinaryMessage {
		return false
	}
	return true
}

// Following constants define values which are expected to be returned from various
// methods in BackendAdministrator interface.
const (
	// ResultSuccess indicates successful completion of requested admin action.
	ResultSuccess = 1
	// ResultNoActionReqd indicates that Requested operation didn't need any action.
	// The system is already in requested state.
	ResultNoActionReqd = 3
	// ResultFailed indicates Requested operation failure
	ResultFailed = 4
)

// Additional control message types which are used by balancer for state communication
// between frontend and backend connections. These are NOT standard message types and
// as such they will never appear on wire on either end of a session. These are used
// internally by balancer.
const (
	// RemoteConnectionNormalClosure indicates a proper, i.e. graceful, closure
	// of a connection to other end of a session. frontend can send this message
	// to backend on receipt of proper closure message in which case the backend
	// connection should terminate itself gracefully (with close code
	// CloseNormalClosure).
	//
	// When sent from backend to frontend, the frontend treats this message as
	// an indication that backend wants to close the frontend connection as well
	// and in this case frontend will gracefully close the connection with
	// remote client (with close code CloseNormalClosure).
	//
	// This message type is also used when an administrative request is made to
	// failover an active connection. The frontend will send this message to
	// backend which will result in backend connection terminating itself while
	// frontend attempt a new backend connection and starts sending data to this
	// new backend connection.
	RemoteConnectionNormalClosure = 32

	// RemoteConnectionAbnormalClosure indicate abnormal closure of the remote
	// end. When sent from backend to frontend, frontend treats as abnormal
	// backend error and attempts to initiate a new backend connection WITHOUT
	// terminating the frontend client connection. If a new backend connection
	// is obtained, current frontend connection is proxied to this new backend
	// connection. As far as the remote frontend connection is concerned, it
	// does not see any disruption.
	//
	// This message is NEVER sent by frontend to backend. Even an abnormal
	// frontend connection is indicated as RemoteConnectionNormalClosure by
	// frontend to backend. This is by design since backend can not initiate a
	// new connection to frontend remote end.
	RemoteConnectionAbnormalClosure = 64
)

// Constants which are commonly used across the balancer subsystem and as such
// it is expected that these will be useful across other packages as well which
// are trying to integrate with balancer in any shape or form.
const (
	// SubprotocolHeader Header name for websocket subprotocol, i.e. Sec-WebSocket-Protocol
	SubprotocolHeader = "Sec-WebSocket-Protocol"
	// ForwardedForHeader forwarded for header , i.e. X-Forwarded-For
	ForwardedForHeader = "X-Forwarded-For"
)
