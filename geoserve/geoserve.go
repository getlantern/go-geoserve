package geoserve

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	geoip2 "github.com/oxtoacart/geoip2-golang"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DB_URL = "https://geolite.maxmind.com/download/geoip/database/GeoLite2-City.mmdb.gz"
)

type GeoServer struct {
	db       *geoip2.Reader
	dbDate   time.Time
	cache    map[string][]byte
	cacheGet chan get
	cachePut chan put
}

type get struct {
	ip   string
	resp chan []byte
}

type put struct {
	ip       string
	jsonData []byte
}

// NewServer constructs a new GeoServer using the (optional) uncompressed dbFile.
// If dbFile is "", then this will fetch the latest GeoLite2-City database from
// MaxMind's website.
func NewServer(dbFile string) (server *GeoServer, err error) {
	var dbData []byte
	var dbDate time.Time
	if dbFile != "" {
		dbData, err = ioutil.ReadFile(dbFile)
		if err != nil {
			return nil, fmt.Errorf("Unable to read db file %s: %s", dbFile, err)
		}
		fileInfo, err := os.Stat(dbFile)
		if err != nil {
			return nil, fmt.Errorf("Unable to stat db file %s: %s", dbFile, err)
		}
		dbDate = fileInfo.ModTime()
	} else {
		dbResp, err := http.Get(DB_URL)
		if err != nil {
			return nil, fmt.Errorf("Unable to get database from %s: %s", DB_URL, err)
		}
		gzipDbData, err := gzip.NewReader(dbResp.Body)
		if err != nil {
			return nil, fmt.Errorf("Unable to open gzip reader on response body%s", err)
		}
		defer gzipDbData.Close()
		dbData, err = ioutil.ReadAll(gzipDbData)
		if err != nil {
			return nil, fmt.Errorf("Unable to fetch database from HTTP response: %s", err)
		}
		lastModified := dbResp.Header.Get("Last-Modified")
		dbDate, err = http.ParseTime(lastModified)
		if err != nil {
			return nil, fmt.Errorf("Unable to parse Last-Modified header %s: %s", lastModified, err)
		}
	}
	db, err := geoip2.OpenBytes(dbData)
	if err != nil {
		return nil, fmt.Errorf("Unable to open database: %s", err)
	}
	server = &GeoServer{
		db:       db,
		dbDate:   dbDate,
		cache:    make(map[string][]byte),
		cacheGet: make(chan get, 10000),
		cachePut: make(chan put, 10000),
	}
	go server.manageCache()
	return
}

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
		// No cache hit, look it up ourselves
		geoData, err := server.db.City(net.ParseIP(ip))
		if err != nil {
			resp.WriteHeader(500)
			resp.Write([]byte(fmt.Sprintf("Unable to look up ip address: %s", err)))
			return
		} else {
			jsonData, err = json.Marshal(geoData)
			if err != nil {
				resp.WriteHeader(500)
				resp.Write([]byte(fmt.Sprintf("Unable to encode ip address: %s", err)))
				return
			} else {
				// Put back in cache
				server.cachePut <- put{ip, jsonData}
			}
		}
	}
	resp.Write(jsonData)
}

func (server *GeoServer) manageCache() {
	for {
		select {
		case g := <-server.cacheGet:
			g.resp <- server.cache[g.ip]
		case p := <-server.cachePut:
			server.cache[p.ip] = p.jsonData
		}
	}
}
