package main

import (
	"crypto/tls"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/acme/autocert"

	relay "github.com/pcc-258/device-relay"
	"github.com/pcc-258/device-relay/internal/server"
)

func main() {
	listen := flag.String("listen", envOr("DEVICE_RELAY_LISTEN", ":8080"), "HTTP listen address (plain HTTP mode)")
	httpsListen := flag.String("https-listen", envOr("DEVICE_RELAY_HTTPS_LISTEN", ":443"), "HTTPS listen address")
	httpListen := flag.String("http-listen", envOr("DEVICE_RELAY_HTTP_LISTEN", ":80"), "HTTP listen address for ACME challenges and redirects")
	domain := flag.String("domain", os.Getenv("DEVICE_RELAY_DOMAIN"), "public domain; enables automatic Let's Encrypt certificates")
	acmeEmail := flag.String("acme-email", os.Getenv("DEVICE_RELAY_ACME_EMAIL"), "contact email used for Let's Encrypt")
	adminToken := flag.String("admin-token", envOr("DEVICE_RELAY_ADMIN_TOKEN", "admin"), "admin token for the web UI")
	deviceToken := flag.String("device-token", envOr("DEVICE_RELAY_DEVICE_TOKEN", "device"), "shared token used by device agents")
	dataDir := flag.String("data-dir", envOr("DEVICE_RELAY_DATA_DIR", "./data"), "directory for persisted state")
	tlsCert := flag.String("tls-cert", os.Getenv("DEVICE_RELAY_TLS_CERT"), "optional TLS certificate file")
	tlsKey := flag.String("tls-key", os.Getenv("DEVICE_RELAY_TLS_KEY"), "optional TLS private key file")
	flag.Parse()

	web, err := fs.Sub(relay.WebFS, "web/dist")
	if err != nil {
		log.Fatalf("embedded web ui unavailable: %v", err)
	}
	srv, err := server.NewServer(server.Config{
		AdminToken:  *adminToken,
		DeviceToken: *deviceToken,
		DataDir:     *dataDir,
		WebFS:       web,
	})
	if err != nil {
		log.Fatalf("create server: %v", err)
	}
	handler := srv.Handler()

	switch {
	case *domain != "":
		runAutomaticHTTPS(handler, *domain, *acmeEmail, *dataDir, *httpsListen, *httpListen, *adminToken)
	case *tlsCert != "" && *tlsKey != "":
		log.Printf("device relay serving HTTPS on %s", *httpsListen)
		httpSrv := &http.Server{Addr: *httpsListen, Handler: handler}
		log.Fatal(httpSrv.ListenAndServeTLS(*tlsCert, *tlsKey))
	default:
		log.Printf("device relay serving HTTP on %s (enable -domain or -tls-cert/-tls-key for HTTPS)", *listen)
		log.Printf("web ui: http://%s/ (admin token: %s)", displayHost(*listen), *adminToken)
		httpSrv := &http.Server{Addr: *listen, Handler: handler}
		log.Fatal(httpSrv.ListenAndServe())
	}
}

func runAutomaticHTTPS(handler http.Handler, domain, acmeEmail, dataDir, httpsListen, httpListen, adminToken string) {
	certDir := filepath.Join(dataDir, "certs")
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      autocert.DirCache(certDir),
		Email:      acmeEmail,
	}

	httpsSrv := &http.Server{
		Addr:    httpsListen,
		Handler: handler,
		TLSConfig: &tls.Config{
			GetCertificate: manager.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		},
	}
	go func() {
		challengeHandler := manager.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
		}))
		log.Printf("ACME/redirect server on %s for %s", httpListen, domain)
		challengeSrv := &http.Server{Addr: httpListen, Handler: challengeHandler}
		log.Fatal(challengeSrv.ListenAndServe())
	}()

	log.Printf("device relay serving HTTPS on %s for %s", httpsListen, domain)
	log.Printf("web ui: https://%s/ (admin token: %s)", domain, adminToken)
	log.Fatal(httpsSrv.ListenAndServeTLS("", ""))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func displayHost(listen string) string {
	if strings.HasPrefix(listen, ":") {
		return "127.0.0.1" + listen
	}
	return listen
}
