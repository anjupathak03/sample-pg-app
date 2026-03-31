package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// startupConfig holds data fetched once at startup from an external HTTPS API.
// This is the "init traffic" — recorded as mocks for every pod.
// If the app is NOT restarted between test-set replays, these mocks
// won't be consumed again → RemoveUnusedMocks deletes them.
var startupConfig struct {
	Posts []map[string]interface{}
}

// in-memory store (no DB needed)
var (
	users   []map[string]interface{}
	usersMu sync.Mutex
	nextID  int
)

func main() {
	// ----------------------------------------------------------------
	// STARTUP HTTPS CALLS — these become "initial mocks":
	//   DNS resolve → TLS handshake → HTTP GET → response
	// Each pod records this on startup. If the app is NOT restarted
	// between test-set replays, these mocks won't be consumed again.
	// ----------------------------------------------------------------
	log.Println("fetching startup config from external API...")

	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get("https://jsonplaceholder.typicode.com/posts?_limit=3")
	if err != nil {
		log.Fatalf("startup config fetch failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		log.Fatalf("startup config read failed: %v", err)
	}
	if err := json.Unmarshal(body, &startupConfig.Posts); err != nil {
		log.Fatalf("startup config parse failed: %v", err)
	}
	log.Printf("startup config loaded: %d posts", len(startupConfig.Posts))

	// Second startup call — more init traffic to make the bug obvious
	resp2, err := client.Get("https://jsonplaceholder.typicode.com/users?_limit=2")
	if err != nil {
		log.Fatalf("startup users fetch failed: %v", err)
	}
	resp2.Body.Close()
	log.Println("startup users fetched")

	// ----------------------------------------------------------------
	// HTTP endpoints — each request makes an external HTTPS call
	// that WILL be consumed during replay (no issue with these).
	// ----------------------------------------------------------------

	nextID = 1
	http.HandleFunc("/healthz", handleHealthz)
	http.HandleFunc("/users", handleListUsers)
	http.HandleFunc("/users/create", handleCreateUser)
	http.HandleFunc("/config", handleConfig)

	port := "8080"
	log.Printf("listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// handleHealthz — lightweight liveness check.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

// handleConfig — returns the startup config (no external call).
func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(startupConfig.Posts)
}

// handleListUsers — returns in-memory users + makes external call for enrichment.
func handleListUsers(w http.ResponseWriter, r *http.Request) {
	// Per-request external call — produces a mock that WILL be consumed
	resp, err := http.Get("https://jsonplaceholder.typicode.com/posts/1")
	if err != nil {
		http.Error(w, "external call failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp.Body.Close()

	usersMu.Lock()
	result := make([]map[string]interface{}, len(users))
	copy(result, users)
	usersMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleCreateUser — adds a user to the in-memory store.
func handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Email == "" {
		http.Error(w, "name and email required", http.StatusBadRequest)
		return
	}

	// Per-request external call — mock will be consumed during replay
	resp, err := http.Get("https://jsonplaceholder.typicode.com/posts/2")
	if err != nil {
		http.Error(w, "external call failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp.Body.Close()

	usersMu.Lock()
	user := map[string]interface{}{
		"id":    nextID,
		"name":  body.Name,
		"email": body.Email,
	}
	nextID++
	users = append(users, user)
	usersMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}
