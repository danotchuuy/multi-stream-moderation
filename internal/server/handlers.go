package server

import (
	"encoding/json"
	"net/http"
)

type healthResponse struct {
	Status string `json:"status"`
	// Version identifies this running process (see Server.instanceID) —
	// a fresh random value each time the server starts, so the frontend
	// can tell a redeploy (process restarted) apart from a merely brief
	// network blip and reload itself to pick up the new build.
	Version string `json:"version"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: s.instanceID})
}
