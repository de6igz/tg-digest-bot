package redis

import "context"

type Client struct{}

type BoolCmd struct{}

type StatusCmd struct{}

type StringCmd struct{}

func NewClient(opt *Options) *Client { return &Client{} }

type Options struct{
Addr string
}

func (c *Client) SetNX(ctx context.Context, key string, value interface{}, ttl interface{}) *BoolCmd {
return &BoolCmd{}
}

func (c *Client) Set(ctx context.Context, key string, value interface{}, ttl interface{}) *StatusCmd {
return &StatusCmd{}
}

func (c *Client) Get(ctx context.Context, key string) *StringCmd {
return &StringCmd{}
}

func (c *Client) Del(ctx context.Context, keys ...string) *StatusCmd {
return &StatusCmd{}
}

func (c *BoolCmd) Result() (bool, error) { return true, nil }

func (c *StatusCmd) Err() error { return nil }

func (c *StringCmd) Bytes() ([]byte, error) { return nil, nil }
