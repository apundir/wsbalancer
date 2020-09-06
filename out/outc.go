package out

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apundir/wsbalancer/common"
	"github.com/gorilla/websocket"
)

// messageCache is used to cache messages which couldn't be written
// to backend server due to IO error. These are sent first as
// and when the connection is established again to backend.
type messageCache struct {
	cache []*common.WsMessage
}

func newMessageCache(oldCache *messageCache) *messageCache {
	nc := &messageCache{}
	if oldCache == nil {
		nc.cache = make([]*common.WsMessage, 0)
	} else {
		nc.cache = oldCache.clear()
	}
	return nc
}

func (um *messageCache) add(msg *common.WsMessage) {
	um.cache = append(um.cache, msg)
}

func (um *messageCache) clear() []*common.WsMessage {
	ret := um.cache
	um.cache = make([]*common.WsMessage, 0)
	return ret
}

// outc - outgoing client object
type outc struct {
	// websocket connection object
	con *websocket.Conn

	// For sending messages to F/E
	send chan<- *common.WsMessage

	// For receiving messages from F/E
	receive <-chan *common.WsMessage

	// how often a ping be sent to backend server
	pingWait time.Duration

	// for tracking this connection is being cleaned up
	shuttingDown uint32

	// unique id for this connection
	id string

	// Identifier for upstream server
	backendID string

	// channel for signaling closure of websocket
	done chan struct{}

	// channel for signaling to backend that this connection is done
	bkDone chan<- struct{}

	// headers that shall be passed form backend to frontend AS-IS
	passHeaders http.Header

	// for safeguarding concurrent closure
	closeOnce *sync.Once

	// for storing unsent messages in event of IO failure
	cache *messageCache

	// for safeguarding concurrent notification to frontned
	notifyFEOnce *sync.Once
}

func newOutc(c *websocket.Conn, rcv <-chan *common.WsMessage,
	snd chan<- *common.WsMessage, pingTime time.Duration,
	bkID string, conDone chan<- struct{},
	passHeaders http.Header, oldCon *outc) *outc {
	oc := &outc{
		con:          c,
		receive:      rcv,
		send:         snd,
		pingWait:     pingTime,
		backendID:    bkID,
		bkDone:       conDone,
		passHeaders:  passHeaders,
		notifyFEOnce: &sync.Once{},
		closeOnce:    &sync.Once{},
		shuttingDown: 0,
		done:         make(chan struct{}, 4),
	}
	var oldCache *messageCache = nil
	if oldCon != nil {
		oldCache = oldCon.cache
	}
	oc.cache = newMessageCache(oldCache)
	return oc
}

// Start - start read and write routines
func (oc *outc) Start() {
	go oc.startReading()
	go oc.startWriting()
}

// Close - closes this connection
func (oc *outc) Close() error {
	oc.closeOnce.Do(func() {
		olog.Debugf("[%v] closing BE WS connection", oc.id)
		atomic.StoreUint32(&oc.shuttingDown, 1)
		oc.con.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second))
		oc.con.Close()
		oc.done <- struct{}{}
		olog.Debugf("[%v] BE connection closed", oc.id)
	})
	return nil
}

// Subprotocol returns the negotiated protocol for the connection.
func (oc *outc) Subprotocol() string {
	return oc.con.Subprotocol()
}

// AdditionalHeaders headers from backend that shall be passed
// onto frontend AS-IS, may include Cookie etc. Subprotocol
// should NOT be part of these headers.
func (oc *outc) AdditionalHeaders() http.Header {
	return oc.passHeaders
}

// BackendID returns the unique id for the backend server
func (oc *outc) BackendID() string {
	return oc.backendID
}

// GetID - returns unique id for this connection.
func (oc *outc) GetID() string {
	return oc.id
}

// notifyBackend - notifies backend about closure of this connection and
// handle any panic if raised.
func (oc *outc) notifyBackend() {
	if oc.bkDone == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			olog.Warnf("[%v] Recovered from panic during notifyBackend", oc.id, r)
		}
		oc.bkDone = nil
	}()
	// this may panic in case the channel is already closed
	oc.bkDone <- struct{}{}
}

// startReading - reads from upstream connection and write it to proxy's channel
func (oc *outc) startReading() {
	defer func() {
		oc.Close()
		oc.notifyBackend()
		olog.Debugf("[%v] backend reader routine done", oc.id)
	}()
	for {
		mt, message, err := oc.con.ReadMessage()
		if atomic.LoadUint32(&oc.shuttingDown) == 1 {
			return // in cleanup phase, no further action needed
		}
		if err != nil {
			oc.notifyFrontend(err, "reading")
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
		oc.send <- msg
		olog.Tracef("[%v] client<-proxy: %s", oc.id, msg)
	}
}

func (oc *outc) notifyFrontend(err error, direction string) {
	oc.notifyFEOnce.Do(func() {
		oc._notifyFrontend(err, direction)
	})
}

func (oc *outc) _notifyFrontend(err error, direction string) {
	pxMsgType := common.RemoteConnectionNormalClosure
	// identify if this is non websocket error, e.g. network disconnect
	_, wceOk := err.(*websocket.CloseError)
	if !wceOk || websocket.IsCloseError(err, websocket.CloseAbnormalClosure, websocket.CloseGoingAway,
		websocket.CloseInternalServerErr, websocket.CloseNoStatusReceived,
		websocket.CloseTryAgainLater) {
		// these close codes demands a new upstream connections shall be established
		pxMsgType = common.RemoteConnectionAbnormalClosure
		olog.Infof("[%v] backend abnormally closed while %v, error: %v", oc.id, direction, err)
	} else {
		olog.Infof("[%v] backend normally closed while %v, error: %v", oc.id, direction, err)
	}
	msg := &common.WsMessage{
		MessageType: pxMsgType,
		Data:        []byte(err.Error()),
	}
	oc.send <- msg
	olog.Tracef("[%v]  client<-proxy: %s", oc.id, msg)
}

// startWriting - start the writing routine which read from FE channel
// and write it to BE server connection.
func (oc *outc) startWriting() {
	ticker := time.NewTicker(oc.pingWait)
	defer func() {
		ticker.Stop()
		oc.Close()
		olog.Debugf("[%v] backend writer routine done", oc.id)
	}()
	// cleanup existing cache by sending all messages form that first
	if !oc.clearCache() {
		return
	}
	for {
		select {
		case msg := <-oc.receive:
			if msg == nil || msg.MessageType == common.RemoteConnectionNormalClosure {
				// well, client is gone, cleanup upstream connection
				olog.Debugf("[%v] received control message: %v", oc.id, msg)
				return
			}
			if wrr := oc.con.WriteMessage(msg.MessageType, msg.Data); wrr != nil {
				oc.notifyFrontend(wrr, "writing")
				oc.cache.add(msg)
				return
			}
			olog.Tracef("[%v] client->proxy: %s", oc.id, msg)
		case <-ticker.C:
			err := oc.con.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second))
			if err != nil {
				oc.notifyFrontend(err, "writing ping")
				return
			}
		case <-oc.done:
			return
		}
	}
}

func (oc *outc) clearCache() bool {
	cached := oc.cache.clear()
	olog.Debugf("[%v] clearing %v cached messages", oc.id, len(cached))
	cacheClearError := false
	for _, am := range cached {
		if cacheClearError {
			oc.cache.add(am)
			continue
		}
		wrr := oc.con.WriteMessage(am.MessageType, am.Data)
		if wrr == nil {
			continue
		}
		cacheClearError = true
		oc.cache.add(am)
		oc.notifyFrontend(wrr, "writing cache")
	}
	if !cacheClearError {
		return true
	}
	return false
}
