package main

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/AdguardTeam/golibs/log"
	"github.com/AdguardTeam/gomitmproxy"
	"github.com/AdguardTeam/gomitmproxy/mitm"
	"github.com/AdguardTeam/gomitmproxy/proxyutil"
	"github.com/caarlos0/env/v11"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// initMitm initializes the MITM configuration for the proxy.
func initMitm() *mitm.Config {
	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		log.Fatal(err)
	}
	privateKey := tlsCert.PrivateKey.(*rsa.PrivateKey)

	x509c, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		log.Fatal(err)
	}

	mitmConfig, err := mitm.NewConfig(x509c, privateKey, nil)
	if err != nil {
		log.Fatal(err)
	}

	mitmConfig.SetValidity(time.Hour * 24 * 356) // generate certs valid for 1 year
	mitmConfig.SetOrganization("gitmproxy")      // cert organization
	return mitmConfig
}

func main() {
	log.Info("Starting Gopher in the middle cache proxy...")

	config := env.Must(env.ParseAs[Config]())
	config.Print()

	// Initialize the disk cache
	diskCache, err := NewDiskCache(config,
		&http.Transport{
			TLSClientConfig:    &tls.Config{InsecureSkipVerify: true},
			DisableCompression: true,
		})
	if err != nil {
		log.Fatal(err)
	}

	// Create an HTTP client with the disk cache transport
	cacheClient := http.Client{
		Transport: diskCache,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Create an HTTP client without caching
	noCacheClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// resolve TCP address for the proxy to listen on
	addr, err := net.ResolveTCPAddr("tcp", config.ListenAddr)
	if err != nil {
		log.Fatal(err)
	}

	// Create a handler for the Prometheus metrics endpoint
	prometheusHandler := promhttp.Handler()

	// Initialize the proxy with the MITM configuration and request handler
	proxy := gomitmproxy.NewProxy(gomitmproxy.Config{
		ListenAddr: addr,
		MITMConfig: initMitm(),

		OnRequest: func(session *gomitmproxy.Session) (*http.Request, *http.Response) {
			req := session.Request()
			if req.Method == http.MethodConnect {
				return nil, nil
			}

			// handle metrics endpoint
			if req.URL.Path == "/_gitmproxy_metrics" {
				rw := NewResponseWriter()
				prometheusHandler.ServeHTTP(rw, req)
				return nil, rw.Response(req)
			}

			// ignore requests to the proxy itself
			if strings.HasPrefix(req.URL.Host, "127.0.0.1") || strings.HasPrefix(req.URL.Host, "localhost") {
				// do not proxy requests to localhost or
				return nil, proxyutil.NewResponse(http.StatusNotFound, nil, req)
			}

			// count HTTP requests
			mHttpRequestsTotal.WithLabelValues(req.Method).Add(1)
			req.RequestURI = ""

			var response *http.Response
			// cache only GET requests
			if req.Method == http.MethodGet {
				response, err = cacheClient.Do(req)
			} else {
				response, err = noCacheClient.Do(req)
			}

			// handle errors from the HTTP client
			if err != nil {
				body := strings.NewReader(err.Error())
				res := proxyutil.NewResponse(http.StatusInternalServerError, body, req)
				return nil, res
			}

			return nil, response
		},
	})
	err = proxy.Start()
	if err != nil {
		log.Fatal(err)
	}

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	<-signalChannel

	// Clean up.
	proxy.Close()
}
