package warcraftlogs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
)

type WCL interface {
	Query(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error)
	FetchReports(ctx context.Context, filter ReportFilter) ([]ReportSummary, error)
	FetchCharacterMythicPlus(ctx context.Context, char models.Character, limit int) ([]MythicPlusRun, error)
}

type ReportLink struct {
	KeyID      int64
	ReportCode string
	FightID    *int64
	PullID     *int64
	URL        string
}

type ReportSummary struct {
	Code     string
	Title    string
	ZoneName string
	Start    time.Time
	End      time.Time
}

type ReportFilter struct {
	StartTime    time.Time
	EndTime      time.Time
	GuildName    string
	ServerSlug   string
	ServerRegion string
	Limit        int
}

// MythicPlusRun represents a completed M+ dungeon from WarcraftLogs.
type MythicPlusRun struct {
	ReportCode    string
	FightID       int
	Dungeon       string
	EncounterID   int
	KeystoneLevel int
	KeystoneTime  int64 // completion time in ms
	KeystoneBonus int   // +0, +1, +2 timing bonus
	Rating        float64
	CompletedAt   time.Time // absolute timestamp of completion
	Kill          bool
}

// MatchResult represents a successful link between RaiderIO key and WCL report.
type MatchResult struct {
	Key        models.CompletedKey
	Run        MythicPlusRun
	Confidence float64 // 0.0 to 1.0 match confidence
}

func BuildReportURL(code string, fightID, pullID *int64) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	base := fmt.Sprintf("https://www.warcraftlogs.com/reports/%s", code)
	if fightID != nil {
		return fmt.Sprintf("%s#fight=%d", base, *fightID)
	}
	if pullID != nil {
		return fmt.Sprintf("%s#pull=%d", base, *pullID)
	}
	return base
}

// BuildMythicPlusURL builds a direct link to an M+ run in WarcraftLogs.
func BuildMythicPlusURL(run MythicPlusRun) string {
	fightID := int64(run.FightID)
	return BuildReportURL(run.ReportCode, &fightID, nil)
}
