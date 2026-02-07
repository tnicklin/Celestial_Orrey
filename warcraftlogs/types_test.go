package warcraftlogs

import (
	"testing"
)

func TestBuildReportURL(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		fightID *int64
		pullID  *int64
		want    string
	}{
		{
			name:    "basic report URL",
			code:    "abc123",
			fightID: nil,
			pullID:  nil,
			want:    "https://www.warcraftlogs.com/reports/abc123",
		},
		{
			name:    "report URL with fight ID",
			code:    "xyz789",
			fightID: ptr(int64(5)),
			pullID:  nil,
			want:    "https://www.warcraftlogs.com/reports/xyz789#fight=5",
		},
		{
			name:    "report URL with pull ID",
			code:    "def456",
			fightID: nil,
			pullID:  ptr(int64(10)),
			want:    "https://www.warcraftlogs.com/reports/def456#pull=10",
		},
		{
			name:    "fight ID takes precedence over pull ID",
			code:    "ghi789",
			fightID: ptr(int64(3)),
			pullID:  ptr(int64(7)),
			want:    "https://www.warcraftlogs.com/reports/ghi789#fight=3",
		},
		{
			name:    "empty code returns empty string",
			code:    "",
			fightID: nil,
			pullID:  nil,
			want:    "",
		},
		{
			name:    "whitespace code returns empty string",
			code:    "   ",
			fightID: ptr(int64(1)),
			pullID:  nil,
			want:    "",
		},
		{
			name:    "zero fight ID",
			code:    "jkl012",
			fightID: ptr(int64(0)),
			pullID:  nil,
			want:    "https://www.warcraftlogs.com/reports/jkl012#fight=0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildReportURL(tt.code, tt.fightID, tt.pullID)
			if got != tt.want {
				t.Errorf("BuildReportURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildMythicPlusURL(t *testing.T) {
	tests := []struct {
		name string
		run  MythicPlusRun
		want string
	}{
		{
			name: "basic M+ URL",
			run: MythicPlusRun{
				ReportCode: "abc123",
				FightID:    5,
			},
			want: "https://www.warcraftlogs.com/reports/abc123#fight=5",
		},
		{
			name: "zero fight ID",
			run: MythicPlusRun{
				ReportCode: "xyz789",
				FightID:    0,
			},
			want: "https://www.warcraftlogs.com/reports/xyz789#fight=0",
		},
		{
			name: "empty report code",
			run: MythicPlusRun{
				ReportCode: "",
				FightID:    10,
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildMythicPlusURL(tt.run)
			if got != tt.want {
				t.Errorf("BuildMythicPlusURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}
