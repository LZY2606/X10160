package main

import (
	"flag"
	"log"
	"net/http"

	"calibration-trace/internal/httpapi"
	"calibration-trace/internal/service"
	"calibration-trace/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:5234", "HTTP listen address")
	dataPath := flag.String("data", "data/calibration-trace.json", "JSON data file")
	flag.Parse()
	st, err := store.New(*dataPath)
	if err != nil {
		log.Fatal(err)
	}
	server := http.Server{Addr: *addr, Handler: httpapi.New(service.New(st))}
	log.Printf("校准追溯站 listening on http://%s", *addr)
	log.Fatal(server.ListenAndServe())
}
