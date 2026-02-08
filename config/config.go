package config

import (
	"os"

	"github.com/tnicklin/celestial_orrey/discord"
	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/models"
	"github.com/tnicklin/celestial_orrey/raiderio"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/warcraftlogs"
	"go.uber.org/config"
)

// Load reads configuration from the specified YAML files.
// Files are merged in order, with later files overriding earlier ones.
// Missing files are silently ignored.
