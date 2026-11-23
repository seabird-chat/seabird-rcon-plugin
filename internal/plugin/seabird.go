package plugin

import (
	"context"
	"time"

	"github.com/seabird-chat/seabird-go"
	"github.com/seabird-chat/seabird-go/pb"
)

// SeabirdChat sends replies through seabird core.
type SeabirdChat struct {
	Client *seabird.Client
}

func (c *SeabirdChat) Send(ctx context.Context, channelID, text string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := c.Client.Inner.SendMessage(ctx, &pb.SendMessageRequest{
		ChannelId: channelID,
		Text:      text,
		// Status replies carry the map link, which doesn't need a preview.
		Tags: map[string]string{"url/skip": "1"},
	})
	return err
}
