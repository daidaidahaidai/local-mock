// Standalone fixture: a real forwarding service and an interchangeable mock.
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	client := &http.Client{Timeout: 3 * time.Second}
	if len(os.Args) > 1 && os.Args[1] == "health" {
		r, err := client.Get("http://127.0.0.1:8080/health")
		if err != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		log.Printf("request method=%s path=%s", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if backend := os.Getenv("BACKEND_URL"); backend != "" {
			resp, err := client.Get(backend + "/")
			if err != nil {
				http.Error(w, err.Error(), 502)
				return
			}
			defer resp.Body.Close()
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"message": os.Getenv("MOCK_RESPONSE"), "implementation": "mock"})
	})
	log.Fatal((&http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}
