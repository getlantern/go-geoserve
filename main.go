// go-geoserve provides an ip geolocation server that's made so that it can run
// on Heroku.  It uses the MaxMind GeoIP2City Lite data form the MaxMind
// website.  It checks for updates every minute and automatically downloads the
// most recent version of that database.
//
// The server caches JSON results by ip address for low-latency lookups.
//
// When starting the server, the following environment variables control its
// behavior:
//
//    PORT - integer port on which to listen
//    DB - optional filename of local database file (useful for testing, not Heroku)
//    ALLOW_ORIGIN - optional cors access control for the response header ("*", "example.com", etc.)
//
//
// To request JSON geolocation information for your IP:
//
//    curl http://go-geoserve.herokuapp.com/lookup/
//
// To request JSON geolocation information for a specific IP:
//
//    curl http://go-geoserve.herokuapp.com/lookup/66.69.242.177
//
// Sample response:
//
//     {
//         "City": {
//             "GeoNameID": 4671654,
//             "Names": {
//                 "de": "Austin",
//                 "en": "Austin",
//                 "es": "Austin",
//                 "fr": "Austin",
//                 "ja": "オースティン",
//                 "pt-BR": "Austin",
//                 "ru": "Остин"
//             }
//         },
//         "Continent": {
//             "Code": "NA",
//             "GeoNameID": 6255149,
//             "Names": {
//                 "de": "Nordamerika",
//                 "en": "North America",
//                 "es": "Norteamérica",
//                 "fr": "Amérique du Nord",
//                 "ja": "北アメリカ",
//                 "pt-BR": "América do Norte",
//                 "ru": "Северная Америка",
//                 "zh-CN": "北美洲"
//             }
//         },
//         "Country": {
//             "GeoNameID": 6252001,
//             "IsoCode": "US",
//             "Names": {
//                 "de": "USA",
//                 "en": "United States",
//                 "es": "Estados Unidos",
//                 "fr": "États-Unis",
//                 "ja": "アメリカ合衆国",
//                 "pt-BR": "Estados Unidos",
//                 "ru": "США",
//                 "zh-CN": "美国"
//             }
//         },
//         "Location": {
//             "Latitude": 30.2672,
//             "Longitude": -97.7431,
//             "MetroCode": "635",
//             "TimeZone": "America/Chicago"
//         },
//         "Postal": {
//             "Code": ""
//         },
//         "RegisteredCountry": {
//             "GeoNameID": 6252001,
//             "IsoCode": "US",
//             "Names": {
//                 "de": "USA",
//                 "en": "United States",
//                 "es": "Estados Unidos",
//                 "fr": "États-Unis",
//                 "ja": "アメリカ合衆国",
//                 "pt-BR": "Estados Unidos",
//                 "ru": "США",
//                 "zh-CN": "美国"
//             }
//         },
//         "RepresentedCountry": {
//             "GeoNameID": 0,
//             "IsoCode": "",
//             "Names": null,
//             "Type": ""
//         },
//         "Subdivisions": [
//             {
//                 "GeoNameID": 4736286,
//                 "IsoCode": "TX",
//                 "Names": {
//                     "en": "Texas",
//                     "es": "Texas",
//                     "ja": "テキサス州",
//                     "ru": "Техас",
//                     "zh-CN": "得克萨斯州"
//                 }
//             }
//         ],
//         "Traits": {
//             "IsAnonymousProxy": false,
//             "IsSatelliteProvider": false
//         }
//     }
//
package main

import (
	"net/http"
	"os"

	"github.com/getlantern/golog"

	"github.com/getlantern/go-geoserve/geoserve"
)

var (
	log = golog.LoggerFor("go-geoserve")
)

func main() {
	log.Debug("Creating GeoServer, this can take a while")
	geoServer, err := geoserve.NewServer(os.Getenv("DB"), os.Getenv("MAXMIND_LICENSE_KEY"))
	if err != nil {
		log.Fatalf("Unable to create geoserve server: %s", err)
	}
	allowOrigin := os.Getenv("ALLOW_ORIGIN")
	log.Debugf("Access-Control-Allow-Origin set to: %s", allowOrigin)
	http.HandleFunc("/lookup/", func(resp http.ResponseWriter, req *http.Request) {
		geoServer.Handle(resp, req, "/lookup/", allowOrigin)
	})
	http.HandleFunc("/lookup", func(resp http.ResponseWriter, req *http.Request) {
		geoServer.Handle(resp, req, "/lookup", allowOrigin)
	})
	port := os.Getenv("PORT")
	log.Debugf("About to listen at port: %s", port)
	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatalf("Unable to start HTTP server: %s", err)
	}
}
