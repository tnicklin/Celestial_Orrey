package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tnicklin/celestial_orrey/models"
)

func TestFetchWeeklyRuns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/v1/characters/profile" {
			t.Fatalf("unexpected path: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "mythic_plus_weekly_highest_level_runs": [
    {
      "keystone_run_id": 12345,
      "dungeon": "Mists of Tirna Scithe",
      "mythic_level": 10,
      "clear_time_ms": 1320000,
      "par_time_ms": 1500000,
      "completed_at": "2026-02-01T01:23:45Z"
    }
  ]
}`))
	}))
	defer server.Close()

	client := New(Params{
		BaseURL:    server.URL,
		UserAgent:  "test/1.0",
		HTTPClient: server.Client(),
	})

	runs, err := client.FetchWeeklyRuns(context.Background(), models.Character{
		Region: "us",
		Realm:  "illidan",
		Name:   "Arthas",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].KeyID != 12345 {
		t.Fatalf("expected key id 12345, got %d", runs[0].KeyID)
	}
	if runs[0].Dungeon != "Mists of Tirna Scithe" {
		t.Fatalf("expected dungeon to map, got %s", runs[0].Dungeon)
	}
	if runs[0].Character != "arthas" {
		t.Fatalf("expected character to map, got %s", runs[0].Character)
	}
}
