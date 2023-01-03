package geoserve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/getlantern/golog"
	"github.com/golang/groupcache/lru"
	"github.com/mholt/archiver/v3"
	geoip2 "github.com/oschwald/geoip2-golang"
)

const (
	DB_URL = "https://download.maxmind.com/app/geoip_download?license_key=%s&edition_id=GeoLite2-City&suffix=tar.gz"

	CacheSize = 50000
)

var (
	log            = golog.LoggerFor("go-geoserve")
	errNotModified = errors.New("unmodified")
)

// GeoServer is a server for IP geolocation information
type GeoServer struct {
	db       *geoip2.Reader
	dbURL    string
	cache    *lru.Cache
	cacheGet chan get
	dbUpdate chan *geoip2.Reader
}

// get encapsulates a request to geolocate an ip address
type get struct {
	ip   string
	resp chan []byte
}

// NewServer constructs a new GeoServer using the (optional) uncompressed dbFile.
// If dbFile is "", then this will fetch the latest GeoLite2-City database from
// MaxMind's website using the license_key provided.
func NewServer(dbFile, license_key string) (server *GeoServer, err error) {
	server = &GeoServer{
		cache:    lru.New(CacheSize),
		cacheGet: make(chan get, 10000),
		dbUpdate: make(chan *geoip2.Reader),
	}
	var lastModified time.Time
	if dbFile != "" {
		server.db, lastModified, err = readDbFromFile(dbFile)
		if err != nil {
			return nil, err
		}
	} else {
		server.dbURL = fmt.Sprintf(DB_URL, license_key)
		server.db, lastModified, err = readDbFromWeb(server.dbURL, time.Time{})
		if err != nil {
			return nil, err
		}
	}
	go server.run()
	go server.keepDbCurrent(lastModified)
	return
}

// HandleCors writes the access control allow origin for the lookup handler
// from the ALLOW_ORIGIN environment variable. If empty, no access control is
// not added to the response header.
func HandleCors(resp *http.ResponseWriter, allowOrigin string) {
    if allowOrigin != "" {
        (*resp).Header().Set("Access-Control-Allow-Origin", allowOrigin)
    }
}

// Handle is used to handle requests from an HTTP server. basePath is the path
// at which the containing request handler is registered, and is used to extract
// the ip address from the remainder of the path. allowOrigin is the cors
// response config.
func (server *GeoServer) Handle(resp http.ResponseWriter, req *http.Request, basePath string, allowOrigin string) {
    HandleCors(&resp, allowOrigin)
	path := strings.Replace(req.URL.Path, basePath, "", 1)
	// Use path as ip
	ip := path
	if ip == "" {
		// When no path supplied, grab remote address or X-Forwarded-For
		ip = clientIpFor(req)
	}
	g := get{ip, make(chan []byte)}
	server.cacheGet <- g
	jsonData := <-g.resp
	if jsonData == nil {
		resp.WriteHeader(500)
	} else {
		resp.Header().Set("X-Reflected-Ip", ip)
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

			if cached, found := server.cache.Get(g.ip); found {
				log.Trace("Cache hit")
				g.resp <- cached.([]byte)
			} else {
				jsonData, err := server.lookupDB(g.ip)
				if err != nil {
					log.Error(err)
				} else {
					server.cache.Add(g.ip, jsonData)
				}
				g.resp <- jsonData
			}
		case db := <-server.dbUpdate:
			if server.db != nil {
				log.Debug("Closing old database")
				server.db.Close()
			}
			log.Debug("Applying new database")
			server.db = db
			log.Debug("Clearing cached lookups")
			server.cache = lru.New(CacheSize)
		}
	}
}

func (server *GeoServer) lookupDB(ip string) ([]byte, error) {
	geoData, err := server.db.City(net.ParseIP(ip))
	if err != nil {
		return nil, fmt.Errorf("Unable to look up ip address %s: %s", ip, err)
	}
	jsonData, err := json.Marshal(geoData)
	if err != nil {
		return nil, fmt.Errorf("Unable to encode json response for ip address: %s", ip)
	}
	return jsonData, nil
}

// keepDbCurrent checks the MaxMind database URL every hour and downloads it if it's
// newer and submits it to server.dbUpdate for the run() routine to pick up.
func (server *GeoServer) keepDbCurrent(lastModified time.Time) {
	for {
		time.Sleep(1 * time.Hour)
		db, modifiedTime, err := readDbFromWeb(server.dbURL, lastModified)
		if err == errNotModified {
			continue
		}
		if err != nil {
			log.Errorf("Unable to update database from web: %s", err)
			continue
		}
		lastModified = modifiedTime
		server.dbUpdate <- db
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
	lastModified := fileInfo.ModTime()
	db, err := openDb(dbData)
	if err != nil {
		return nil, time.Time{}, err
	} else {
		return db, lastModified, nil
	}
}

// readDbFromWeb reads the MaxMind database and timestamp from the web
func readDbFromWeb(url string, ifModifiedSince time.Time) (*geoip2.Reader, time.Time, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Add("If-Modified-Since", ifModifiedSince.Format(http.TimeFormat))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("Unable to get database from %s: %s", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, time.Time{}, errNotModified
	}
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("unexpected HTTP status %v", resp.Status)
	}
	lastModified, err := getLastModified(resp)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("Unable to parse Last-Modified header %s: %s", lastModified, err)
	}

	unzipper := archiver.NewTarGz()
	err = unzipper.Open(resp.Body, 0)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer unzipper.Close()
	for {
		f, err := unzipper.Read()
		if err != nil {
			return nil, time.Time{}, err
		}
		if f.Name() == "GeoLite2-City.mmdb" {
			dbData, err := ioutil.ReadAll(f)
			if err != nil {
				return nil, time.Time{}, err
			}
			db, err := openDb(dbData)
			if err != nil {
				return nil, time.Time{}, err
			}
			return db, lastModified, nil
		}
	}
	return nil, time.Time{}, err
}

// getLastModified parses the Last-Modified header from a response
func getLastModified(resp *http.Response) (time.Time, error) {
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
	// clientIp may contain multiple ips, use the first
	ips := strings.Split(clientIp, ",")
	return strings.TrimSpace(ips[0])
}
