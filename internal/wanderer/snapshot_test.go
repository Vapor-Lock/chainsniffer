package wanderer_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Vapor-Lock/chainsniffer/internal/wanderer"
)

// snapshotFixture is shaped like MapSystemAPIController.index: systems from
// APIUtils.map_system_to_json and connections from APIUtils.connection_to_json.
const snapshotFixture = `{"data":{
	"systems":[
		{"id":"5b1d","map_id":"f1b0","solar_system_id":31002604,"name":"J142845","original_name":"J142845","custom_name":null,"temporary_name":null,"description":null,"tag":null,"labels":null,"locked":false,"visible":true,"status":0,"position_x":0,"position_y":0,"inserted_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:00:00Z"},
		{"id":"5b1e","map_id":"f1b0","solar_system_id":30000142,"name":"Jita","original_name":"Jita","visible":true},
		{"id":"5b1f","map_id":"f1b0","solar_system_id":31000005,"name":"Thera","original_name":"Thera","visible":true}
	],
	"connections":[
		{"id":"c1","map_id":"f1b0","solar_system_source":31002604,"solar_system_target":30000142,"mass_status":0,"time_status":0,"ship_size_type":2,"type":0,"wormhole_type":"K162","locked":false,"inserted_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:00:00Z"},
		{"id":"c2","map_id":"f1b0","solar_system_source":30000142,"solar_system_target":31000005,"type":1}
	]
}}`

func TestSnapshotRequestsAndDecodesTheMapSystems(t *testing.T) {
	t.Parallel()

	server, recorder := newStreamServer(t, respondWith(http.StatusOK, snapshotFixture))
	client := newClient(t, server.URL)

	got, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}

	want := wanderer.Snapshot{
		Systems: []int64{31002604, 30000142, 31000005},
		Connections: []wanderer.Connection{
			{Source: 31002604, Target: 30000142},
			{Source: 30000142, Target: 31000005},
		},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Snapshot() = %+v, want %+v", got, want)
	}

	requests := recorder.all()
	if len(requests) != 1 {
		t.Fatalf("made %d requests, want 1", len(requests))
	}
	request := requests[0]
	if want := "/api/maps/" + testMap + "/systems"; request.Path != want {
		t.Errorf("request path = %q, want %q", request.Path, want)
	}
	if len(request.Query) != 0 {
		t.Errorf("request query = %v, want none", request.Query)
	}
	if want := "Bearer " + testToken; request.Header.Get("Authorization") != want {
		t.Errorf("Authorization = %q, want %q", request.Header.Get("Authorization"), want)
	}
	if want := "application/json"; request.Header.Get("Accept") != want {
		t.Errorf("Accept = %q, want %q", request.Header.Get("Accept"), want)
	}
}

func TestSnapshotOfAnEmptyMap(t *testing.T) {
	t.Parallel()

	server, _ := newStreamServer(t, respondWith(http.StatusOK, `{"data":{"systems":[],"connections":[]}}`))
	got, err := newClient(t, server.URL).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	if len(got.Systems) != 0 || len(got.Connections) != 0 {
		t.Errorf("Snapshot() = %+v, want an empty snapshot", got)
	}
}

func TestSnapshotRejectsAMalformedAnswer(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"not json":            "<html>maintenance</html>",
		"no data":             `{"systems":[],"connections":[]}`,
		"no connections list": `{"data":{"systems":[{"solar_system_id":1}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server, _ := newStreamServer(t, respondWith(http.StatusOK, body))
			_, err := newClient(t, server.URL).Snapshot(context.Background())
			if err == nil {
				t.Fatal("Snapshot() succeeded, want an error")
			}
			if wanderer.IsFatal(err) {
				t.Errorf("Snapshot() = %v, want a retryable error", err)
			}
		})
	}
}

func TestSnapshotFatalResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		body     string
		wantHint string
	}{
		{
			name:     "rejected token",
			status:   http.StatusUnauthorized,
			body:     `{"error":"Invalid or missing API key"}`,
			wantHint: "public API key",
		},
		{
			name:     "public api disabled",
			status:   http.StatusForbidden,
			body:     "Public API is disabled",
			wantHint: "public API disabled",
		},
		{
			name:     "unknown map",
			status:   http.StatusNotFound,
			body:     `{"error":"Map not found"}`,
			wantHint: "map ID or slug",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server, _ := newStreamServer(t, respondWith(test.status, test.body))
			_, err := newClient(t, server.URL).Snapshot(context.Background())

			var fatal *wanderer.FatalError
			if !errors.As(err, &fatal) {
				t.Fatalf("Snapshot() = %v, want a *wanderer.FatalError", err)
			}
			if fatal.StatusCode != test.status {
				t.Errorf("StatusCode = %d, want %d", fatal.StatusCode, test.status)
			}
			if fatal.Message == "" {
				t.Error("Message is empty, want the server's error message")
			}
			if !strings.Contains(fatal.Hint, test.wantHint) {
				t.Errorf("Hint = %q, want it to mention %q", fatal.Hint, test.wantHint)
			}
			if !strings.Contains(err.Error(), "map systems request") {
				t.Errorf("Error() = %q, want it to name the snapshot request", err.Error())
			}
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("Error() = %q leaks the token", err.Error())
			}
		})
	}
}

func TestSnapshotRetryableResponses(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()

			server, _ := newStreamServer(t, respondWith(status, "upstream is restarting"))
			_, err := newClient(t, server.URL).Snapshot(context.Background())
			if err == nil {
				t.Fatal("Snapshot() succeeded, want an error")
			}
			if wanderer.IsFatal(err) {
				t.Errorf("Snapshot() = %v, want a retryable error", err)
			}
			if !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Errorf("Error() = %q, want it to report the status", err.Error())
			}
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("Error() = %q leaks the token", err.Error())
			}
		})
	}
}

func TestSnapshotNetworkErrorKeepsTheTokenOut(t *testing.T) {
	t.Parallel()

	server, _ := newStreamServer(t, closeImmediately)
	base := server.URL
	server.Close()

	_, err := newClient(t, base).Snapshot(context.Background())
	if err == nil {
		t.Fatal("Snapshot() against a closed server succeeded, want an error")
	}
	if wanderer.IsFatal(err) {
		t.Errorf("Snapshot() = %v, want a retryable error", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("Error() = %q leaks the token", err.Error())
	}
}
