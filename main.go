package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"text/template"
	"time"

	"atomirex.com/umbrella/razor"
	"atomirex.com/umbrella/sfu"
	"github.com/atomirex/mdns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"google.golang.org/protobuf/proto"
)

// nolint
var ()

//go:embed frontend/dist/static/*
var staticFiles embed.FS

//go:embed frontend/dist/templates/*
var templateFiles embed.FS

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rec *responseRecorder) WriteHeader(code int) {
	rec.statusCode = code
	rec.ResponseWriter.WriteHeader(code)
}

// Implement http.Hijacker for websocket
func (rr *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := rr.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
	}
	return hijacker.Hijack()
}

type umbrellaInjectedParameters struct {
	HttpPrefix string
}

func main() {
	cloudEnv := os.Getenv("UMBRELLA_CLOUD")
	isCloud := cloudEnv == "1"

	// Path from the domain base, to the umbrella sfu base, including any leading slash
	// For example "/umbrella" serves at https://domain:port/umbrella/sfu etc.
	// This is the same form traefik uses for PathPrefix, so you can use a common env var
	httpPrefixEnv := os.Getenv("UMBRELLA_HTTP_PREFIX")
	httpPrefix := ""

	if httpPrefixEnv != "" {
		httpPrefix = httpPrefixEnv
	}

	publicIpEnv := os.Getenv("UMBRELLA_PUBLIC_IP")
	publicIp := net.ParseIP(publicIpEnv)

	publicHostEnv := os.Getenv("UMBRELLA_PUBLIC_HOST")

	publicMinPortEnv := os.Getenv("UMBRELLA_MIN_PORT")
	publicMaxPortEnv := os.Getenv("UMBRELLA_MAX_PORT")

	minPort := uint16(40000)
	maxPort := uint16(60000)

	if publicMinPortEnv != "" {
		p, err := strconv.Atoi(publicMinPortEnv)
		if err == nil {
			minPort = uint16(p)
		}
	}

	if publicMaxPortEnv != "" {
		p, err := strconv.Atoi(publicMaxPortEnv)
		if err == nil {
			maxPort = uint16(p)
		}
	}

	httpServeAddrEnv := os.Getenv("UMBRELLA_HTTP_SERVE_ADDR")

	// Default is to serve on 8081
	httpServeAddr := ":8081"
	if httpServeAddrEnv != "" {
		httpServeAddr = httpServeAddrEnv
	}

	log.Println("Hello there", runtime.GOOS, runtime.GOARCH)
	if isCloud {
		log.Println("Running in cloud configuration")

		if publicIp == nil {
			log.Println("WARNING: public IP must be set when running as cloud")
		} else {
			log.Println("Public IP is", publicIp.String())
		}
	} else {
		log.Println("Running in edge configuration")
	}

	debug.SetMemoryLimit(256 * 1024 * 1024)

	host, err := os.Hostname()
	if err != nil {
		log.Fatal(err)
		return
	}

	if !strings.HasSuffix(host, ".local") {
		host = host + ".local"
	}

	if isCloud && publicHostEnv != "" {
		host = publicHostEnv
	} else {
		overriddenhost, overriddenport, err := net.SplitHostPort(httpServeAddr)
		if err != nil {
			host += ":8081"
		} else {
			usehost := host
			useport := "8081"

			if overriddenhost != "" {
				usehost = overriddenhost
			}

			if overriddenport != "" {
				useport = overriddenport
			}

			host = net.JoinHostPort(usehost, useport)
		}
	}

	log.Println("Starting server at", fmt.Sprintf("https://%v%s/", strings.ToLower(host), httpPrefix))

	templates, err := template.ParseFS(templateFiles, "frontend/dist/templates/*.html")
	if err != nil {
		log.Fatal(err)
		return
	}

	staticFilesSub, err := fs.Sub(staticFiles, "frontend/dist/static")
	if err != nil {
		log.Fatalf("failed to create file system: %v", err)
	}

	var ipStr *string
	if publicIp != nil {
		s := publicIp.String()
		ipStr = &s
	}

	logger := razor.NewLogger(razor.LogLevelError, false)
	s := sfu.NewSfu(logger, minPort, maxPort, ipStr)

	mux := http.NewServeMux()

	addHandler := func(pattern string, handler http.Handler) {
		if httpPrefix == "" {
			mux.Handle(pattern, handler)
		} else {
			mux.Handle(httpPrefix+pattern, http.StripPrefix(httpPrefix, handler))
		}
	}

	addHandler("/wsb", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.WebsocketHandler(w, r)
	}))

	addHandler("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFilesSub))))

	generic := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			contentType := r.Header.Get("Content-Type")
			if contentType == "application/x-protobuf" {
				status := s.GetStatus()

				log.Println("SFU STATUS", status)

				data, err := proto.Marshal(status)
				if err != nil {
					http.Error(w, "Failed to serialize Protobuf", http.StatusInternalServerError)
					return
				}

				w.Header().Set("Content-Type", contentType)
				w.WriteHeader(http.StatusOK)

				w.Write(data)
				return
			}
		}

		if r.URL.Path == "/servers" {
			contentType := r.Header.Get("Content-Type")
			if contentType == "application/x-protobuf" {
				switch r.Method {
				case http.MethodGet:
					data, err := proto.Marshal(s.GetCurrentServers())
					if err != nil {
						http.Error(w, "Failed to serialize Protobuf", http.StatusInternalServerError)
						return
					}

					w.Header().Set("Content-Type", contentType)
					w.WriteHeader(http.StatusOK)

					w.Write(data)
					return
				case http.MethodPost:
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, "Failed to read payload", http.StatusInternalServerError)
						return
					}

					var update sfu.CurrentServers
					err = proto.Unmarshal(body, &update)
					if err != nil {
						http.Error(w, "Failed to deserialize payload", http.StatusInternalServerError)
						return
					}

					data, err := proto.Marshal(s.SetCurrentServers(&update))
					if err != nil {
						http.Error(w, "Failed to serialize Protobuf", http.StatusInternalServerError)
						return
					}

					w.Header().Set("Content-Type", contentType)
					w.WriteHeader(http.StatusOK)

					w.Write(data)
					return
				}
			}
		}

		injectedBytes, err := json.Marshal(&umbrellaInjectedParameters{
			HttpPrefix: httpPrefix,
		})

		if err != nil {
			http.Error(w, "Error preparing template", http.StatusInternalServerError)
			return
		}

		data := map[string]string{
			"Title":      "SFU test page",
			"HttpPrefix": httpPrefix,
			"Injected":   string(injectedBytes),
		}

		if err := templates.ExecuteTemplate(w, "generic.html", data); err != nil {
			http.Error(w, "Error rendering template", http.StatusInternalServerError)
			log.Println("Error executing template:", err)
		}
	})

	addHandler("/sfu", generic)
	addHandler("/servers", generic)
	addHandler("/status", generic)

	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			log.Println("Request handling complete for", r.URL.Path)
		}()

		log.Println("Req ", r.URL.Path)

		rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		mux.ServeHTTP(rec, r)

		if rec.statusCode == http.StatusNotFound {
			log.Println("Requesting unfound url", r.URL.Path)
		}
	})

	if isCloud {
		go func() {
			err = http.ListenAndServe(httpServeAddr, wrapped)
			if err != nil {
				log.Fatal(err)
			}
		}()
	} else {
		go func() {
			err = http.ListenAndServeTLS(httpServeAddr, "service.crt", "service.key", wrapped)
			if err != nil {
				log.Fatal(err)
			}
		}()

		// Use atomirex fork of the pion mdns which works with android clients
		go func() {
			addr4, err := net.ResolveUDPAddr("udp4", mdns.DefaultAddressIPv4)
			if err != nil {
				panic(err)
			}

			addr6, err := net.ResolveUDPAddr("udp6", mdns.DefaultAddressIPv6)
			if err != nil {
				panic(err)
			}

			l4, err := net.ListenUDP("udp4", addr4)
			if err != nil {
				panic(err)
			}

			l6, err := net.ListenUDP("udp6", addr6)
			if err != nil {
				panic(err)
			}

			hostname, _ := os.Hostname()
			hostname = strings.TrimSuffix(strings.ToLower(hostname), ".")
			hostname = strings.TrimSuffix(hostname, ".local")
			hostname = hostname + ".local"

			log.Println("Broadcasting hostname via mdns", hostname)

			mdnsConn, err := mdns.Server(ipv4.NewPacketConn(l4), ipv6.NewPacketConn(l6), &mdns.Config{
				LocalNames: []mdns.RegisteredHost{{Name: hostname}},
			})
			if err != nil {
				panic(err)
			}

			s.SetMdnsConn(mdnsConn)
			select {}
		}()
	}

	go func() {
		for {
			<-time.After(5 * time.Second)
			runtime.GC()
		}
	}()

	razor.WaitForOsInterruptSignal()

}
