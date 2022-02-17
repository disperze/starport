package network

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	campaigntypes "github.com/tendermint/spn/x/campaign/types"
	launchtypes "github.com/tendermint/spn/x/launch/types"
	profiletypes "github.com/tendermint/spn/x/profile/types"
	"github.com/tendermint/starport/starport/pkg/cosmoserror"
	"github.com/tendermint/starport/starport/pkg/cosmosutil"
	"github.com/tendermint/starport/starport/pkg/events"
	"github.com/tendermint/starport/starport/services/network/networktypes"
)

// publishOptions holds info about how to create a chain.
type publishOptions struct {
	genesisURL string
	chainID    string
	campaignID uint64
	noCheck    bool
	shares     campaigntypes.Shares
}

// PublishOption configures chain creation.
type PublishOption func(*publishOptions)

// WithCampaign add a campaign id.
func WithCampaign(id uint64) PublishOption {
	return func(o *publishOptions) {
		o.campaignID = id
	}
}

// WithChainID use a custom chain id.
func WithChainID(chainID string) PublishOption {
	return func(o *publishOptions) {
		o.chainID = chainID
	}
}

// WithNoCheck disables checking integrity of the chain.
func WithNoCheck() PublishOption {
	return func(o *publishOptions) {
		o.noCheck = true
	}
}

// WithCustomGenesis enables using a custom genesis during publish.
func WithCustomGenesis(url string) PublishOption {
	return func(o *publishOptions) {
		o.genesisURL = url
	}
}

// WithShares provides a account shares
func WithShares(shares campaigntypes.Shares) PublishOption {
	return func(o *publishOptions) {
		o.shares = shares
	}
}

// Publish submits Genesis to SPN to announce a new network.
func (n Network) Publish(ctx context.Context, c Chain, options ...PublishOption) (launchID, campaignID uint64, err error) {
	o := publishOptions{}
	for _, apply := range options {
		apply(&o)
	}

	var genesisHash string

	// if the initial genesis is a genesis URL and no check are performed, we simply fetch it and get its hash.
	if o.noCheck && o.genesisURL != "" {
		if _, genesisHash, err = cosmosutil.GenesisAndHashFromURL(ctx, o.genesisURL); err != nil {
			return 0, 0, err
		}
	}

	chainID := o.chainID
	if chainID == "" {
		chainID, err = c.ID()
		if err != nil {
			return 0, 0, err
		}
	}

	coordinatorAddress := n.account.Address(networktypes.SPN)
	campaignID = o.campaignID

	n.ev.Send(events.New(events.StatusOngoing, "Publishing the network"))

	_, err = profiletypes.
		NewQueryClient(n.cosmos.Context).
		CoordinatorByAddress(ctx, &profiletypes.QueryGetCoordinatorByAddressRequest{
			Address: coordinatorAddress,
		})
	if cosmoserror.Unwrap(err) == cosmoserror.ErrInvalidRequest {
		msgCreateCoordinator := profiletypes.NewMsgCreateCoordinator(
			coordinatorAddress,
			"",
			"",
			"",
		)
		if _, err := n.cosmos.BroadcastTx(n.account.Name, msgCreateCoordinator); err != nil {
			return 0, 0, err
		}
	} else if err != nil {
		return 0, 0, err
	}

	if campaignID != 0 {
		_, err = campaigntypes.
			NewQueryClient(n.cosmos.Context).
			Campaign(ctx, &campaigntypes.QueryGetCampaignRequest{
				CampaignID: o.campaignID,
			})
		if err != nil {
			return 0, 0, err
		}
	} else {
		msgCreateCampaign := campaigntypes.NewMsgCreateCampaign(
			coordinatorAddress,
			c.Name(),
			nil,
		)
		res, err := n.cosmos.BroadcastTx(n.account.Name, msgCreateCampaign)
		if err != nil {
			return 0, 0, err
		}

		var createCampaignRes campaigntypes.MsgCreateCampaignResponse
		if err := res.Decode(&createCampaignRes); err != nil {
			return 0, 0, err
		}
		campaignID = createCampaignRes.CampaignID
	}

	msgCreateChain := launchtypes.NewMsgCreateChain(
		n.account.Address(networktypes.SPN),
		chainID,
		c.SourceURL(),
		c.SourceHash(),
		o.genesisURL,
		genesisHash,
		true,
		campaignID,
	)
	res, err := n.cosmos.BroadcastTx(n.account.Name, msgCreateChain)
	if err != nil {
		return 0, 0, err
	}

	var createChainRes launchtypes.MsgCreateChainResponse
	if err := res.Decode(&createChainRes); err != nil {
		return 0, 0, err
	}

	if !sdk.Coins(o.shares).Empty() {
		err := n.AddShares(campaignID, coordinatorAddress, o.shares)
		if err != nil {
			return createChainRes.LaunchID, campaignID, err
		}
	}
	return createChainRes.LaunchID, campaignID, nil
}

// AddShares add a shares to an account
func (n Network) AddShares(campaignID uint64, address string, shares campaigntypes.Shares) error {
	n.ev.Send(events.New(events.StatusOngoing, fmt.Sprintf(
		"Adding shares %s to account %s for campaign %d",
		sdk.Coins(shares).String(),
		address,
		campaignID,
	)))

	msg := campaigntypes.NewMsgAddShares(
		campaignID,
		n.account.Address(networktypes.SPN),
		address,
		shares,
	)

	res, err := n.cosmos.BroadcastTx(n.account.Name, msg)
	if err != nil {
		return err
	}

	var sharesRes campaigntypes.MsgAddSharesResponse
	if err := res.Decode(&sharesRes); err != nil {
		return err
	}

	n.ev.Send(events.New(events.StatusDone, fmt.Sprintf(
		"Added %s for addess %s in the campaign %d",
		sdk.Coins(shares).String(),
		address,
		campaignID,
	)))
	return nil
}
