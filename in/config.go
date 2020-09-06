package in

import "time"

// ServerConfig combines Frontend and Admin http servers configuration and other
// parameters needed by frontend system to operate properly. The configuration
// is supported via yml configuration file or via environment variables. Another
// option, especially useful for those integrating balancer into their own
// program, is to build up this configuration by any means they need to and use
// this configuration to bootstrap balancer.
//
// All the parameters having env tags can be set by passing these parameters as
// environment variables while launching the balancer. If set, environment
// variables takes precedence over values from yml. All parameters having
// env-default in their tag will assume these values if not present either in
// yml file OR in environment variable.
//
// Environment variables are evaluated and considered ONLY if yml configuration
// file is being used for configuration. Environment variables will have NO
// effect if you are programatically building configuration yourself instead of
// parsing a yml config file.
type ServerConfig struct {
	FrontendHTTP          FrontendHTTPConfig `yaml:"frontend"`
	AdminHTTP             AdminHTTPConfig    `yaml:"admin"`
	ReadBufferSize        int                `yaml:"read-buffer-size" env:"FE_RB_SIZE" env-description:"WebSocket ReadBuffer size (bytes)" env-default:"1024"`
	WriteBufferSize       int                `yaml:"write-buffer-size" env:"FE_WB_SIZE" env-description:"WebSocket WriteBuffer size (bytes)" env-default:"1024"`
	TrustReceivedXff      bool               `yaml:"trust-received-xff" env:"FE_TRUST_XFF" env-description:"Trust X-Forwarded-For header from clients" env-default:"true"`
	IgnoreHeaders         []string           `yaml:"block-headers" env:"BLOCK_HEADERS" env-description:"Additional headers to block, comma separated"`
	ReconnectHeader       string             `yaml:"reconnect-header" env:"RECONNECT_HDR" env-description:"Header to include during reconnect" env-default:"X-Balancer-Reconnect"`
	EnableInstrumentation bool               `yaml:"enable-instrumentation" env:"ENABLE_INST" env-description:"Enable Prometheus instrumentation" env-default:"false"`
	LoggingLevel          string             `yaml:"logging-level" env:"LOG_LEVEL" env-description:"Logging level" env-default:"WARN"`
}

// FrontendHTTPConfig is the HTTP server config for incoming websocket requests.
// This endpoint must be exposed to outside world using appropriate means (via
// another reverse-proxy or firewall or cloud balancer etc)
type FrontendHTTPConfig struct {
	Addr           string        `yaml:"address" env:"FE_ADDR" env-description:"Frontend server listen address" env-default:":8080"`
	ReadTimeout    time.Duration `yaml:"read-timeout" env:"FE_READ_TIMEOUT" env-description:"Frontend server read timeout" env-default:"10s"`
	WriteTimeout   time.Duration `yaml:"wite-timeout" env:"FE_WRITE_TIMEOUT" env-description:"Frontend server write timeout" env-default:"10s"`
	MaxHeaderBytes int           `yaml:"max-header-bytes" env:"FE_MAX_HDR_BYTES" env-description:"Frontend server Max Header Size" env-default:"524288"`
}

// AdminHTTPConfig is the HTTP server config for receiving admin API requests.
// This endpoint MUST NOT be exposed to outside world. As of now, the admin
// interface DO NOT have any security in place and it is designed to be used
// within a trusted environment. Future version of balancer may add security
// controls to admin interface.
type AdminHTTPConfig struct {
	Addr           string        `yaml:"address" env:"ADM_ADDR" env-description:"Admin server listen address" env-default:":8081"`
	ReadTimeout    time.Duration `yaml:"read-timeout" env:"ADM_READ_TIMEOUT" env-description:"Admin server read timeout" env-default:"10s"`
	WriteTimeout   time.Duration `yaml:"wite-timeout" env:"ADM_WRITE_TIMEOUT" env-description:"Admin server write timeout" env-default:"10s"`
	MaxHeaderBytes int           `yaml:"max-header-bytes" env:"ADM_MAX_HDR_BYTES" env-description:"Admin server Max Header Size" env-default:"524288"`
}
