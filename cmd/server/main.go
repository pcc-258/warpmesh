package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/caddyserver/certmagic"

	relay "github.com/pcc-258/warpmesh"
	"github.com/pcc-258/warpmesh/internal/server"
)

// HTTP timeouts. ReadHeaderTimeout is the slowloris guard; the other values are
// deliberately generous because the console and the agent plane both carry
// long-lived WebSocket connections alongside ordinary requests.
const (
	readHeaderTimeout = 15 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 15 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("warpmesh: %v", err)
	}
}

func run() error {
	listen := flag.String("listen", envOr("DEVICE_RELAY_LISTEN", ":8080"), "HTTP listen address (plain HTTP mode)")
	httpsListen := flag.String("https-listen", envOr("DEVICE_RELAY_HTTPS_LISTEN", ":443"), "primary HTTPS listen address")
	altHTTPSListen := flag.String("alt-https-listen", os.Getenv("DEVICE_RELAY_ALT_HTTPS_LISTEN"), "optional secondary HTTPS listen address")
	httpListen := flag.String("http-listen", envOr("DEVICE_RELAY_HTTP_LISTEN", ":80"), "HTTP listen address for ACME challenges and redirects")
	domain := flag.String("domain", os.Getenv("DEVICE_RELAY_DOMAIN"), "public domain; enables automatic Let's Encrypt certificates")
	acmeEmail := flag.String("acme-email", os.Getenv("DEVICE_RELAY_ACME_EMAIL"), "contact email used for Let's Encrypt")
	adminToken := flag.String("admin-token", os.Getenv("DEVICE_RELAY_ADMIN_TOKEN"), "admin token for the web UI; required")
	adminUser := flag.String("admin-user", envOr("DEVICE_RELAY_ADMIN_USER", "admin"), "initial console username")
	adminPassword := flag.String("admin-password", os.Getenv("DEVICE_RELAY_ADMIN_PASSWORD"), "initial console password; required")
	keyTTL := flag.String("key-ttl", envOr("DEVICE_RELAY_KEY_TTL", "8760h"), "device key lifetime")
	dataDir := flag.String("data-dir", envOr("DEVICE_RELAY_DATA_DIR", "./data"), "directory for persisted state")
	tlsCert := flag.String("tls-cert", os.Getenv("DEVICE_RELAY_TLS_CERT"), "optional TLS certificate file")
	tlsKey := flag.String("tls-key", os.Getenv("DEVICE_RELAY_TLS_KEY"), "optional TLS private key file")
	flag.Parse()
	parsedTTL, err := time.ParseDuration(*keyTTL)
	if err != nil {
		return fmt.Errorf("parse key-ttl: %w", err)
	}

	if strings.TrimSpace(*adminToken) == "" {
		return errors.New("DEVICE_RELAY_ADMIN_TOKEN is required: refusing to start with an empty admin token")
	}
	if *adminToken == "admin" {
		return errors.New(`the default admin token "admin" is not allowed: set DEVICE_RELAY_ADMIN_TOKEN to a strong random value`)
	}
	if *adminPassword == "admin" {
		return errors.New(`the default admin password "admin" is not allowed: set DEVICE_RELAY_ADMIN_PASSWORD to a strong value`)
	}

	web, err := fs.Sub(relay.WebFS, "web/dist")
	if err != nil {
		return fmt.Errorf("embedded web ui unavailable: %w", err)
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
		return fmt.Errorf("create server: %w", err)
	}
	handler := srv.WebHandler()
	agentHandler := srv.AgentHandler()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer func() { _ = srv.Close() }()

	var servers []*http.Server
	var starters []tlsServer
	switch {
	case *domain != "":
		starters, servers, err = runCertMagic(ctx, handler, agentHandler, *domain, *acmeEmail, *dataDir, *httpsListen, *altHTTPSListen, *httpListen)
		if err != nil {
			return err
		}
	case *tlsCert != "" && *tlsKey != "":
		cert, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
		if err != nil {
			return fmt.Errorf("load tls certificate: %w", err)
		}
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		webSrv := newHTTPServer(*httpsListen, handler, *domain)
		webSrv.TLSConfig = tlsConfig
		starters = append(starters, tlsServer{server: webSrv, tls: true})
		servers = append(servers, webSrv)
		if *altHTTPSListen != "" {
			agentSrv := newHTTPServer(*altHTTPSListen, agentHandler, *domain)
			agentSrv.TLSConfig = tlsConfig
			starters = append(starters, tlsServer{server: agentSrv, tls: true})
			servers = append(servers, agentSrv)
		}
		log.Printf("warpmesh web plane serving HTTPS on %s", *httpsListen)
	default:
		log.Printf("warpmesh serving HTTP on %s (enable -domain or -tls-cert/-tls-key for HTTPS)", *listen)
		log.Printf("web ui: http://%s/", displayHost(*listen))
		plain := newHTTPServer(*listen, handler, "")
		starters = append(starters, tlsServer{server: plain, tls: false})
		servers = append(servers, plain)
	}

	serveAll(ctx, starters)
	shutdownServers(servers)
	return nil
}

// newHTTPServer builds a listener with the shared timeout policy and a
// noise-filtering error logger.
func newHTTPServer(addr string, handler http.Handler, domain string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          newFilteredErrorLog(domain),
	}
}

// newFilteredErrorLog drops the one class of net/http error that a public
// listener cannot avoid: TLS handshakes for names this server does not serve.
//
// An internet-facing IP is probed constantly (random SNI hostnames, bare-IP
// handshakes), and each probe otherwise produces a line. On the reference
// deployment these accounted for roughly a third of all log output and buried
// the actual relay events, while conveying nothing an operator can act on.
//
// The filter is deliberately narrow: an unknown-name handshake is only dropped
// when the offered SNI does not share a registrable domain with the configured
// one. A failure for our own domain is always reported, because that is what a
// certificate problem looks like.
func newFilteredErrorLog(domain string) *log.Logger {
	return log.New(&noiseFilteringWriter{w: os.Stderr, domain: domain}, "", log.LstdFlags)
}

type noiseFilteringWriter struct {
	w      io.Writer
	domain string
}

func (n *noiseFilteringWriter) Write(p []byte) (int, error) {
	if n.suppress(string(p)) {
		// Report the byte count so callers do not treat it as a failure.
		return len(p), nil
	}
	return n.w.Write(p)
}

// suppress reports whether a net/http error line is scanner noise.
func (n *noiseFilteringWriter) suppress(line string) bool {
	if !strings.Contains(line, "TLS handshake error") {
		return false
	}
	// Probes that speak a protocol this server cannot answer, or speak plain
	// HTTP to the TLS port. Nothing here is actionable by an operator: the
	// client is simply too old or too broken to complete a handshake.
	for _, noise := range []string{
		"client sent an HTTP request to an HTTPS server",
		"client offered only unsupported versions",
		"unsupported SSLv2 handshake received",
		"remote error: tls: protocol version not supported",
		"remote error: tls: no cipher suite supported",
	} {
		if strings.Contains(line, noise) {
			return true
		}
	}
	const marker = "no certificate available for '"
	idx := strings.Index(line, marker)
	if idx < 0 {
		return false
	}
	sni := line[idx+len(marker):]
	if end := strings.IndexByte(sni, '\''); end >= 0 {
		sni = sni[:end]
	}
	if sni == "" {
		return false
	}
	// Our own certificate failing to serve is a real incident, never noise.
	ours := registrableDomain(n.domain)
	if ours == "" || registrableDomain(sni) == ours {
		return false
	}
	return true
}

// registrableDomain returns the last two labels of a hostname. It deliberately
// ignores public-suffix rules: that is enough to decide whether a probe targets
// our namespace, and it errs toward reporting rather than hiding.
func registrableDomain(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return ""
	}
	return strings.Join(labels[len(labels)-2:], ".")
}

// serveAll runs every listener until the context is cancelled or one of them
// fails for a reason other than an orderly shutdown. Each entry carries its own
// mode because the ACME challenge listener must stay plain HTTP while the web
// and agent planes terminate TLS.
func serveAll(ctx context.Context, servers []tlsServer) {
	errCh := make(chan error, len(servers))
	for _, entry := range servers {
		entry := entry
		go func() {
			var err error
			if entry.tls {
				err = entry.server.ListenAndServeTLS("", "")
			} else {
				err = entry.server.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}
	select {
	case <-ctx.Done():
		log.Printf("shutdown signal received, draining connections")
	case err := <-errCh:
		log.Printf("server stopped: %v", err)
	}
}

// tlsServer marks whether a listener terminates TLS.
type tlsServer struct {
	server *http.Server
	tls    bool
}

func shutdownServers(servers []*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown of %s: %v", srv.Addr, err)
			_ = srv.Close()
		}
	}
	log.Printf("warpmesh stopped")
}

func runCertMagic(ctx context.Context, webHandler, agentHandler http.Handler, domain, acmeEmail, dataDir, httpsListen, altHTTPSListen, httpListen string) ([]tlsServer, []*http.Server, error) {
	certDir := filepath.Join(dataDir, "certs")
	magic := certmagic.NewDefault()
	magic.Storage = &certmagic.FileStorage{Path: certDir}
	issuer := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
		Email:  acmeEmail,
		Agreed: true,
		CA:     certmagic.LetsEncryptProductionCA,
	})
	magic.Issuers = []certmagic.Issuer{issuer}
	if err := magic.ManageSync(ctx, []string{domain}); err != nil {
		return nil, nil, fmt.Errorf("manage certificates: %w", err)
	}
	tlsConfig := magic.TLSConfig()
	tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)

	webSrv := newHTTPServer(httpsListen, webHandler, domain)
	webSrv.TLSConfig = tlsConfig
	starters := []tlsServer{{server: webSrv, tls: true}}
	all := []*http.Server{webSrv}
	if altHTTPSListen != "" {
		agentSrv := newHTTPServer(altHTTPSListen, agentHandler, domain)
		agentSrv.TLSConfig = tlsConfig
		starters = append(starters, tlsServer{server: agentSrv, tls: true})
		all = append(all, agentSrv)
		log.Printf("warpmesh agent plane serving HTTPS on %s for %s", altHTTPSListen, domain)
	}

	// The ACME challenge and redirect listener must stay plain HTTP: ACME
	// HTTP-01 has to be reachable on port 80 without TLS, so it is started in
	// HTTP mode rather than through the TLS listener path.
	challengeHandler := issuer.HTTPChallengeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
	}))
	challengeSrv := newHTTPServer(httpListen, challengeHandler, "")
	starters = append(starters, tlsServer{server: challengeSrv, tls: false})
	all = append(all, challengeSrv)
	log.Printf("ACME/redirect server on %s for %s", httpListen, domain)

	log.Printf("warpmesh web plane serving HTTPS on %s for %s", httpsListen, domain)
	log.Printf("web ui: https://%s/", domain)
	return starters, all, nil
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
