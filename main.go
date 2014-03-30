// go-geoserve provides an ip geolocation server that's made so that it can run
// on Heroku.  It uses the MaxMind GeoIP2City Lite data form the MaxMind
// website.  It checks for updates every minute and automatically downloads the
// most recent version of that database.  At the moment, only IPv4 is supported.
//
// To request JSON geolocation information for your IP:
//
//    http://serene-plains-5039.herokuapp.com/lookup/
//
// To request JSOn geolocation information for a specific IP:
//
//    http://serene-plains-5039.herokuapp.com/lookup/66.69.242.177
//
// The server caches JSON results by ip address for low-latency lookups.
//
// When starting the server, the following environment variables control its
// behavior:
//
//    PORT - integer port on which to listen
//    DB - optional filename of local database file (useful for testing, not Heroku)
//
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
