package main

import (
	"flag"
	"fmt"
	"log"

	"prom-live-svg/internal/app"
	"prom-live-svg/internal/config"
)

func main() { // H
	configPath := flag.String("config", "", "Path to a YAML or JSON configuration file")
	checkConfig := flag.Bool("check-config", false, "Validate configuration and exit")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	if *checkConfig {
		fmt.Println("configuration valid")
		return
	}

	if err := app.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
