package main

import (
	"context"
	"crypto/tls"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"

	relay "github.com/pcc-258/warpmesh"
	"github.com/pcc-258/warpmesh/internal/server"
)

func main() {
	listen := flag.String("listen", envOr("DEVICE_RELAY_LISTEN", ":8080"), "HTTP listen address (plain HTTP mode)")
	httpsListen := flag.String("https-listen", envOr("DEVICE_RELAY_HTTPS_LISTEN", ":443"), "primary HTTPS listen address")
	altHTTPSListen := flag.String("alt-https-listen", os.Getenv("DEVICE_RELAY_ALT_HTTPS_LISTEN"), "optional secondary HTTPS listen address")
	httpListen := flag.String("http-listen", envOr("DEVICE_RELAY_HTTP_LISTEN", ":80"), "HTTP listen address for ACME challenges and redirects")
	domain := flag.String("domain", os.Getenv("DEVICE_RELAY_DOMAIN"), "public domain; enables automatic Let's Encrypt certificates")
	acmeEmail := flag.String("acme-email", os.Getenv("DEVICE_RELAY_ACME_EMAIL"), "contact email used for Let's Encrypt")
	adminToken := flag.String("admin-token", envOr("DEVICE_RELAY_ADMIN_TOKEN", "admin"), "admin token for the web UI")
	adminUser := flag.String("admin-user", envOr("DEVICE_RELAY_ADMIN_USER", "admin"), "initial console username")
	adminPassword := flag.String("admin-password", envOr("DEVICE_RELAY_ADMIN_PASSWORD", "admin"), "initial console password")
	keyTTL := flag.String("key-ttl", envOr("DEVICE_RELAY_KEY_TTL", "8760h"), "device key lifetime")
	dataDir := flag.String("data-dir", envOr("DEVICE_RELAY_DATA_DIR", "./data"), "directory for persisted state")
	tlsCert := flag.String("tls-cert", os.Getenv("DEVICE_RELAY_TLS_CERT"), "optional TLS certificate file")
	tlsKey := flag.String("tls-key", os.Getenv("DEVICE_RELAY_TLS_KEY"), "optional TLS private key file")
	flag.Parse()
	parsedTTL, err := time.ParseDuration(*keyTTL)
	if err != nil {
		log.Fatalf("parse key-ttl: %v", err)
	}

	if *adminPassword == "admin" || *adminToken == "admin" {
		log.Printf("WARNING: default admin credentials are in use; set DEVICE_RELAY_ADMIN_PASSWORD and DEVICE_RELAY_ADMIN_TOKEN before exposing the console")
	}

	web, err := fs.Sub(relay.WebFS, "web/dist")
	if err != nil {
		log.Fatalf("embedded web ui unavailable: %v", err)
	}
	srv, err := server.NewServer(server.Config{
		AdminToken:    *adminToken,
		DataDir:       *dataDir,
		WebFS:         web,
		AdminUser:     *adminUser,
		AdminPassword: *adminPassword,
		AgentListen:   *altHTTPSListen,
		KeyTTL:        parsedTTL,
	})
	if err != nil {
		log.Fatalf("create server: %v", err)
	}
	handler := srv.WebHandler()
	agentHandler := srv.AgentHandler()

	switch {
	case *domain != "":
		runCertMagic(handler, agentHandler, *domain, *acmeEmail, *dataDir, *httpsListen, *altHTTPSListen, *httpListen, *adminToken)
	case *tlsCert != "" && *tlsKey != "":
		cert, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
		if err != nil {
			log.Fatalf("load tls certificate: %v", err)
		}
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		httpSrv := &http.Server{Addr: *httpsListen, Handler: handler, TLSConfig: tlsConfig}
		if *altHTTPSListen != "" {
			altSrv := &http.Server{Addr: *altHTTPSListen, Handler: agentHandler, TLSConfig: tlsConfig}
			go func() {
				log.Printf("warpmesh agent plane serving HTTPS on %s", *altHTTPSListen)
				log.Fatal(altSrv.ListenAndServeTLS("", ""))
			}()
		}
		log.Printf("warpmesh web plane serving HTTPS on %s", *httpsListen)
		log.Fatal(httpSrv.ListenAndServeTLS("", ""))
	default:
		log.Printf("warpmesh serving HTTP on %s (enable -domain or -tls-cert/-tls-key for HTTPS)", *listen)
		log.Printf("web ui: http://%s/ (admin token: %s)", displayHost(*listen), *adminToken)
		httpSrv := &http.Server{Addr: *listen, Handler: handler}
		log.Fatal(httpSrv.ListenAndServe())
	}
}

func runCertMagic(webHandler, agentHandler http.Handler, domain, acmeEmail, dataDir, httpsListen, altHTTPSListen, httpListen, adminToken string) {
	certDir := filepath.Join(dataDir, "certs")
	magic := certmagic.NewDefault()
	magic.Storage = &certmagic.FileStorage{Path: certDir}
	issuer := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
		Email:  acmeEmail,
		Agreed: true,
		CA:     certmagic.LetsEncryptProductionCA,
	})
	magic.Issuers = []certmagic.Issuer{issuer}
	ctx := context.Background()
	if err := magic.ManageSync(ctx, []string{domain}); err != nil {
		log.Fatalf("manage certificates: %v", err)
	}
	tlsConfig := magic.TLSConfig()
	tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)

	httpsSrv := &http.Server{
		Addr:      httpsListen,
		Handler:   webHandler,
		TLSConfig: tlsConfig,
	}
	if altHTTPSListen != "" {
		altSrv := &http.Server{
			Addr:      altHTTPSListen,
			Handler:   agentHandler,
			TLSConfig: tlsConfig,
		}
		go func() {
			log.Printf("warpmesh agent plane serving HTTPS on %s for %s", altHTTPSListen, domain)
			log.Fatal(altSrv.ListenAndServeTLS("", ""))
		}()
	}
	go func() {
		challengeHandler := issuer.HTTPChallengeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
		}))
		log.Printf("ACME/redirect server on %s for %s", httpListen, domain)
		challengeSrv := &http.Server{Addr: httpListen, Handler: challengeHandler}
		log.Fatal(challengeSrv.ListenAndServe())
	}()

	log.Printf("warpmesh web plane serving HTTPS on %s for %s", httpsListen, domain)
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
