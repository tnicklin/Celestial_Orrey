package discord

import "context"

// Discord defines the interface for the Discord client.
type Discord interface {
	WriteMessage(channelNameOrID, msg string) error
	Start(ctx context.Context) error
	Stop() error
}
