package internal_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// AdminBackend as returned from admin API
type AdminBackend struct {
	URL               string `json:"url"`
	ID                string `json:"id"`
	ActiveConnections int    `json:"activeConnections"`
	Enabled           bool   `json:"enabled"`
	Tripped           bool   `json:"tripped"`
	TotalConnections  int    `json:"totalConnections"`
	TotalFailures     int    `json:"totalFailures"`
}

// NewBackendData for sending add backend requests to admin interface
type NewBackendData struct {
	URL                string   `json:"url"`
	ID                 string   `json:"id"`
	PingTime           string   `json:"ping-time"`
	ReadBufferSize     int      `json:"read-buffer-size"`
	WriteBufferSize    int      `json:"write-buffer-size"`
	BreakerThreshold   int      `json:"breaker-threshold"`
	HandshakeTimeout   string   `json:"handshake-timeout"`
	PassHeaders        []string `json:"pass-headers"`
	AbnormalCloseCodes []int    `json:"abnormal-close-codes"`
}

// Config get config of this backend using admin request to server
func (ab *AdminBackend) Config(t *testing.T, config *TestConfig) *NewBackendData {
	cData := &NewBackendData{}
	GetAdminRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/backend/config/"+ab.ID, cData)
	return cData
}

// Enable this backend using admin request to server
func (ab *AdminBackend) Enable(t *testing.T, config *TestConfig) *AdminActionResponse {
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/backend/enable/"+ab.ID, []byte{})
}

// Disable this backend using admin request to server
func (ab *AdminBackend) Disable(t *testing.T, config *TestConfig) *AdminActionResponse {
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/backend/disable/"+ab.ID, []byte{})
}

// Delete this backend using admin request to server
func (ab *AdminBackend) Delete(t *testing.T, config *TestConfig) *AdminActionResponse {
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/backend/delete/"+ab.ID, []byte{})
}

// Add this backend using admin request to server
func (ab *AdminBackend) Add(t *testing.T, config *TestConfig, abConfig *NewBackendData) *AdminActionResponse {
	var bkJSON []byte
	if abConfig == nil {
		bkJSON, _ = json.Marshal(ab)
	} else {
		bkJSON, _ = json.Marshal(abConfig)
	}
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/backend/add", bkJSON)
}

// Failover this backend using admin request to server
func (ab *AdminBackend) Failover(t *testing.T, config *TestConfig) *AdminActionResponse {
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/backend/failover/"+ab.ID, []byte{})
}

// Reset this backend's trigger status using admin request to server
func (ab *AdminBackend) Reset(t *testing.T, config *TestConfig) *AdminActionResponse {
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/backend/reset/"+ab.ID, []byte{})
}

// AdminActionResponse action request response from admin APIs
type AdminActionResponse struct {
	Result     string `json:"result"`
	Reason     string `json:"reason"`
	StatusCode int
}

// String returns human readable string
func (aar *AdminActionResponse) String() string {
	return fmt.Sprintf("[Result: %v, Reason: %v, StatusCode: %v]",
		aar.Result, aar.Reason, aar.StatusCode)
}

// Failed returns true if this response states failed
func (aar *AdminActionResponse) Failed() bool {
	if "failed" == aar.Result {
		return true
	}
	return false
}

// Successful returns true if this response states success
func (aar *AdminActionResponse) Successful() bool {
	if "success" == aar.Result {
		return true
	}
	return false
}

// NoActionReqd returns true if this response indicate there was
// no action taken by the server
func (aar *AdminActionResponse) NoActionReqd() bool {
	if "success" != aar.Result {
		return false
	}
	if "NoActionRequired" == aar.Reason {
		return true
	}
	return false
}

// BadRequest returns true if the HTTP response code was 400 (Bad Request)
func (aar *AdminActionResponse) BadRequest() bool {
	if aar.StatusCode == http.StatusBadRequest {
		return true
	}
	return false
}

// AdminFrontend frontend as returned from admin API
type AdminFrontend struct {
	ID string `json:"id"`
}

// Pause this frontend using admin request to server
func (af *AdminFrontend) Pause(t *testing.T, config *TestConfig) *AdminActionResponse {
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/frontend/pause/"+af.ID, []byte{})
}

// Resume this frontend using admin request to server
func (af *AdminFrontend) Resume(t *testing.T, config *TestConfig) *AdminActionResponse {
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/frontend/resume/"+af.ID, []byte{})
}

//AdminSession session as returned from admin API
type AdminSession struct {
	ID           string `json:"id"`
	URI          string `json:"uri"`
	BackendID    string `json:"backendId"`
	Reconnects   int    `json:"reconnects"`
	MsgsReceived int    `json:"msgsReceived"`
	MsgsSent     int    `json:"msgsSent"`
}

// Failover this session using admin request to server
func (ss *AdminSession) Failover(t *testing.T, config *TestConfig) *AdminActionResponse {
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/session/failover/"+ss.ID, []byte{})
}

// Abort this session using admin request to server
func (ss *AdminSession) Abort(t *testing.T, config *TestConfig) *AdminActionResponse {
	return PostAdminActionRequest(t, "http://"+config.Server.AdminHTTP.Addr+"/session/abort/"+ss.ID, []byte{})
}
