package internal_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// GetAdminActionRequest perform a HTTP Get request on given endpoint
func GetAdminActionRequest(t *testing.T, ep string) *AdminActionResponse {
	var hClient = &http.Client{}
	defer hClient.CloseIdleConnections()
	resp, err := hClient.Get(ep)
	return parseAdminActionResponse(t, ep, resp, err)
}

// GetAdminRequest perform a HTTP Get request and unmarshal response
// into supplied returnData
func GetAdminRequest(t *testing.T, ep string, returnData interface{}) {
	var hClient = &http.Client{}
	defer hClient.CloseIdleConnections()
	resp, err := hClient.Get(ep)
	if err != nil {
		t.Fatalf("Unable to connect to %v, error %v", ep, err)
	}
	defer resp.Body.Close()
	err = json.NewDecoder(resp.Body).Decode(returnData)
	if err != nil {
		t.Fatal("Unable to parse response", err)
	}
}

// PostAdminActionRequest perform a HTTP Post request on given endpoint
func PostAdminActionRequest(t *testing.T, ep string, postData []byte) *AdminActionResponse {
	var hClient = &http.Client{}
	defer hClient.CloseIdleConnections()
	resp, err := hClient.Post(ep, "application/json", bytes.NewBuffer(postData))
	return parseAdminActionResponse(t, ep, resp, err)
}

func parseAdminActionResponse(t *testing.T,
	ep string, resp *http.Response,
	err error) *AdminActionResponse {
	if err != nil {
		t.Fatalf("Unable to connect to %v, error %v", ep, err)
	}
	defer resp.Body.Close()
	var actResponse AdminActionResponse
	err = json.NewDecoder(resp.Body).Decode(&actResponse)
	if err != nil {
		t.Fatal("Unable to parse response", err)
	}
	actResponse.StatusCode = resp.StatusCode
	return &actResponse
}

// GetAdminBackendListAsMap fetches and convert backend list as map
func GetAdminBackendListAsMap(t *testing.T, config *TestConfig) map[string]AdminBackend {
	bkMap := map[string]AdminBackend{}
	bl := GetAdminBackendList(t, config)
	for _, ab := range bl {
		bkMap[ab.ID] = ab
	}
	return bkMap
}

// GetAdminBackendList fetches admin listing from admin
func GetAdminBackendList(t *testing.T, config *TestConfig) []AdminBackend {
	var hClient = &http.Client{}
	defer hClient.CloseIdleConnections()
	ep := "http://" + config.Server.AdminHTTP.Addr + "/backend/list"
	resp, err := hClient.Get(ep)
	if err != nil {
		t.Fatalf("Unable to connect to %v, error %v", ep, err)
	}
	defer resp.Body.Close()
	var bList []AdminBackend
	err = json.NewDecoder(resp.Body).Decode(&bList)
	if err != nil {
		t.Fatal("Unable to parse response", err)
	}
	return bList
}

// GetAdminSessionList fetches and return list of session from admin
func GetAdminSessionList(t *testing.T, config *TestConfig) []AdminSession {
	var hClient = &http.Client{}
	defer hClient.CloseIdleConnections()
	ep := "http://" + config.Server.AdminHTTP.Addr + "/session/list"
	resp, err := hClient.Get(ep)
	if err != nil {
		t.Fatalf("Unable to connect to %v, error %v", ep, err)
	}
	defer resp.Body.Close()
	var bList []AdminSession
	err = json.NewDecoder(resp.Body).Decode(&bList)
	if err != nil {
		t.Fatal("Unable to parse response", err)
	}
	return bList
}

// GetAdminFrontendList fetches and return list of session from admin
func GetAdminFrontendList(t *testing.T, config *TestConfig) []AdminFrontend {
	var hClient = &http.Client{}
	defer hClient.CloseIdleConnections()
	ep := "http://" + config.Server.AdminHTTP.Addr + "/frontend/list"
	resp, err := hClient.Get(ep)
	if err != nil {
		t.Fatalf("Unable to connect to %v, error %v", ep, err)
	}
	defer resp.Body.Close()
	var bList []AdminFrontend
	err = json.NewDecoder(resp.Body).Decode(&bList)
	if err != nil {
		t.Fatal("Unable to parse response", err)
	}
	return bList
}
