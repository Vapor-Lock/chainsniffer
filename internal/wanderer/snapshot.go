package wanderer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Snapshot is a map's topology as Wanderer's REST API reports it: the systems
// on the map and the connections between them. It holds visible systems only,
// since those are all the endpoint lists.
type Snapshot struct {
	// Systems are the solar system IDs of the systems on the map.
	Systems []int64
	// TemporaryNames are the systems' Wanderer temporary names (chain
	// aliases), trimmed, keyed by solar system ID. A system without one, or
	// with one that is blank, has no entry.
	TemporaryNames map[int64]string
	// Connections are the connections between those systems.
	Connections []Connection
}

// Connection is one connection between two systems. Wanderer records a
// direction, but a connection can be travelled both ways.
type Connection struct {
	// Source and Target are the solar system IDs of the connection's ends.
	Source, Target int64
}

// wireSnapshot is the response of MapSystemAPIController.index. Systems and
// connections are pointers so that a response missing either list is told
// apart from an empty map: a snapshot replaces the whole topology, so a
// response of the wrong shape must not read as "no connections".
type wireSnapshot struct {
	Data *struct {
		Systems *[]struct {
			SolarSystemID int64  `json:"solar_system_id"`
			TemporaryName string `json:"temporary_name"`
		} `json:"systems"`
		Connections *[]struct {
			Source int64 `json:"solar_system_source"`
			Target int64 `json:"solar_system_target"`
		} `json:"connections"`
	} `json:"data"`
}

// Snapshot reads the map's systems and connections from
// GET {base}/api/maps/{map}/systems, authenticating with the map's API token
// as the stream does.
//
// A response that no retry can fix — 401, 403, 404 — comes back as a
// *FatalError; a network error, 429, 5xx or a response of the wrong shape as a
// plain error worth retrying. Entries without a system ID are skipped. A null
// temporary_name reads as none.
func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.systems.String(), nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("wanderer: building %s: %w", requestSnapshot, err)
	}
	// The token travels in a header, as for the stream, so neither the URL
	// nor a *url.Error can leak it. Wanderer's content negotiation on this
	// route wants the Accept header.
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("wanderer: sending %s: %w", requestSnapshot, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, statusError(resp, requestSnapshot)
	}

	var wire wireSnapshot
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSnapshotBody)).Decode(&wire); err != nil {
		return Snapshot{}, fmt.Errorf("wanderer: decoding the answer to %s: %w", requestSnapshot, err)
	}
	if wire.Data == nil || wire.Data.Systems == nil || wire.Data.Connections == nil {
		return Snapshot{}, errors.New("wanderer: the answer to " + requestSnapshot + " lacks its systems or connections")
	}

	snapshot := Snapshot{
		Systems:        make([]int64, 0, len(*wire.Data.Systems)),
		TemporaryNames: make(map[int64]string),
		Connections:    make([]Connection, 0, len(*wire.Data.Connections)),
	}
	for _, system := range *wire.Data.Systems {
		if system.SolarSystemID == 0 {
			continue
		}
		snapshot.Systems = append(snapshot.Systems, system.SolarSystemID)
		if name := strings.TrimSpace(system.TemporaryName); name != "" {
			snapshot.TemporaryNames[system.SolarSystemID] = name
		}
	}
	for _, connection := range *wire.Data.Connections {
		if connection.Source != 0 && connection.Target != 0 {
			snapshot.Connections = append(snapshot.Connections, Connection{Source: connection.Source, Target: connection.Target})
		}
	}
	return snapshot, nil
}
