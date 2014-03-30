package main

import (
	"github.com/oxtoacart/go-geoserve/geoserve"
	"log"
	"net/http"
	"os"
)

func main() {
	log.Println("Creating GeoServer, this can take a while")
	geoServer, err := geoserve.NewServer(os.Getenv("DB"))
	if err != nil {
		log.Fatalf("Unable to create geoserve server: %s", err)
	}
	http.HandleFunc("/lookup/", func(resp http.ResponseWriter, req *http.Request) {
		geoServer.Handle(resp, req, "/lookup/")
	})
	http.HandleFunc("/lookup", func(resp http.ResponseWriter, req *http.Request) {
		geoServer.Handle(resp, req, "/lookup")
	})
	port := os.Getenv("PORT")
	log.Printf("About to listen at port: %s", port)
	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatalf("Unable to start HTTP server: %s", err)
	}
}
