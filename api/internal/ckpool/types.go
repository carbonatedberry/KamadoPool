package ckpool

import (
	"context"
	"fmt"
)

// DSPSToHashrate converts CKPool's internal "difficulty shares per second"
// value to hashrate in H/s. CKPool shares are normalized to diff 1 which
// corresponds to 2^32 hashes.
const HashesPerShare = 4294967296.0

func DSPSToHashrate(dsps float64) float64 { return dsps * HashesPerShare }

// PoolStats is the response to `poolstats` on the stratifier socket.
type PoolStats struct {
	Start        int64   `json:"start"`
	Update       int64   `json:"update"`
	Workers      int64   `json:"workers"`
	Users        int64   `json:"users"`
	Disconnected int64   `json:"disconnected"`
	Shares       int64   `json:"shares"`
	SPS1         float64 `json:"sps1"`
	SPS5         float64 `json:"sps5"`
	SPS15        float64 `json:"sps15"`
	SPS60        float64 `json:"sps60"`
	Accepted     int64   `json:"accepted"`
	Rejected     int64   `json:"rejected"`
	RejectCount  int64   `json:"rejectcount"`
	DSPS1        float64 `json:"dsps1"`
	DSPS5        float64 `json:"dsps5"`
	DSPS15       float64 `json:"dsps15"`
	DSPS60       float64 `json:"dsps60"`
	DSPS360      float64 `json:"dsps360"`
	DSPS1440     float64 `json:"dsps1440"`
	DSPS10080    float64 `json:"dsps10080"`
}

// User is one entry in the `users` response. BestEver is exposed by the
// Kamado ckpool patch (patches/0001-expose-bestever-in-runtime-json.patch)
// and will be 0 against an unpatched upstream ckpool.
type User struct {
	User      string  `json:"user"`
	ID        int64   `json:"id"`
	Workers   int     `json:"workers"`
	BestDiff  float64 `json:"bestdiff"`
	BestEver  float64 `json:"bestever"`
	DSPS1     float64 `json:"dsps1"`
	DSPS5     float64 `json:"dsps5"`
	DSPS60    float64 `json:"dsps60"`
	DSPS1440  float64 `json:"dsps1440"`
	DSPS10080 float64 `json:"dsps10080"`
	LastShare int64   `json:"lastshare"`
}

type UsersResponse struct {
	Users []User `json:"users"`
}

// Worker is one entry in the `workers` response. BestEver requires the
// Kamado ckpool patch; see User above.
type Worker struct {
	User      string  `json:"user"`
	Worker    string  `json:"worker"`
	ID        int64   `json:"id"`
	DSPS1     float64 `json:"dsps1"`
	DSPS5     float64 `json:"dsps5"`
	DSPS60    float64 `json:"dsps60"`
	DSPS1440  float64 `json:"dsps1440"`
	LastShare int64   `json:"lastshare"`
	BestDiff  float64 `json:"bestdiff"`
	BestEver  float64 `json:"bestever"`
	Shares    int64   `json:"shares"`
	MinDiff   float64 `json:"mindiff"`
	Idle      bool    `json:"idle"`
}

type WorkersResponse struct {
	Workers []Worker `json:"workers"`
}

// Client is one entry in the `clients` response. Note: this is CKPool's
// view of a connected stratum session, not our Go socket client.
type StratumClient struct {
	ID         int64   `json:"id"`
	Enonce1    string  `json:"enonce1"`
	Enonce1Var string  `json:"enonce1var"`
	Enonce164  int64   `json:"enonce1_64"`
	Diff       float64 `json:"diff"`
	DSPS1      float64 `json:"dsps1"`
	DSPS5      float64 `json:"dsps5"`
	DSPS60     float64 `json:"dsps60"`
	DSPS1440   float64 `json:"dsps1440"`
	DSPS10080  float64 `json:"dsps10080"`
	LastShare  int64   `json:"lastshare"`
	StartTime  int64   `json:"starttime"`
	Address    string  `json:"address"`
	Subscribed bool    `json:"subscribed"`
	Authorised bool    `json:"authorised"`
	Idle       bool    `json:"idle"`
	UserAgent  string  `json:"useragent"`
	WorkerName string  `json:"workername"`
	UserID     int64   `json:"userid"`
	Server     int     `json:"server"`
	BestDiff   float64 `json:"bestdiff"`
	ProxyID    int     `json:"proxyid"`
	SubProxyID int     `json:"subproxyid"`
}

type ClientsResponse struct {
	Clients []StratumClient `json:"clients"`
}

// Uptime is the response to `uptime`.
type Uptime struct {
	Uptime int64 `json:"uptime"`
}

// --- High-level wrappers ------------------------------------------------

func (c *Client) PoolStats(ctx context.Context) (*PoolStats, error) {
	var ps PoolStats
	if err := c.SendJSON(ctx, SocketStratifier, "poolstats", &ps); err != nil {
		return nil, err
	}
	return &ps, nil
}

func (c *Client) Users(ctx context.Context) ([]User, error) {
	var r UsersResponse
	if err := c.SendJSON(ctx, SocketStratifier, "users", &r); err != nil {
		return nil, err
	}
	return r.Users, nil
}

func (c *Client) Workers(ctx context.Context) ([]Worker, error) {
	var r WorkersResponse
	if err := c.SendJSON(ctx, SocketStratifier, "workers", &r); err != nil {
		return nil, err
	}
	return r.Workers, nil
}

func (c *Client) Clients(ctx context.Context) ([]StratumClient, error) {
	var r ClientsResponse
	if err := c.SendJSON(ctx, SocketStratifier, "clients", &r); err != nil {
		return nil, err
	}
	return r.Clients, nil
}

func (c *Client) Uptime(ctx context.Context) (int64, error) {
	var u Uptime
	if err := c.SendJSON(ctx, SocketStratifier, "uptime", &u); err != nil {
		return 0, err
	}
	return u.Uptime, nil
}

// GetUser returns stats for a single user by address.
func (c *Client) GetUser(ctx context.Context, address string) (*User, error) {
	cmd := fmt.Sprintf("getuser.{\"user\":%q}", address)
	var u User
	if err := c.SendJSON(ctx, SocketStratifier, cmd, &u); err != nil {
		return nil, err
	}
	return &u, nil
}
