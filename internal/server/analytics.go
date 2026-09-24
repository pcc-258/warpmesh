package server

import (
	"net/http"
	"strings"
	"time"
)

type analyticsBucket struct {
	StartedAt       time.Time `json:"startedAt"`
	BytesToDevice   int64     `json:"bytesToDevice"`
	BytesFromDevice int64     `json:"bytesFromDevice"`
}

type analyticsResponse struct {
	Period         string             `json:"period"`
	Sessions       int                `json:"sessions"`
	Active         int                `json:"active"`
	DirectSessions int                `json:"directSessions"`
	RelaySessions  int                `json:"relaySessions"`
	DirectTunnels  int                `json:"directTunnels"`
	RelayTunnels   int                `json:"relayTunnels"`
	RelayBytes     int64              `json:"relayBytes"`
	Buckets        []analyticsBucket  `json:"buckets"`
	Recent         []ConnectionRecord `json:"recent"`
}

func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	period := r.URL.Query().Get("range")
	if period != "7d" {
		period = "24h"
	}
	now := time.Now().UTC()
	var graphStart time.Time
	var bucketCount int
	var bucketWidth time.Duration
	if period == "7d" {
		graphStart = now.Truncate(24*time.Hour).AddDate(0, 0, -6)
		bucketCount = 7
		bucketWidth = 24 * time.Hour
	} else {
		graphStart = now.Truncate(time.Hour).Add(-23 * time.Hour)
		bucketCount = 24
		bucketWidth = time.Hour
	}
	connections := s.reg.ListConnections(graphStart, 5000)
	traffic := s.reg.ListConnectionTraffic(graphStart, 50000)
	allowed := map[string]bool(nil)
	if !s.isAdmin(actor) {
		user, found := s.actorInfo(actor)
		allowed = make(map[string]bool)
		if found {
			for _, id := range user.DeviceIDs {
				allowed[id] = true
			}
		}
	}
	visible := func(deviceID string) bool {
		return allowed == nil || allowed[deviceID]
	}
	result := analyticsResponse{
		Period:  period,
		Buckets: make([]analyticsBucket, bucketCount),
		Recent:  make([]ConnectionRecord, 0, 25),
	}
	for i := range result.Buckets {
		result.Buckets[i].StartedAt = graphStart.Add(time.Duration(i) * bucketWidth)
	}
	for _, connection := range connections {
		if !visible(connection.DeviceID) {
			continue
		}
		result.Sessions++
		if connection.State == "active" || connection.State == "pending" {
			result.Active++
		}
		switch connection.Transport {
		case "direct":
			result.DirectSessions++
		case "relay":
			result.RelaySessions++
		}
		if strings.HasPrefix(connection.Service, "tcp:") {
			switch connection.Transport {
			case "direct":
				result.DirectTunnels++
			case "relay":
				result.RelayTunnels++
			}
		}
		if len(result.Recent) < 25 {
			if allowed != nil && connection.PeerDeviceID != "" && !visible(connection.PeerDeviceID) {
				connection.PeerDeviceID = ""
				connection.PeerDeviceName = ""
			}
			result.Recent = append(result.Recent, connection)
		}
	}
	for _, sample := range traffic {
		if !visible(sample.DeviceID) {
			continue
		}
		offset := int(sample.BucketStart.Sub(graphStart) / bucketWidth)
		if offset < 0 || offset >= len(result.Buckets) {
			continue
		}
		result.Buckets[offset].BytesToDevice += sample.BytesToDevice
		result.Buckets[offset].BytesFromDevice += sample.BytesFromDevice
		result.RelayBytes += sample.BytesToDevice + sample.BytesFromDevice
	}
	writeJSON(w, http.StatusOK, result)
}
