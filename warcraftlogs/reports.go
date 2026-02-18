package warcraftlogs

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const _reportsQuery = `
query Reports($startTime: Float!, $endTime: Float!, $guildName: String, $guildServerSlug: String, $guildServerRegion: String, $limit: Int) {
  reportData {
    reports(startTime: $startTime, endTime: $endTime, guildName: $guildName, guildServerSlug: $guildServerSlug, guildServerRegion: $guildServerRegion, limit: $limit) {
      data {
        code
        title
        startTime
        endTime
        zone {
          name
        }
      }
    }
  }
}
`

func (c *DefaultWCL) FetchReports(ctx context.Context, filter ReportFilter) ([]ReportSummary, error) {
	if filter.StartTime.IsZero() || filter.EndTime.IsZero() {
		return nil, errors.New("warcraftlogs: start/end time required")
	}
	vars := map[string]any{
		"startTime": float64(filter.StartTime.UnixMilli()),
		"endTime":   float64(filter.EndTime.UnixMilli()),
	}
	if filter.GuildName != "" {
		vars["guildName"] = filter.GuildName
	}
	if filter.ServerSlug != "" {
		vars["guildServerSlug"] = filter.ServerSlug
	}
	if filter.ServerRegion != "" {
		vars["guildServerRegion"] = filter.ServerRegion
	}
	if filter.Limit > 0 {
		vars["limit"] = filter.Limit
	}

	raw, err := c.Query(ctx, _reportsQuery, vars)
	if err != nil {
		return nil, err
	}

	var payload reportsResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}

	var out []ReportSummary
	for _, report := range payload.ReportData.Reports.Data {
		var start time.Time
		if report.StartTime > 0 {
			start = time.UnixMilli(report.StartTime)
		}
		var end time.Time
		if report.EndTime > 0 {
			end = time.UnixMilli(report.EndTime)
		}
		zoneName := ""
		if report.Zone != nil {
			zoneName = report.Zone.Name
		}
		out = append(out, ReportSummary{
			Code:     report.Code,
			Title:    report.Title,
			ZoneName: zoneName,
			Start:    start,
			End:      end,
		})
	}
	return out, nil
}

type reportsResponse struct {
	ReportData struct {
		Reports struct {
			Data []reportData `json:"data"`
		} `json:"reports"`
	} `json:"reportData"`
}

type reportData struct {
	Code      string `json:"code"`
	Title     string `json:"title"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
	Zone      *struct {
		Name string `json:"name"`
	} `json:"zone"`
}
