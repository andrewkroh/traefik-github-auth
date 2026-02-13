// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package main implements a simple echo HTTP server for integration testing.
// It returns the received request headers, method, and path as JSON, allowing
// tests to verify that Traefik's ForwardAuth middleware correctly forwards
// authentication headers to the upstream service.
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

// echoResponse is the JSON structure returned by the echo server.
type echoResponse struct {
	Headers map[string][]string `json:"headers"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleEcho)

	log.Println("echo server listening on :8081")
	if err := http.ListenAndServe(":8081", mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// handleEcho returns all request headers, method, and path as JSON.
func handleEcho(w http.ResponseWriter, r *http.Request) {
	resp := echoResponse{
		Headers: r.Header,
		Method:  r.Method,
		Path:    r.URL.Path,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
