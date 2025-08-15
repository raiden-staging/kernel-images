package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"kernel-operator-api/internal/routes/clipboard"
	"kernel-operator-api/internal/routes/fs"
	"kernel-operator-api/internal/routes/health"
	"kernel-operator-api/internal/routes/input"
	"kernel-operator-api/internal/routes/logs"
	"kernel-operator-api/internal/routes/metrics"
	"kernel-operator-api/internal/routes/network"
	"kernel-operator-api/internal/routes/oslocale"
	"kernel-operator-api/internal/routes/proc"
	"kernel-operator-api/internal/routes/recording"
	"kernel-operator-api/internal/routes/screenshot"
	"kernel-operator-api/internal/routes/scripts"
	"kernel-operator-api/internal/routes/stream"
	"kernel-operator-api/internal/utils/env"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*, Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, req)
		dur := time.Since(start).Milliseconds()
		log.Printf("%s %s -> %d %dms", req.Method, req.URL.Path, rec.status, dur)
	})
}

func main() {
	env.EnsureDirs()

	// Print env
	log.Println("🔧 [kernel:operator-api:debug] process.env 🔧")
	log.Println("┌─────────────────────────────────────────────────────")
	for _, k := range env.SortedEnvKeys() {
		log.Printf("│ %-25s │ %s", k, os.Getenv(k))
	}
	log.Println("└─────────────────────────────────────────────────────")

	mux := http.NewServeMux()

	health.Register(mux)
	fs.Register(mux)
	screenshot.Register(mux)
	stream.Register(mux)
	input.Register(mux)
	proc.Register(mux)
	network.Register(mux)
	logs.Register(mux)
	clipboard.Register(mux)
	metrics.Register(mux)
	recording.Register(mux)
	scripts.Register(mux)
	oslocale.Register(mux)

	portStr := os.Getenv("PORT")
	if portStr == "" {
		portStr = "10001"
	}
	port, _ := strconv.Atoi(portStr)
	addr := fmt.Sprintf(":%d", port)

	log.Printf("Kernel Computer Operator API listening on %s", addr)
	s := &http.Server{
		Addr:         addr,
		Handler:      cors(logger(mux)),
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Fatal(s.ListenAndServe())
}