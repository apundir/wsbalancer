package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/apundir/wsbalancer/cmd"
	"github.com/apundir/wsbalancer/in"
	"github.com/apundir/wsbalancer/out"
	slog "github.com/go-eden/slf4go"
	"github.com/ilyakaznacheev/cleanenv"
)

var flagHelp = flag.Bool("help", false, "Print help and exit")
var flagConfigFile = flag.String("config", "", "path of the configuration file for balancer")

var (
	frontendServer *http.Server
	adminServer    *http.Server
)

func main() {
	flag.Parse()
	if *flagHelp {
		printHelp(0)
	}
	var cfg cmd.Config
	if *flagConfigFile != "" {
		err := cleanenv.ReadConfig(*flagConfigFile, &cfg)
		if err != nil {
			fmt.Println(err)
			printHelp(1)
		}
	} else {
		// if no file is given than BE configuration MUST have been given in
		// environment variables.
		err := cleanenv.ReadEnv(&cfg)
		if err != nil {
			fmt.Println(err)
			printHelp(1)
		}
	}
	cmd.SetLogLevel(cfg.Server.LoggingLevel)
	outMgr, err := out.NewManager(&cfg.Upstream)
	if err != nil {
		fmt.Println(err.Error())
		printHelp(1)
	}
	inMgr := in.NewManager(&cfg.Server, outMgr)
	inMgr.Start()
	wg := &sync.WaitGroup{}
	wg.Add(2)
	go startFrontendServer(&cfg.Server, inMgr, wg)
	go startAdminServer(&cfg.Server, inMgr, wg)
	wg.Wait()
}

func startFrontendServer(cfg *in.ServerConfig, inMgr *in.Manager, wg *sync.WaitGroup) {
	defer wg.Done()
	frontendServer = cmd.BuildFrontendServer(cfg, inMgr)
	slog.Info("Starting Frontend server on ", cfg.FrontendHTTP.Addr)
	slog.Fatal(frontendServer.ListenAndServe())
}

func startAdminServer(cfg *in.ServerConfig, inMgr *in.Manager, wg *sync.WaitGroup) {
	defer wg.Done()
	adminServer := cmd.BuildAdminServer(cfg, inMgr)
	slog.Info("Starting Admin server on ", cfg.AdminHTTP.Addr)
	slog.Fatal(adminServer.ListenAndServe())
}

func printHelp(exitCode int) {
	fmt.Printf("Usage of %s:\n", os.Args[0])
	flag.CommandLine.SetOutput(os.Stdout)
	flag.PrintDefaults()
	var cfg cmd.Config
	help, err := cleanenv.GetDescription(&cfg, nil)
	if err == nil {
		fmt.Println(help)
	} else {
		fmt.Println(err)
	}
	os.Exit(exitCode)
}
