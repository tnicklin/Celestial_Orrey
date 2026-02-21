package client

import (
	"context"

	"github.com/tnicklin/celestial_orrey/models"
)

// ProfileResult holds the result of a RaiderIO profile fetch.
type ProfileResult struct {
	Keys     []models.CompletedKey
	RIOScore float64
}

// Client defines the interface for fetching data from RaiderIO.
type Client interface {
	FetchWeeklyRuns(context.Context, models.Character) (ProfileResult, error)
}
