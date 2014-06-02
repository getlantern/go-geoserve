package geoserve

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	geoip2 "github.com/oschwald/geoip2-golang"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DB_URL = "https://geolite.maxmind.com/download/geoip/database/GeoLite2-City.mmdb.gz"
)

// GeoServer is a server for IP geolocation information
type GeoServer struct {
	db       *geoip2.Reader
	dbDate   time.Time
	cache    map[string][]byte // TODO: bound the cache
	cacheGet chan get
	dbUpdate chan dbu
}

// get encapsulates a request to geolocate an ip address
type get struct {
	ip   string
	resp chan []byte
}

// dbu encapsulates an update to the MaxMind database
type dbu struct {
	db     *geoip2.Reader
	dbDate time.Time
}

// NewServer constructs a new GeoServer using the (optional) uncompressed dbFile.
// If dbFile is "", then this will fetch the latest GeoLite2-City database from
// MaxMind's website.
func NewServer(dbFile string) (server *GeoServer, err error) {
	server = &GeoServer{
		cache:    make(map[string][]byte),
		cacheGet: make(chan get, 10000),
		dbUpdate: make(chan dbu),
	}
	if dbFile != "" {
		server.db, server.dbDate, err = readDbFromFile(dbFile)
		if err != nil {
			return nil, err
		}
	} else {
		server.db, server.dbDate, err = readDbFromWeb()
		if err != nil {
			return nil, err
		}
	}
	go server.run()
	go server.keepDbCurrent()
	return
}

// Handle is used to handle requests from an HTTP server.  basePath is the path
// at which the containing request handler is registered, and is used to extract
// the ip address from the remainder of the path.
func (server *GeoServer) Handle(resp http.ResponseWriter, req *http.Request, basePath string) {
	path := strings.Replace(req.URL.Path, basePath, "", 1)
	// Use path as ip
	ip := path
	if ip == "" {
		// When no path supplied, use remote address from X-Forwarded-For header
		ip = req.Header.Get("X-Forwarded-For")
	}
	if ip == "" {
		// When no X-Forwarded-For, use remote address
		ip = strings.Split(req.RemoteAddr, ":")[0]
	}
	g := get{ip, make(chan []byte)}
	server.cacheGet <- g
	jsonData := <-g.resp
	if jsonData == nil {
		resp.WriteHeader(500)
	} else {
		resp.Header().Set("X-Reflected-Ip", clientIpFor(req))
		resp.Write(jsonData)
	}
}

// run runs the geolocation routine which takes care of looking up values from
// the cache, updating the cache and udpating the database when a new version is
// available.
func (server *GeoServer) run() {
	for {
		select {
		case g := <-server.cacheGet:
			jsonData := server.cache[g.ip]
			if jsonData == nil {
				// No cache hit, look it up ourselves
				geoData, err := server.db.City(net.ParseIP(g.ip))
				if err != nil {
					log.Printf("Unable to look up ip address %s: %s", g.ip, err)
				} else {
					jsonData, err = json.Marshal(geoData)
					if err != nil {
						log.Printf("Unable to encode json response for ip address: %s")
					} else {
						// Cache it
						server.cache[g.ip] = jsonData
					}
				}
			}
			g.resp <- jsonData
		case update := <-server.dbUpdate:
			// Update the database
			server.db = update.db
			server.dbDate = update.dbDate
			// Clear the cache
			server.cache = make(map[string][]byte)
		}
	}
}

// keepDbCurrent checks for new versions of the database on the web every minute
// by issuing a HEAD request.  If a new database is found, this downloads it and
// submits it to server.dbUpdate for the run() routine to pick up.
func (server *GeoServer) keepDbCurrent() {
	for {
		time.Sleep(1 * time.Minute)
		headResp, err := http.Head(DB_URL)
		if err != nil {
			log.Printf("Unable to request modified of %s: %s", DB_URL, err)
		}
		lm, err := lastModified(headResp)
		if err != nil {
			log.Printf("Unable to parse modified date for %s: %s", DB_URL, err)
		}
		if lm.After(server.dbDate) {
			log.Printf("Updating database from web")
			db, dbDate, err := readDbFromWeb()
			if err != nil {
				log.Printf("Unable to update database from web: %s", err)
			} else {
				server.dbUpdate <- dbu{db, dbDate}
			}
		}
	}
}

// readDbFromFile reads the MaxMind database and timestamp from a file
func readDbFromFile(dbFile string) (*geoip2.Reader, time.Time, error) {
	dbData, err := ioutil.ReadFile(dbFile)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("Unable to read db file %s: %s", dbFile, err)
	}
	fileInfo, err := os.Stat(dbFile)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("Unable to stat db file %s: %s", dbFile, err)
	}
	dbDate := fileInfo.ModTime()
	db, err := openDb(dbData)
	if err != nil {
		return nil, time.Time{}, err
	} else {
		return db, dbDate, nil
	}
}

// readDbFromWeb reads the MaxMind database and timestamp from the web
func readDbFromWeb() (*geoip2.Reader, time.Time, error) {
	dbResp, err := http.Get(DB_URL)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("Unable to get database from %s: %s", DB_URL, err)
	}
	gzipDbData, err := gzip.NewReader(dbResp.Body)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("Unable to open gzip reader on response body%s", err)
	}
	defer gzipDbData.Close()
	dbData, err := ioutil.ReadAll(gzipDbData)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("Unable to fetch database from HTTP response: %s", err)
	}
	dbDate, err := lastModified(dbResp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("Unable to parse Last-Modified header %s: %s", lastModified, err)
	} else {
		db, err := openDb(dbData)
		if err != nil {
			return nil, time.Time{}, err
		} else {
			return db, dbDate, nil
		}
	}
}

// lastModified parses the Last-Modified header from a response
func lastModified(resp *http.Response) (time.Time, error) {
	lastModified := resp.Header.Get("Last-Modified")
	return http.ParseTime(lastModified)
}

// openDb opens a MaxMind in-memory db using the geoip2.Reader
func openDb(dbData []byte) (*geoip2.Reader, error) {
	db, err := geoip2.FromBytes(dbData)
	if err != nil {
		return nil, fmt.Errorf("Unable to open database: %s", err)
	} else {
		return db, nil
	}
}

func clientIpFor(req *http.Request) string {
	// Client requested their info
	clientIp := req.Header.Get("X-Forwarded-For")
	if clientIp == "" {
		clientIp = strings.Split(req.RemoteAddr, ":")[0]
	}
	// clientIp may contain multiple ips, use the last
	ips := strings.Split(clientIp, ",")
	return strings.TrimSpace(ips[len(ips)-1])
}
