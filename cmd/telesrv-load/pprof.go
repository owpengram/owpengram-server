package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
)

// TELESRV_LOAD_PPROF=127.0.0.1:6070 exposes the load generator's own pprof
// endpoints, to check whether a run is limited by the generator rather than
// by the server under test.
func init() {
	addr := os.Getenv("TELESRV_LOAD_PPROF")
	if addr == "" {
		return
	}
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("telesrv-load: pprof listener: %v", err)
		}
	}()
}
