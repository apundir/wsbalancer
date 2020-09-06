package cmd

import (
	"github.com/apundir/wsbalancer/in"
	"github.com/apundir/wsbalancer/out"
)

// Config is the composite configuration for frontend as well as backend
// servers. This configuration is used to bootstrap balancer within main
// package.
type Config struct {
	Server   in.ServerConfig    `yaml:"server"`
	Upstream out.UpstreamConfig `yaml:"upstream"`
}
