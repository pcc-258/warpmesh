package main

import (
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	relay "github.com/pcc-258/device-relay"
	"github.com/pcc-258/device-relay/internal/server"
)

func main() {
	listen := flag.String("listen", envOr("DEVICE_RELAY_LISTEN", ":8080"), "listen address")
	adminToken := flag.String("admin-token", envOr("DEVICE_RELAY_ADMIN_TOKEN", "admin"), "admin token for the web UI")
	deviceToken := flag.String("device-token", envOr("DEVICE_RELAY_DEVICE_TOKEN", "device"), "shared token used by device agents")
	dataDir := flag.String("data-dir", envOr("DEVICE_RELAY_DATA_DIR", "./data"), "directory for persisted state")
	tlsCert := flag.String("tls-cert", os.Getenv("DEVICE_RELAY_TLS_CERT"), "optional TLS certificate")
	tlsKey := flag.String("tls-key", os.Getenv("DEVICE_RELAY_TLS_KEY"), "optional TLS private key")
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

	log.Printf("device relay listening on %s", *listen)
	log.Printf("web ui: http://%s/ (admin token: %s)", displayHost(*listen), *adminToken)
	httpSrv := &http.Server{
		Addr:    *listen,
		Handler: srv.Handler(),
	}
	if *tlsCert != "" && *tlsKey != "" {
		log.Fatal(httpSrv.ListenAndServeTLS(*tlsCert, *tlsKey))
	}
	log.Fatal(httpSrv.ListenAndServe())
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
