package in

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/apundir/wsbalancer/common"
)

type mockBackendAdmin struct{}

func (mba *mockBackendAdmin) DialUpstream(dialContext *common.BackendDialContext) (common.BackendConnection, error) {
	return nil, nil
}

func (mba *mockBackendAdmin) ListBackends() string {
	return ""
}
func (mba *mockBackendAdmin) AddBackend(data []byte) string {
	return ""
}
func (mba *mockBackendAdmin) BackendConfig(id string) string {
	return ""
}
func (mba *mockBackendAdmin) DisableBackend(id string) (int, error) {
	return 0, nil
}
func (mba *mockBackendAdmin) EnableBackend(id string) (int, error) {
	return 0, nil
}
func (mba *mockBackendAdmin) DeleteBackend(id string) (int, error) {
	return 0, nil
}
func (mba *mockBackendAdmin) ResetBackend(id string) (int, error) {
	return 0, nil
}

func TestWithNonAdminBackend(t *testing.T) {
	adm := &admin{}
	adm.manager = &Manager{}

	verifyNotImplemented(t, http.HandlerFunc(adm.listBackendHandler), "GET", "/backend/list")
	verifyNotImplemented(t, http.HandlerFunc(adm.addBackendHandler), "POST", "/backend/add")
	verifyNotImplemented(t, http.HandlerFunc(adm.backendConfigHandler), "GET", "/backend/config/id")
	verifyNotImplemented(t, http.HandlerFunc(adm.disableBackendHandler), "GET", "/backend/disable/id")
	verifyNotImplemented(t, http.HandlerFunc(adm.enableBackendHandler), "GET", "/backend/enable/id")
	verifyNotImplemented(t, http.HandlerFunc(adm.resetBackendHandler), "GET", "/backend/reset/id")
	verifyNotImplemented(t, http.HandlerFunc(adm.deleteBackendHandler), "GET", "/backend/delete/id")
}

func verifyNotImplemented(t *testing.T, handler http.HandlerFunc, verb string, url string) {
	req, err := http.NewRequest(verb, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotImplemented {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusNotImplemented)
	}

	expected := `{"result":"failed", "reason":"NotImplemented"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v",
			rr.Body.String(), expected)
	}
}

func TestWithInvalidHTTPVerb(t *testing.T) {
	adm := &admin{}
	adm.manager = &Manager{}
	adm.backendAdmin = &mockBackendAdmin{}

	verifyHTTPPostRequired(t, http.HandlerFunc(adm.addBackendHandler), "/backend/add")
	verifyHTTPPostRequired(t, http.HandlerFunc(adm.disableBackendHandler), "/backend/disable/id")
	verifyHTTPPostRequired(t, http.HandlerFunc(adm.enableBackendHandler), "/backend/enable/id")
	verifyHTTPPostRequired(t, http.HandlerFunc(adm.resetBackendHandler), "/backend/reset/id")
	verifyHTTPPostRequired(t, http.HandlerFunc(adm.deleteBackendHandler), "/backend/delete/id")
	verifyHTTPPostRequired(t, http.HandlerFunc(adm.failoverBackendHandler), "/backend/failover/id")
	verifyHTTPPostRequired(t, http.HandlerFunc(adm.abortSessionHandler), "/session/abort/id")
	verifyHTTPPostRequired(t, http.HandlerFunc(adm.failoverSessionHandler), "/session/failover/id")
	verifyHTTPPostRequired(t, http.HandlerFunc(adm.pauseFrontendHandler), "/frontend/pause/id")
	verifyHTTPPostRequired(t, http.HandlerFunc(adm.resumeFrontendHandler), "/frontend/resume/id")
}

func verifyHTTPPostRequired(t *testing.T, handler http.HandlerFunc, url string) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusBadRequest)
	}

	expected := `{"result":"failed", "reason":"OnlyPostAllowed"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v",
			rr.Body.String(), expected)
	}
}
