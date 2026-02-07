package client

import (
	"context"

	"github.com/tnicklin/celestial_orrey/models"
)

// Client defines the interface for fetching data from RaiderIO.
type Client interface {
	FetchWeeklyRuns(context.Context, models.Character) ([]models.CompletedKey, error)
}
