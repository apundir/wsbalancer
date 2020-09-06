package in

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apundir/wsbalancer/common"
	"github.com/gorilla/websocket"
)

// inc is an incoming client connection type
type inc struct {
	// websocket connection type
	con *websocket.Conn

	// Headers received from remote end
	header *http.Header

	// Original incoming request URL
	url *url.URL

	// For sending messages to backend
	send chan *common.WsMessage

	// For receiving messages from backend
	receive chan *common.WsMessage

	// channel to indicate no further processing required (during shutdown)
	done chan bool
	// lock for using done channel
	doneLock sync.RWMutex

	// to send unregistration request to manager
	unregister chan<- *inc

	// for waiting on availability of backend connection
	bkConnectionLock *sync.RWMutex

	// Message valves in use for this connection
	messageValves []common.MessageValve

	// unique id of the frontend which created this connection
	frontendID string

	// unique id for this connection itself
	id string

	// number of messages received from remote client so far
	totalReceived int

	// number of messages sent to remote client so far
	totalSent int

	// how many times this connection has been re-connected to backend. First
	// connection is NOT counted as re-connect
	totalReconnects int

	// tracking wait state while awaiting new backend connection
	reinitializing uint32

	// for protecting against concurrent close requests
	closeOnce *sync.Once

	// true while closing down
	shuttingDown uint32
}

// newInc - constructs a new client object
func newInc(conn *websocket.Conn, hdr *http.Header, fid string,
	url *url.URL, unregister chan<- *inc) *inc {
	ic := &inc{
		con:              conn,
		header:           hdr,
		url:              url,
		unregister:       unregister,
		frontendID:       fid,
		reinitializing:   0,
		bkConnectionLock: &sync.RWMutex{},
		closeOnce:        &sync.Once{},
		shuttingDown:     0,
		doneLock:         sync.RWMutex{},
	}
	ic.id = fmt.Sprintf("%p", ic)[2:]
	return ic
}

// GetID - returns a general purpose Identifier for usage in logs and
//   instrumentation purposes.
func (inc *inc) GetID() string {
	return inc.id
}

// FrontendID returns unique id for the frontend server
func (inc *inc) FrontendID() string {
	return inc.frontendID
}

// RequestURI returns the full uri (excluding host) for this connection
func (inc *inc) RequestURI() string {
	return inc.url.RequestURI()
}

// Close incoming channel and cleanup
func (inc *inc) Close() error {
	inc.closeOnce.Do(func() {
		atomic.StoreUint32(&inc.shuttingDown, 1)
		inc.doneLock.RLock()
		defer inc.doneLock.RUnlock()
		inc.done <- false
		ilog.Debugf("[%v] Closing FE WS connection", inc.id)
		inc.con.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second))
		inc.con.Close()
		if atomic.LoadUint32(&inc.reinitializing) == 1 {
			inc.bkConnectionLock.Unlock()
		}
	})
	return nil
}

func (inc *inc) isClientActive() bool {
	if atomic.LoadUint32(&inc.shuttingDown) == 1 {
		return false
	}
	return true
}

// Start - Start read and write routines
func (inc *inc) Start() {
	inc.doneLock.Lock()
	defer inc.doneLock.Unlock()
	inc.done = make(chan bool, 5)
	// start read and write routines
	go inc.startReading()
	go inc.startWriting()
}

func (inc *inc) resume() {
	ilog.Debugf("[%v] resuming session", inc.id)
	inc.totalReconnects++
	inc.doneLock.Lock()
	defer inc.doneLock.Unlock()
	inc.done = make(chan bool, 5)
	// Let the reading routine resume
	atomic.StoreUint32(&inc.reinitializing, 0)
	inc.bkConnectionLock.Unlock()
	// start a fresh writing routine
	go inc.startWriting()
}

// abort the backend and re-initiate new backend connection
func (inc *inc) abortBackend() {
	ilog.Infof("[%v] Aborting backend", inc.id)
	// send a normal closure request to backend so that it closes
	// remote connection and clean itself.
	inc.doneLock.RLock()
	defer inc.doneLock.RUnlock()
	inc.done <- true
	inc.sendToUpstream(&common.WsMessage{
		MessageType: common.RemoteConnectionNormalClosure,
		Data:        []byte("Administratively aborted"),
	})
}

// startReading - start reading from the client connection and write
// this data to channel for proxying to upstream connection
func (inc *inc) startReading() {
	defer func() {
		inc.Close()
		inc.unregister <- inc
		ilog.Debugf("[%v] frontend reading routine done", inc.id)
	}()
	for {
		mt, message, err := inc.con.ReadMessage()
		if err != nil {
			ilog.Warnf("[%v] FE connection read error: %v", inc.id, err)
			inc.sendToUpstream(&common.WsMessage{
				MessageType: common.RemoteConnectionNormalClosure,
				Data:        []byte(err.Error()),
			})
			return
		}
		msg := &common.WsMessage{
			MessageType: mt,
			Data:        message,
		}
		if msg.IsControl() {
			// it's neither TextMessage nor BinaryMessage
			continue
		}
		inc.sendToUpstream(msg)
	}
}

// startWriting reads from upstream channel and write data to client
func (inc *inc) startWriting() (reInit bool) {
	defer func() {
		if reInit {
			inc.reinitUpstream()
		} else {
			inc.Close()
		}
		ilog.Debugf("[%v] frontend writing routine done", inc.id)
	}()
	for {
		select {
		case msg := <-inc.receive:
			if msg != nil {
				msg = inc.onReceiveFromUpstream(msg)
			}
			if msg == nil || msg.MessageType == common.RemoteConnectionNormalClosure {
				return false
			}
			if msg.MessageType == common.RemoteConnectionAbnormalClosure {
				// this is an abnormal closure thus we MUST reInit upstream connection
				return true
			}
			if err := inc.con.WriteMessage(msg.MessageType, msg.Data); err != nil {
				ilog.Warnf("[%v] Error writing to FE socket", inc.id, err.Error())
				return false
			}
			ilog.Tracef("[%v]  <-client: %s", inc.id, msg)
		case reInit := <-inc.done:
			return reInit
		}
	}
}

func (inc *inc) sendToUpstream(msg *common.WsMessage) {
	var err error
	// ignore balancer control messages
	if msg.MessageType < common.RemoteConnectionNormalClosure {
		inc.totalReceived++
	}
	for _, vl := range inc.messageValves {
		msg, err = vl.BeforeTransmit(msg)
		if err != nil {
			ilog.Errorf("[%v] Valve BeforeTransmit error: %s", inc.id, err)
			return
		}
	}
	if msg == nil {
		return
	}
	// Lock not needed for control messages as these must be sent
	// immediately to current channel
	if msg.IsControl() {
		inc.send <- msg
		ilog.Tracef("[%v] ->client: %s", inc.id, msg)
		return
	}
	inc.bkConnectionLock.RLock()
	defer inc.bkConnectionLock.RUnlock()
	if atomic.LoadUint32(&inc.shuttingDown) == 1 {
		return
	}
	inc.send <- msg
	ilog.Tracef("[%v] ->client: %s", inc.id, msg)
}

func (inc *inc) onReceiveFromUpstream(msg *common.WsMessage) *common.WsMessage {
	var err error
	for i := len(inc.messageValves); i > 0; i-- {
		msg, err = inc.messageValves[i-1].AfterReceive(msg)
		if err != nil {
			ilog.Errorf("[%v] Valve AfterReceive error: %s", inc.id, err)
			return nil
		}
	}
	// ignore balancer control messages
	if msg.MessageType < common.RemoteConnectionNormalClosure {
		inc.totalSent++
	}
	return msg
}

// initiate the process for acquiring fresh upstream connection from the manager
func (inc *inc) reinitUpstream() {
	initReqd := atomic.CompareAndSwapUint32(&inc.reinitializing, 0, 1)
	if !initReqd {
		return
	}
	inc.bkConnectionLock.Lock()
	ilog.Infof("[%v] reinitializing backend connection", inc.id)
	inc.unregister <- inc
}
