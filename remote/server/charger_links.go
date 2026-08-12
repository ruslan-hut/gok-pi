// charger_links.go holds the mapping between EV charging locations managed by the
// evsys central system and the battery agents that should discharge while a car is
// charging there. Links are edited from the web UI and consulted by the evsys
// webhook receiver in chargers.go.

package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gok-pi/internal/lib/atomicfile"
)

// ChargerLink connects an evsys charging location to one battery on one agent.
//
// LocationId is the primary match key: evsys reports it on OCPP 1.6 transaction
// events. OCPP 2.0.1 events carry no location, so ChargePointIds lists the charge
// point identities that should also resolve to this link.
type ChargerLink struct {
	Name           string   `json:"name"`             // unique label, shown in the UI
	Enabled        bool     `json:"enabled"`          // disabled links never trigger a discharge
	LocationId     string   `json:"location_id"`      // evsys location id
	ChargePointIds []string `json:"charge_point_ids"` // fallback match keys (OCPP 2.0.1)
	AgentId        string   `json:"agent_id"`         // gok-pi agent device id
	BatteryName    string   `json:"battery_name"`     // battery to discharge (command target)
	PowerLimit     int      `json:"power_limit"`      // discharge rate in W
	SocLimit       int      `json:"soc_limit"`        // SoC floor in %
	MaxDurationMin int      `json:"max_duration_min"` // safety cap in minutes; 0 = no cap
}

// Validate reports whether the link is usable. An enabled link with no match key
// would never fire, and one with no target would produce commands that go nowhere.
func (l ChargerLink) Validate() error {
	if strings.TrimSpace(l.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if l.LocationId == "" && len(l.ChargePointIds) == 0 {
		return fmt.Errorf("link %q: location_id or charge_point_ids is required", l.Name)
	}
	if strings.TrimSpace(l.AgentId) == "" {
		return fmt.Errorf("link %q: agent_id is required", l.Name)
	}
	if strings.TrimSpace(l.BatteryName) == "" {
		return fmt.Errorf("link %q: battery_name is required", l.Name)
	}
	if l.PowerLimit <= 0 {
		return fmt.Errorf("link %q: power_limit must be greater than 0", l.Name)
	}
	if l.SocLimit < 0 || l.SocLimit > 100 {
		return fmt.Errorf("link %q: soc_limit must be between 0 and 100", l.Name)
	}
	if l.MaxDurationMin < 0 {
		return fmt.Errorf("link %q: max_duration_min cannot be negative", l.Name)
	}
	return nil
}

// Matches reports whether an evsys event belongs to this link. The location is
// checked first; charge point ids are the fallback for events that carry none.
func (l ChargerLink) Matches(locationID, chargePointID string) bool {
	if l.LocationId != "" && locationID != "" && l.LocationId == locationID {
		return true
	}
	if chargePointID == "" {
		return false
	}
	for _, id := range l.ChargePointIds {
		if id == chargePointID {
			return true
		}
	}
	return false
}

// ValidateChargerLinks validates a whole set and enforces unique names, which the
// UI relies on to address individual links.
func ValidateChargerLinks(links []ChargerLink) error {
	seen := make(map[string]struct{}, len(links))
	for _, l := range links {
		if err := l.Validate(); err != nil {
			return err
		}
		if _, exists := seen[l.Name]; exists {
			return fmt.Errorf("duplicate charger link name: %s", l.Name)
		}
		seen[l.Name] = struct{}{}
	}
	return nil
}

// ChargerLinkStore provides thread-safe access to the charger links, persisted to
// a JSON file on disk.
type ChargerLinkStore struct {
	mu    sync.RWMutex
	links []ChargerLink
	path  string
}

// NewChargerLinkStore creates a store backed by the given file path. If the file
// exists it is loaded; otherwise the store starts empty (feature inactive).
func NewChargerLinkStore(path string) *ChargerLinkStore {
	s := &ChargerLinkStore{path: path}
	if path != "" {
		s.load()
	}
	return s
}

func (s *ChargerLinkStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return // file doesn't exist yet — start empty
	}
	var links []ChargerLink
	if err := json.Unmarshal(data, &links); err != nil {
		return
	}
	s.links = links
}

// List returns a copy of the configured links.
func (s *ChargerLinkStore) List() []ChargerLink {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneChargerLinks(s.links)
}

// Find returns the first enabled link matching the event's location or charge
// point. Disabled links are skipped so an operator can pause the integration for
// one site without deleting its configuration.
func (s *ChargerLinkStore) Find(locationID, chargePointID string) (ChargerLink, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, l := range s.links {
		if !l.Enabled {
			continue
		}
		if l.Matches(locationID, chargePointID) {
			return l, true
		}
	}
	return ChargerLink{}, false
}

// Get returns the link with the given name regardless of its enabled state. Used
// when resuming a session recorded before the link was disabled.
func (s *ChargerLinkStore) Get(name string) (ChargerLink, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, l := range s.links {
		if l.Name == name {
			return l, true
		}
	}
	return ChargerLink{}, false
}

// Set replaces the whole link set and persists it to disk.
func (s *ChargerLinkStore) Set(links []ChargerLink) error {
	if err := ValidateChargerLinks(links); err != nil {
		return err
	}

	stored := cloneChargerLinks(links)

	s.mu.Lock()
	s.links = stored
	s.mu.Unlock()

	if s.path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, data, 0o644)
}

func cloneChargerLinks(links []ChargerLink) []ChargerLink {
	if links == nil {
		return []ChargerLink{}
	}
	out := make([]ChargerLink, len(links))
	copy(out, links)
	for i := range out {
		if links[i].ChargePointIds == nil {
			continue
		}
		ids := make([]string, len(links[i].ChargePointIds))
		copy(ids, links[i].ChargePointIds)
		out[i].ChargePointIds = ids
	}
	return out
}
