package types

import (
	"fmt"
	"time"

	"go.uber.org/multierr"

	"github.com/smartcontractkit/sqlx"

	"github.com/smartcontractkit/chainlink-terra/pkg/terra"
	terraclient "github.com/smartcontractkit/chainlink-terra/pkg/terra/client"
	terraconfig "github.com/smartcontractkit/chainlink-terra/pkg/terra/config"

	"github.com/smartcontractkit/chainlink/core/chains/terra/terratxm"
	"github.com/smartcontractkit/chainlink/core/logger"
	"github.com/smartcontractkit/chainlink/core/services/keystore"
	"github.com/smartcontractkit/chainlink/core/services/pg"
	"github.com/smartcontractkit/chainlink/core/utils"
)

// DefaultRequestTimeoutSeconds is the default Terra client timeout.
const DefaultRequestTimeoutSeconds = 10

var _ terra.Chain = (*chain)(nil)

type chain struct {
	utils.StartStopOnce
	id     string
	cfg    terraconfig.ChainCfg
	client *terraclient.Client
	txm    *terratxm.Txm
	lggr   logger.Logger
}

// NewChain returns a new chain backed by node.
func NewChain(db *sqlx.DB, ks keystore.Terra, logCfg pg.LogConfig, eb pg.EventBroadcaster, dbchain Chain, lggr logger.Logger) (*chain, error) {
	if len(dbchain.Nodes) == 0 {
		return nil, fmt.Errorf("no nodes for Terra chain: %s", dbchain.ID)
	}
	cfg := dbchain.Cfg
	lggr = lggr.With("terraChainID", dbchain.ID)
	node := dbchain.Nodes[0] // TODO multi-node client pool https://app.shortcut.com/chainlinklabs/story/26278/terra-multi-node-client-pools
	lggr.Debugw(fmt.Sprintf("Terra chain %q has %d nodes - using %q", dbchain.ID, len(dbchain.Nodes), node.Name),
		"tendermint-url", node.TendermintURL)
	client, err := terraclient.NewClient(dbchain.ID,
		node.TendermintURL, node.FCDURL, DefaultRequestTimeoutSeconds, lggr.Named("Client"))
	if err != nil {
		return nil, err
	}
	txm, err := terratxm.NewTxm(db, client, cfg.FallbackGasPriceULuna, cfg.GasLimitMultiplier, ks, lggr, logCfg, eb, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &chain{
		id:     dbchain.ID,
		cfg:    cfg,
		client: client,
		txm:    txm,
		lggr:   lggr.Named("Chain"),
	}, nil
}

func (c *chain) ID() string {
	return c.id
}

func (c *chain) Config() terraconfig.ChainCfg {
	return c.cfg
}

func (c *chain) MsgEnqueuer() terra.MsgEnqueuer {
	return c.txm
}

func (c *chain) Reader() terraclient.Reader {
	return c.client
}

func (c *chain) Start() error {
	return c.StartOnce("Chain", func() error {
		c.lggr.Debug("Starting")
		//TODO dial client?

		c.lggr.Debug("Starting txm")
		return c.txm.Start()
	})
}

func (c *chain) Close() error {
	return c.StopOnce("Chain", func() error {
		c.lggr.Debug("Stopping")
		c.lggr.Debug("Stopping txm")
		return c.txm.Close()
	})
}

func (c *chain) Ready() error {
	return multierr.Combine(
		c.StartStopOnce.Ready(),
		c.txm.Ready(),
	)
}

func (c *chain) Healthy() error {
	return multierr.Combine(
		c.StartStopOnce.Healthy(),
		c.txm.Healthy(),
	)
}