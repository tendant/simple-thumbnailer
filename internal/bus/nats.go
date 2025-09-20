// internal/bus/nats.go
package bus

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
)

type Client struct{ nc *nats.Conn }

func Connect(url string) (*Client, error) {
	nc, err := nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Timeout(5*time.Second),
	)
	if err != nil {
		return nil, err
	}
	return &Client{nc: nc}, nil
}

func (c *Client) Close() {
	if c.nc != nil {
		_ = c.nc.Drain()
	}
}

func (c *Client) Conn() *nats.Conn { return c.nc }

func (c *Client) PublishJSON(subject string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.nc.Publish(subject, b)
}

func (c *Client) SubscribeJSON(subject string, handler func(ctx context.Context, data []byte)) (*nats.Subscription, error) {
	return c.nc.Subscribe(subject, func(msg *nats.Msg) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		handler(ctx, msg.Data)
	})
}
