package ambassador

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"strconv"
	"time"

	cache "github.com/patrickmn/go-cache"
	"github.com/sirupsen/logrus"
)

type Ambassador struct {
	listen     string
	experation int
	cleanup    int
	cache      *cache.Cache
	cacheSize  int64
	logger     *logrus.Logger
}

type cachedContent struct {
	creation time.Time
	expiry   time.Time
	data     map[string][]byte
}

func NewAmbassador() *Ambassador {
	a := &Ambassador{}

	// Create a cache with a default expiration time of 5 minutes, and which
	// purges expired items every 10 minutes
	a.cache = cache.New(5*time.Minute, 10*time.Minute)

	// Logrus logger setup
	a.logger = logrus.New()

	// Listen on ...
	a.listen = "0.0.0.0:80"

	return a
}

func (a *Ambassador) Run() error {
	// Start the proxy server
	http.HandleFunc("/", a.handleRequest)
	err := http.ListenAndServe(a.listen, nil)
	if err != nil {
		return err
	}

	return nil
}

func (a *Ambassador) handleRequest(res http.ResponseWriter, req *http.Request) {
	a.logger.Infof("Got request for url: %s", getURL(req))
	// lookup in local cache
	contentInterface, cached := a.cache.Get(getURL(req))

	// if in local cache
	if cached {
		content := contentInterface.(*cachedContent)
		a.logger.Info("Serving from cache")
		res.Write(content.data["uncompressed"])
	} else {
		a.logger.Info("Getting content from origin")
		a.proxyRequest(res, req)
	}
}

func (a *Ambassador) cacheContent(resp *http.Response) error {
	// Read the response body to save it
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	err = resp.Body.Close()
	if err != nil {
		return err
	}

	// Parse max-age
	// Wed, 12 Feb 2020 15:30:00 GMT
	maxTime, err := time.Parse("Wed, 2 Jan 2006 15:04:05 MST", resp.Header.Get("max-age"))
	if err != nil {
		a.logger.Infof("error parsing time: %s", err)
	}
	var dur time.Duration
	if maxTime.IsZero() {
		dur = 5 * time.Minute
	} else {
		dur = maxTime.Sub(time.Now())
	}

	data := make(map[string][]byte)
	data["uncompressed"] = b

	// cache the content
	cc := &cachedContent{
		creation: time.Now(),
		expiry:   maxTime,
		data:     data,
	}
	url := getURL(resp.Request)
	a.logger.Infof("Caching content: %s for %s", url, dur)
	a.cache.Set(url, cc, dur)
	body := ioutil.NopCloser(bytes.NewReader(b))
	resp.Body = body
	resp.ContentLength = int64(len(b))
	resp.Header.Set("Content-Length", strconv.Itoa(len(b)))
	return nil
}

func (a *Ambassador) proxyRequest(res http.ResponseWriter, req *http.Request) {
	// get the url
	url := req.URL
	host := req.Host

	// create the reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(url)

	// Update the headers to allow for SSL redirection
	req.URL.Host = host
	req.URL.Scheme = "http"
	req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
	req.Host = host

	proxy.ModifyResponse = a.cacheContent
	// Note that ServeHttp is non blocking and uses a go routine under the hood
	proxy.ServeHTTP(res, req)
}

func getURL(req *http.Request) string {
	return fmt.Sprintf("%s%s%s", "http://", req.Host, req.RequestURI)
}
