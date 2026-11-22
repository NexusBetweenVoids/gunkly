// Command webhook is a tiny capture sink for staging: it accepts any request
// on any path, logs it and keeps the last 100 for inspection at GET /_requests.
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type captured struct {
	At     time.Time           `json:"at"`
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Header map[string][]string `json:"header"`
	Body   string              `json:"body"`
}

func main() {
	var (
		mu   sync.Mutex
		last []captured
	)
	http.HandleFunc("/_requests", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(last)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		c := captured{time.Now().UTC(), r.Method, r.URL.RequestURI(), r.Header, string(body)}
		log.Printf("%s %s (%d bytes)", c.Method, c.Path, len(body))
		mu.Lock()
		if last = append(last, c); len(last) > 100 {
			last = last[1:]
		}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	log.Fatal((&http.Server{Addr: ":8080", ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}
