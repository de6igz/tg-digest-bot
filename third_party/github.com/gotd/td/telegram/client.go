package telegram

import (
"context"

"github.com/gotd/td/telegram/auth"
)

type Client struct{}

type Options struct {
SessionStorage auth.SessionStorage
}

func NewClient(apiID int, apiHash string, options Options) *Client {
_ = apiID
_ = apiHash
_ = options
return &Client{}
}

func (c *Client) Run(ctx context.Context, f func(context.Context) error) error {
return f(ctx)
}
