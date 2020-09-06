package common

import (
	"flag"
	"testing"

	"github.com/gorilla/websocket"
)

var flagTestLogLevel = flag.String("loglevel", "panic", "logging level to kep for test")
var flagTestParallel = flag.Bool("parallel", false, "execute tests in parallel, defaults to serial execution")
var flagTestValidateLeaks = flag.Bool("check-for-leaks", true, "check for goroutine leaks after every test, defaults to true")

func TestRemoteSystemError(t *testing.T) {
	err := NewConnectionDeclinedError("bkId", "test error", 400)
	if err.GetID() != "bkId" {
		t.Errorf("ID mismatch")
	}
	if err.Error() != "bkId reported status code 400 with test error" {
		t.Errorf("Error string not matching expected value")
	}
	if err.Code() != 400 {
		t.Errorf("Code didn't match, expecting: %v, got: %v", 400, err.Code())
	}
	er2 := NewSystemUnavailableError("bkId", "test error")
	if er2.GetID() != "bkId" {
		t.Errorf("ID mismatch")
	}
	if er2.Error() != "bkId reported test error" {
		t.Errorf("Error string not matching expected value")
	}
	if er2.Code() != 0 {
		t.Errorf("Code didn't match, expecting: %v, got: %v", 0, err.Code())
	}

}

func TestWsMessage(t *testing.T) {
	msg := WsMessage{}
	msg.Data = []byte("test")
	msg.MessageType = websocket.BinaryMessage
	if msg.String() != "[type: 2, data: 4 bytes]" {
		t.Errorf("message string representation didn't match expected value")
	}
	msg.MessageType = websocket.TextMessage
	if msg.String() != "[type: 1, data: test]" {
		t.Errorf("message string representation didn't match expected value")
	}
}
