// A real SQL/cache consumer used to exercise seeded local components.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		client := http.Client{Timeout: time.Second}
		r, err := client.Get("http://127.0.0.1:8080/health")
		if err != nil {
			os.Exit(1)
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	cfg := mysql.NewConfig()
	cfg.Net, cfg.Addr, cfg.User, cfg.DBName = "tcp", os.Getenv("MYSQL_ADDRESS"), "root", "localmock"
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 3*time.Second, 3*time.Second, 3*time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	cache := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDRESS"), MaxRetries: -1, DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	defer cache.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	var product string
	if err = db.QueryRowContext(ctx, "SELECT name FROM products WHERE id = 1").Scan(&product); err != nil {
		log.Fatal("missing SQL fixture: ", err)
	}
	seed, err := cache.Get(ctx, "fixture:greeting").Result()
	if err != nil {
		log.Fatal("missing cache fixture: ", err)
	}
	cancel()
	log.Print("fixtures ready; accepting requests")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ready") })
	// This demo GET deliberately writes data to exercise the current GET probe.
	mux.HandleFunc("GET /exercise", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		fail := func(err error) {
			log.Printf("exercise failed: %v", err)
			http.Error(w, "datastore operation failed", http.StatusInternalServerError)
		}
		result, err := db.ExecContext(ctx, "INSERT INTO orders (product_id) VALUES (?)", 1)
		if err != nil {
			fail(err)
			return
		}
		id, err := result.LastInsertId()
		if err != nil {
			fail(err)
			return
		}
		var stored int64
		if err = db.QueryRowContext(ctx, "SELECT product_id FROM orders WHERE id = ?", id).Scan(&stored); err != nil {
			fail(err)
			return
		}
		key := fmt.Sprintf("order:%d", id)
		if err = cache.Set(ctx, key, product, time.Minute).Err(); err != nil {
			fail(err)
			return
		}
		value, err := cache.Get(ctx, key).Result()
		if err != nil {
			fail(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"order_id": id, "product_id": stored, "product": product, "cached_product": value, "greeting": seed})
	})
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServe())
}
