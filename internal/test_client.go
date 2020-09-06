package internal_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/apundir/wsbalancer/common"
	"github.com/gorilla/websocket"
)

// TestClient for unit/integration tests
type TestClient struct {
	Endpoint     string
	Dialer       *websocket.Dialer
	Sent         int
	Received     int
	LastPongAt   time.Time
	WsConnection *websocket.Conn
}

// newTestClient creates a new TestClient against given config
func newTestClient(cfg *TestClientConfig) *TestClient {
	dialer := &websocket.Dialer{
		ReadBufferSize:   cfg.BufferSize,
		WriteBufferSize:  cfg.BufferSize,
		HandshakeTimeout: cfg.HandshakeTimeout,
	}
	if len(cfg.Subprotocol) > 0 {
		dialer.Subprotocols = cfg.Subprotocol
	}
	return &TestClient{
		Endpoint: cfg.Endpoint,
		Dialer:   dialer,
	}
}

// Connect - connects to endpoint
func (tc *TestClient) Connect() (*http.Response, error) {
	return tc.ConnectWithHeaders(nil)
}

// ConnectWithHeaders connects to endpoint with provided headers
func (tc *TestClient) ConnectWithHeaders(reqHeaders http.Header) (*http.Response, error) {
	c, resp, err := tc.Dialer.Dial(tc.Endpoint, reqHeaders)
	if err != nil {
		return resp, err
	}
	tc.WsConnection = c
	tc.setPongHandler()
	return resp, err
}

// Subprotocol returns the negotiated protocol for the connection.
func (tc *TestClient) Subprotocol() string {
	return tc.WsConnection.Subprotocol()
}

// ResetStats reset all internal stats
func (tc *TestClient) ResetStats() {
	tc.Sent = 0
	tc.Received = 0
	tc.LastPongAt = time.Time{}
}

// Send sends given message
func (tc *TestClient) Send(msg *common.WsMessage) error {
	tc.WsConnection.SetWriteDeadline(time.Now().Add(time.Second))
	tc.Sent++
	return tc.WsConnection.WriteMessage(msg.MessageType, msg.Data)
}

// SendText sends a text message
func (tc *TestClient) SendText(data string) error {
	return tc.Send(&common.WsMessage{MessageType: websocket.TextMessage, Data: []byte(data)})
}

// SendBinary sends a binary message
func (tc *TestClient) SendBinary(data string) error {
	return tc.Send(&common.WsMessage{MessageType: websocket.BinaryMessage, Data: []byte(data)})
}

// ReadMessage reads one message from connection
func (tc *TestClient) ReadMessage() (*common.WsMessage, error) {
	tc.WsConnection.SetReadDeadline(time.Now().Add(time.Second))
	mt, data, err := tc.WsConnection.ReadMessage()
	if err != nil {
		return nil, err
	}
	tc.Received++
	return &common.WsMessage{MessageType: mt, Data: data}, err
}

// closeWithCode closes the connection with given Closure code
func (tc *TestClient) closeWithCode(closeCode int) error {
	if tc.WsConnection == nil {
		return nil // already closed
	}
	wc := tc.WsConnection
	tc.WsConnection = nil
	wc.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, ""), time.Now().Add(time.Second))
	return wc.Close()
}

// Ping send a ping message to server
func (tc *TestClient) Ping(data string) error {
	return tc.WsConnection.WriteControl(websocket.PingMessage, []byte(data), time.Now().Add(time.Second))
}

func (tc *TestClient) setPongHandler() {
	tc.WsConnection.SetPongHandler(func(appData string) error {
		tc.LastPongAt = time.Now()
		return nil
	})
}

// SendRecvTestMessage sends a text message to client and reads response expecting
// same response form server in return. Returns the received message data slice.
// You can pass a message yourself, if not passed (nil) then a new text message
// is created and sent to server.
func (tc *TestClient) SendRecvTestMessage(t *testing.T,
	inMsg interface{},
	expectFailure bool,
	expectDiffResponse bool) []byte {
	msg := tc.prepareMessage(inMsg)
	wErr := tc.Send(msg)
	if wErr != nil && !expectFailure {
		t.Errorf("Unexpected error while sending message %v, error %v", msg, wErr)
		return nil
	}
	rMsg, err := tc.ReadMessage()
	if err != nil {
		if !expectFailure {
			t.Errorf("Unexpected error while reading message %v", err)
		}
		return nil
	}
	if !bytes.Equal(rMsg.Data, msg.Data) {
		if !expectDiffResponse {
			t.Errorf("received message different than what was sent. "+
				"Expected %v, Found %v", msg, rMsg)
		}
	}
	if rMsg.MessageType != msg.MessageType {
		t.Errorf("Message type failed, Expected %v, Found %v", msg.MessageType, rMsg.MessageType)
	}
	return rMsg.Data
}

func (tc *TestClient) prepareMessage(inMsg interface{}) *common.WsMessage {
	if inMsg == nil {
		return &common.WsMessage{
			Data:        []byte("Msg at: " + strconv.FormatInt(time.Now().UnixNano(), 10)),
			MessageType: websocket.TextMessage,
		}
	}
	_msg, isWsMsg := inMsg.(*common.WsMessage)
	if isWsMsg {
		return _msg
	}
	dt := fmt.Sprintf("%v", inMsg)
	return &common.WsMessage{
		Data:        []byte(dt),
		MessageType: websocket.TextMessage,
	}
}
