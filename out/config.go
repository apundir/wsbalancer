package out

import (
	"fmt"
	"time"

	"github.com/apundir/wsbalancer/in"
	"github.com/gorilla/websocket"
)

const (
	cfgDefaultPingTime         = 10 * time.Minute
	cfgDefaultBufferSize       = 1024
	cfgDefaultHandshakeTimeout = 10 * time.Second
	cfgDefaultBreakerThreshold = 10
)

var (
	cfgDefaultAbnormalCloseCodes = []int{websocket.CloseAbnormalClosure,
		websocket.CloseGoingAway, websocket.CloseInternalServerErr,
		websocket.CloseNoStatusReceived, websocket.CloseTryAgainLater}
)

// UpstreamConfig defines backend servers configuration
//
// Most of these are environment ONLY having no counterparts into yml. yml
// parameters are in nested BackendConfig. IF any of these env variables are
// provided then these will OVERWRITE any value of those in BackendConfig struct
// from yml configuration for each of the backend.
type UpstreamConfig struct {
	URLs               []string         `env:"BE_URLS" env-description:"Backend URL endpoints, comma separated, APPENDS to existing backends"`
	IDs                []string         `env:"BE_URL_IDS" env-description:"Respective backend URL IDs, comma separated"`
	PingTime           time.Duration    `env:"BE_PING_TIME" env-description:"Backend webSocket ping time"`
	ReadBufferSize     int              `env:"BE_RB_SIZE" env-description:"Backend webSocket ReadBuffer size"`
	WriteBufferSize    int              `env:"BE_WB_SIZE" env-description:"Backend webSocket WriteBuffer size"`
	BreakerThreshold   int              `env:"BE_BREAKER_THRESHOLD" env-description:"Backend circuit breaker threshold"`
	HandshakeTimeout   time.Duration    `env:"BE_HANDSHAKE_TIMEOUT" env-description:"Backend webSocket handshake timeout"`
	PassHeaders        []string         `env:"BE_PASS_HEADERS" env-description:"Headers to pass from B/E to F/E, comma separated"`
	AbnormalCloseCodes []int            `env:"BE_ABNORMAL_CLOSE_CODES" env-description:"Abnormal close codes from B/E, comma separated, OVERWRITE backend config codes"`
	Backends           []*BackendConfig `yaml:"backends"`
	sanitized          bool             // track if this configuration has been sanitized or not
}

// BackendConfig - Single backend server configuration
type BackendConfig struct {
	URL                string        `yaml:"url"`
	ID                 string        `yaml:"id"`
	PingTime           time.Duration `yaml:"ping-time"`
	ReadBufferSize     int           `yaml:"read-buffer-size"`
	WriteBufferSize    int           `yaml:"write-buffer-size"`
	BreakerThreshold   int           `yaml:"breaker-threshold"`
	HandshakeTimeout   time.Duration `yaml:"handshake-timeout"`
	PassHeaders        []string      `yaml:"pass-headers"`
	AbnormalCloseCodes []int         `yaml:"abnormal-close-codes"`
}

func (uc *UpstreamConfig) sanitize() error {
	if uc.sanitized {
		// already sanitized
		return nil
	}
	for _, bk := range uc.Backends {
		uc.sanitizeBackend(bk)
	}
	uc.addEnvBackends()
	return nil
}

func (uc *UpstreamConfig) sanitizeBackend(bk *BackendConfig) {
	uc.addPassHeaders(bk)
	uc.updateAbnormalCloseCodes(bk)
	uc.updateBackendValues(bk)
}

func (uc *UpstreamConfig) addEnvBackends() {
	if len(uc.Backends) > 0 {
		for _, bk := range uc.Backends {
			if bk.ID == "" {
				bk.ID = in.NormalizeForID(bk.URL)
			}
		}
	}
	if len(uc.URLs) < 1 {
		return
	}
	useIds := false
	if len(uc.IDs) > 0 {
		if len(uc.URLs) != len(uc.IDs) {
			panic(fmt.Sprintf("mismatch in count of URL endpoints(%v) and that of IDS(%v)",
				len(uc.URLs), len(uc.IDs)))
		}
		useIds = true
	}
	if uc.Backends == nil {
		uc.Backends = make([]*BackendConfig, 0, len(uc.URLs))
	}
	aID := ""
	for idx, aURL := range uc.URLs {
		if useIds {
			aID = uc.IDs[idx]
		}
		uc.appendBackend(aURL, aID)
		aID = ""
	}
}

func (uc *UpstreamConfig) appendBackend(URL string, ID string) *BackendConfig {
	if ID == "" {
		ID = in.NormalizeForID(URL)
	}
	bk := &BackendConfig{ID: ID, URL: URL}
	uc.Backends = append(uc.Backends, bk)
	uc.sanitizeBackend(bk)
	return bk
}

func (uc *UpstreamConfig) addPassHeaders(bk *BackendConfig) {
	if len(uc.PassHeaders) < 1 {
		return
	}
	if bk.PassHeaders == nil {
		bk.PassHeaders = make([]string, 0, len(uc.PassHeaders))
	}
	bk.PassHeaders = append(bk.PassHeaders, uc.PassHeaders...)
}

func (uc *UpstreamConfig) updateAbnormalCloseCodes(bk *BackendConfig) {
	codesToUse := cfgDefaultAbnormalCloseCodes
	if len(bk.AbnormalCloseCodes) > 0 {
		codesToUse = bk.AbnormalCloseCodes
	}
	if bk.AbnormalCloseCodes == nil {
		bk.AbnormalCloseCodes = make([]int, 0, len(codesToUse))
	}
	bk.AbnormalCloseCodes = append(bk.AbnormalCloseCodes, codesToUse...)
}

func (uc *UpstreamConfig) updateBackendValues(bk *BackendConfig) {
	zeroD := time.Duration(0)
	// PingTime
	if uc.PingTime != zeroD {
		bk.PingTime = uc.PingTime
	} else if bk.PingTime == zeroD {
		bk.PingTime = cfgDefaultPingTime
	}
	// HandshakeTimeout
	if uc.HandshakeTimeout != zeroD {
		bk.HandshakeTimeout = uc.HandshakeTimeout
	} else if bk.HandshakeTimeout == zeroD {
		bk.HandshakeTimeout = cfgDefaultHandshakeTimeout
	}
	// BreakerThreshold
	if uc.BreakerThreshold != 0 {
		bk.BreakerThreshold = uc.BreakerThreshold
	} else if bk.BreakerThreshold == 0 {
		bk.BreakerThreshold = cfgDefaultBreakerThreshold
	}
	// ReadBufferSize
	if uc.ReadBufferSize != 0 {
		bk.ReadBufferSize = uc.ReadBufferSize
	} else if bk.ReadBufferSize == 0 {
		bk.ReadBufferSize = cfgDefaultBufferSize
	}
	// WriteBufferSize
	if uc.WriteBufferSize != 0 {
		bk.WriteBufferSize = uc.WriteBufferSize
	} else if bk.WriteBufferSize == 0 {
		bk.WriteBufferSize = cfgDefaultBufferSize
	}
}
