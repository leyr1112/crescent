package keeper

import (
	"fmt"
	"strconv"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/cosmosquad-labs/squad/x/liquidity/types"
)

// LimitOrder handles types.MsgLimitOrder and stores it.
func (k Keeper) LimitOrder(ctx sdk.Context, msg *types.MsgLimitOrder) (types.SwapRequest, error) {
	params := k.GetParams(ctx)

	if msg.OrderLifespan > params.MaxOrderLifespan {
		return types.SwapRequest{}, types.ErrTooLongOrderLifespan
	}
	expireAt := ctx.BlockTime().Add(msg.OrderLifespan)

	pair, found := k.GetPair(ctx, msg.PairId)
	if !found {
		return types.SwapRequest{}, sdkerrors.Wrapf(sdkerrors.ErrNotFound, "pair not found")
	}

	if pair.LastPrice != nil {
		lastPrice := *pair.LastPrice
		switch {
		case msg.Price.GT(lastPrice):
			priceLimit := lastPrice.Mul(sdk.OneDec().Add(params.MaxPriceLimitRatio))
			if msg.Price.GT(priceLimit) {
				return types.SwapRequest{}, sdkerrors.Wrapf(types.ErrPriceOutOfRange, "%s is higher than %s", msg.Price, priceLimit)
			}
		case msg.Price.LT(lastPrice):
			priceLimit := lastPrice.Mul(sdk.OneDec().Sub(params.MaxPriceLimitRatio))
			if msg.Price.LT(priceLimit) {
				return types.SwapRequest{}, sdkerrors.Wrapf(types.ErrPriceOutOfRange, "%s is lower than %s", msg.Price, priceLimit)
			}
		}
	}

	priceTick := types.PriceToTick(msg.Price, int(params.TickPrecision))
	price := msg.Price
	switch msg.Direction {
	case types.SwapDirectionBuy:
		if msg.OfferCoin.Denom != pair.QuoteCoinDenom || msg.DemandCoinDenom != pair.BaseCoinDenom {
			return types.SwapRequest{},
				sdkerrors.Wrapf(types.ErrWrongPair, "denom pair (%s, %s) != (%s, %s)",
					msg.DemandCoinDenom, msg.OfferCoin.Denom, pair.BaseCoinDenom, pair.QuoteCoinDenom)
		}
		if !price.Equal(priceTick) {
			price = priceTick // TODO: apply new DownTick
		}
	case types.SwapDirectionSell:
		if msg.OfferCoin.Denom != pair.BaseCoinDenom || msg.DemandCoinDenom != pair.QuoteCoinDenom {
			return types.SwapRequest{},
				sdkerrors.Wrapf(types.ErrWrongPair, "denom pair (%s, %s) != (%s, %s)",
					msg.OfferCoin.Denom, msg.DemandCoinDenom, pair.BaseCoinDenom, pair.QuoteCoinDenom)
		}
		if !price.Equal(priceTick) {
			price = types.UpTick(priceTick, int(params.TickPrecision)) // TODO: apply new UpTick
		}
	}

	var offerCoin sdk.Coin
	switch msg.Direction {
	case types.SwapDirectionBuy:
		offerCoin = sdk.NewCoin(msg.OfferCoin.Denom, price.MulInt(msg.Amount).Ceil().TruncateInt())
	case types.SwapDirectionSell:
		offerCoin = msg.OfferCoin
	}
	if msg.OfferCoin.IsLT(offerCoin) {
		return types.SwapRequest{}, types.ErrInsufficientOfferCoin
	}
	if offerCoin.Amount.LT(types.MinCoinAmount) {
		return types.SwapRequest{}, types.ErrTooSmallOfferCoin
	}
	refundedCoin := msg.OfferCoin.Sub(offerCoin)

	if err := k.bankKeeper.SendCoins(ctx, msg.GetOrderer(), pair.GetEscrowAddress(), sdk.NewCoins(offerCoin)); err != nil {
		return types.SwapRequest{}, err
	}

	requestId := k.GetNextSwapRequestIdWithUpdate(ctx, pair)
	req := types.NewSwapRequestForLimitOrder(msg, requestId, pair, offerCoin, price, expireAt, ctx.BlockHeight())
	k.SetSwapRequest(ctx, req)

	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			types.EventTypeLimitOrder,
			sdk.NewAttribute(types.AttributeKeyOrderer, msg.Orderer),
			sdk.NewAttribute(types.AttributeKeyPairId, strconv.FormatUint(msg.PairId, 10)),
			sdk.NewAttribute(types.AttributeKeySwapDirection, msg.Direction.String()),
			sdk.NewAttribute(types.AttributeKeyOfferCoin, offerCoin.String()),
			sdk.NewAttribute(types.AttributeKeyDemandCoinDenom, msg.DemandCoinDenom),
			sdk.NewAttribute(types.AttributeKeyPrice, price.String()),
			sdk.NewAttribute(types.AttributeKeyAmount, msg.Amount.String()),
			sdk.NewAttribute(types.AttributeKeyRequestId, strconv.FormatUint(req.Id, 10)),
			sdk.NewAttribute(types.AttributeKeyBatchId, strconv.FormatUint(req.BatchId, 10)),
			sdk.NewAttribute(types.AttributeKeyExpireAt, req.ExpireAt.Format(time.RFC3339)),
			sdk.NewAttribute(types.AttributeKeyRefundedCoin, refundedCoin.String()),
		),
	})

	return req, nil
}

// MarketOrder handles types.MsgMarketOrder and stores it.
func (k Keeper) MarketOrder(ctx sdk.Context, msg *types.MsgMarketOrder) (types.SwapRequest, error) {
	params := k.GetParams(ctx)

	if msg.OrderLifespan > params.MaxOrderLifespan {
		return types.SwapRequest{}, types.ErrTooLongOrderLifespan
	}
	expireAt := ctx.BlockTime().Add(msg.OrderLifespan)

	pair, found := k.GetPair(ctx, msg.PairId)
	if !found {
		return types.SwapRequest{}, sdkerrors.Wrapf(sdkerrors.ErrNotFound, "pair not found")
	}

	if pair.LastPrice == nil {
		return types.SwapRequest{}, types.ErrNoLastPrice
	}
	lastPrice := *pair.LastPrice

	var price sdk.Dec
	var offerCoin sdk.Coin
	switch msg.Direction {
	case types.SwapDirectionBuy:
		if msg.OfferCoin.Denom != pair.QuoteCoinDenom || msg.DemandCoinDenom != pair.BaseCoinDenom {
			return types.SwapRequest{},
				sdkerrors.Wrapf(types.ErrWrongPair, "denom pair (%s, %s) != (%s, %s)",
					msg.DemandCoinDenom, msg.OfferCoin.Denom, pair.BaseCoinDenom, pair.QuoteCoinDenom)
		}
		price = types.PriceToTick(lastPrice.Mul(sdk.OneDec().Add(params.MaxPriceLimitRatio)), int(params.TickPrecision))
		offerCoin = sdk.NewCoin(msg.OfferCoin.Denom, price.MulInt(msg.Amount).Ceil().TruncateInt())
	case types.SwapDirectionSell:
		if msg.OfferCoin.Denom != pair.BaseCoinDenom || msg.DemandCoinDenom != pair.QuoteCoinDenom {
			return types.SwapRequest{},
				sdkerrors.Wrapf(types.ErrWrongPair, "denom pair (%s, %s) != (%s, %s)",
					msg.OfferCoin.Denom, msg.DemandCoinDenom, pair.BaseCoinDenom, pair.QuoteCoinDenom)
		}
		price = types.PriceToUpTick(lastPrice.Mul(sdk.OneDec().Sub(params.MaxPriceLimitRatio)), int(params.TickPrecision))
		offerCoin = msg.OfferCoin
	}
	if msg.OfferCoin.IsLT(offerCoin) {
		return types.SwapRequest{}, types.ErrInsufficientOfferCoin
	}
	refundedCoin := msg.OfferCoin.Sub(offerCoin)

	if err := k.bankKeeper.SendCoins(ctx, msg.GetOrderer(), pair.GetEscrowAddress(), sdk.NewCoins(offerCoin)); err != nil {
		return types.SwapRequest{}, err
	}

	requestId := k.GetNextSwapRequestIdWithUpdate(ctx, pair)
	req := types.NewSwapRequestForMarketOrder(msg, requestId, pair, offerCoin, price, expireAt, ctx.BlockHeight())
	k.SetSwapRequest(ctx, req)

	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			types.EventTypeMarketOrder,
			sdk.NewAttribute(types.AttributeKeyRequestId, strconv.FormatUint(req.Id, 10)),
			sdk.NewAttribute(types.AttributeKeyOrderer, msg.Orderer),
			sdk.NewAttribute(types.AttributeKeyPairId, strconv.FormatUint(msg.PairId, 10)),
			sdk.NewAttribute(types.AttributeKeySwapDirection, msg.Direction.String()),
			sdk.NewAttribute(types.AttributeKeyOfferCoin, msg.OfferCoin.String()),
			sdk.NewAttribute(types.AttributeKeyDemandCoinDenom, msg.DemandCoinDenom),
			sdk.NewAttribute(types.AttributeKeyPrice, price.String()),
			sdk.NewAttribute(types.AttributeKeyAmount, msg.Amount.String()),
			sdk.NewAttribute(types.AttributeKeyBatchId, strconv.FormatUint(req.BatchId, 10)),
			sdk.NewAttribute(types.AttributeKeyExpireAt, req.ExpireAt.Format(time.RFC3339)),
			sdk.NewAttribute(types.AttributeKeyRefundedCoin, refundedCoin.String()),
		),
	})

	return req, nil
}

// CancelOrder handles types.MsgCancelOrder and cancels an order.
func (k Keeper) CancelOrder(ctx sdk.Context, msg *types.MsgCancelOrder) error {
	swapReq, found := k.GetSwapRequest(ctx, msg.PairId, msg.SwapRequestId)
	if !found {
		return sdkerrors.Wrapf(sdkerrors.ErrNotFound, "swap request with id %d in pair %d not found", msg.SwapRequestId, msg.PairId)
	}

	if msg.Orderer != swapReq.Orderer {
		return sdkerrors.Wrap(sdkerrors.ErrUnauthorized, "mismatching orderer")
	}

	if swapReq.Status == types.SwapRequestStatusCanceled {
		return types.ErrAlreadyCanceled
	}

	pair, _ := k.GetPair(ctx, msg.PairId)
	if swapReq.BatchId == pair.CurrentBatchId {
		return types.ErrSameBatch
	}

	if err := k.FinishSwapRequest(ctx, swapReq, types.SwapRequestStatusCanceled); err != nil {
		return err
	}

	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			types.EventTypeCancelOrder,
			sdk.NewAttribute(types.AttributeKeyOrderer, msg.Orderer),
			sdk.NewAttribute(types.AttributeKeyPairId, strconv.FormatUint(msg.PairId, 10)),
			sdk.NewAttribute(types.AttributeKeySwapRequestId, strconv.FormatUint(msg.SwapRequestId, 10)),
		),
	})

	return nil
}

// CancelAllOrders handles types.MsgCancelAllOrders and cancels all orders.
func (k Keeper) CancelAllOrders(ctx sdk.Context, msg *types.MsgCancelAllOrders) error {
	cb := func(pair types.Pair, req types.SwapRequest) (stop bool, err error) {
		if req.Orderer == msg.Orderer && req.Status != types.SwapRequestStatusCanceled && req.BatchId < pair.CurrentBatchId {
			if err := k.FinishSwapRequest(ctx, req, types.SwapRequestStatusCanceled); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	if len(msg.PairIds) == 0 {
		pairMap := map[uint64]types.Pair{}
		if err := k.IterateAllSwapRequests(ctx, func(req types.SwapRequest) (stop bool, err error) {
			pair, ok := pairMap[req.PairId]
			if !ok {
				pair, _ = k.GetPair(ctx, req.PairId)
				pairMap[req.PairId] = pair
			}
			return cb(pair, req)
		}); err != nil {
			return err
		}

		return nil
	}

	for _, pairId := range msg.PairIds {
		pair, found := k.GetPair(ctx, pairId)
		if !found {
			return sdkerrors.Wrapf(sdkerrors.ErrNotFound, "pair %d not found", pairId)
		}
		if err := k.IterateSwapRequestsByPair(ctx, pairId, func(req types.SwapRequest) (stop bool, err error) {
			return cb(pair, req)
		}); err != nil {
			return err
		}
	}

	return nil
}

func (k Keeper) ExecuteMatching(ctx sdk.Context, pair types.Pair) error {
	params := k.GetParams(ctx)
	tickPrec := int(params.TickPrecision)

	skip := true // Whether to skip the matching since there is no orders.

	ob := types.NewOrderBook(tickPrec)
	if err := k.IterateSwapRequestsByPair(ctx, pair.Id, func(req types.SwapRequest) (stop bool, err error) {
		switch req.Status {
		case types.SwapRequestStatusNotExecuted,
			types.SwapRequestStatusNotMatched,
			types.SwapRequestStatusPartiallyMatched:
			if req.Status != types.SwapRequestStatusNotExecuted && !ctx.BlockTime().Before(req.ExpireAt) {
				if err := k.FinishSwapRequest(ctx, req, types.SwapRequestStatusExpired); err != nil {
					return false, err
				}
				return false, nil
			}
			ob.AddOrder(types.NewUserOrder(req))
			if req.Status == types.SwapRequestStatusNotExecuted {
				req.Status = types.SwapRequestStatusNotMatched
				k.SetSwapRequest(ctx, req)
			}
			skip = false
		case types.SwapRequestStatusCanceled:
		default:
			return false, fmt.Errorf("invalid swap request status: %s", req.Status)
		}
		return false, nil
	}); err != nil {
		return err
	}

	if skip { // TODO: update this when there are more than one pools
		return nil
	}

	var pools []types.PoolI
	var poolBuySources, poolSellSources []types.OrderSource
	_ = k.IteratePoolsByPair(ctx, pair.Id, func(pool types.Pool) (stop bool, err error) {
		rx, ry := k.GetPoolBalance(ctx, pool, pair)
		ps := k.GetPoolCoinSupply(ctx, pool)
		poolInfo := types.NewPoolInfo(rx, ry, ps) // Pool coin supply is not used when matching
		if types.IsDepletedPool(poolInfo) {
			k.MarkPoolAsDisabled(ctx, pool)
			return false, nil
		}
		pools = append(pools, poolInfo)

		poolReserveAddr := pool.GetReserveAddress()
		poolBuySources = append(poolBuySources, types.NewPoolOrderSource(poolInfo, pool.Id, poolReserveAddr, types.SwapDirectionBuy, tickPrec))
		poolSellSources = append(poolSellSources, types.NewPoolOrderSource(poolInfo, pool.Id, poolReserveAddr, types.SwapDirectionSell, tickPrec))
		return false, nil
	})

	buySource := types.MergeOrderSources(append(poolBuySources, ob.OrderSource(types.SwapDirectionBuy))...)
	sellSource := types.MergeOrderSources(append(poolSellSources, ob.OrderSource(types.SwapDirectionSell))...)

	engine := types.NewMatchEngine(buySource, sellSource, tickPrec)
	ob, matchPrice, quoteCoinDustAmt, matched := engine.Match()

	if matched {
		orders := ob.AllOrders()
		bulkOp := types.NewBulkSendCoinsOperation()
		for _, order := range orders {
			if order, ok := order.(*types.PoolOrder); ok {
				var offerCoinDenom string
				switch order.Direction {
				case types.SwapDirectionBuy:
					offerCoinDenom = pair.QuoteCoinDenom
				case types.SwapDirectionSell:
					offerCoinDenom = pair.BaseCoinDenom
				}
				paidCoin := sdk.NewCoin(offerCoinDenom, order.OfferCoinAmount.Sub(order.RemainingOfferCoinAmount))
				bulkOp.SendCoins(order.ReserveAddress, pair.GetEscrowAddress(), sdk.NewCoins(paidCoin))
			}
		}
		if err := bulkOp.Run(ctx, k.bankKeeper); err != nil {
			return err
		}
		bulkOp = types.NewBulkSendCoinsOperation()
		for _, order := range orders {
			switch order := order.(type) {
			case *types.UserOrder:
				var offerCoinDenom, demandCoinDenom string
				switch order.Direction {
				case types.SwapDirectionBuy:
					offerCoinDenom = pair.QuoteCoinDenom
					demandCoinDenom = pair.BaseCoinDenom
				case types.SwapDirectionSell:
					offerCoinDenom = pair.BaseCoinDenom
					demandCoinDenom = pair.QuoteCoinDenom
				}

				// TODO: optimize read/write (can there be only one write?)
				req, _ := k.GetSwapRequest(ctx, pair.Id, order.RequestId)
				req.OpenAmount = order.OpenAmount
				req.RemainingOfferCoin = sdk.NewCoin(offerCoinDenom, order.RemainingOfferCoinAmount)
				req.ReceivedCoin.Amount = req.ReceivedCoin.Amount.Add(order.ReceivedAmount)
				if order.OpenAmount.IsZero() {
					if err := k.FinishSwapRequest(ctx, req, types.SwapRequestStatusCompleted); err != nil {
						return err
					}
				} else {
					req.Status = types.SwapRequestStatusPartiallyMatched
					k.SetSwapRequest(ctx, req)
					// TODO: emit an event?
				}

				demandCoin := sdk.NewCoin(demandCoinDenom, order.ReceivedAmount)
				bulkOp.SendCoins(pair.GetEscrowAddress(), order.Orderer, sdk.NewCoins(demandCoin))
			case *types.PoolOrder:
				var demandCoinDenom string
				switch order.Direction {
				case types.SwapDirectionBuy:
					demandCoinDenom = pair.BaseCoinDenom
				case types.SwapDirectionSell:
					demandCoinDenom = pair.QuoteCoinDenom
				}
				demandCoin := sdk.NewCoin(demandCoinDenom, order.ReceivedAmount)
				bulkOp.SendCoins(pair.GetEscrowAddress(), order.ReserveAddress, sdk.NewCoins(demandCoin))
			}
		}
		bulkOp.SendCoins(pair.GetEscrowAddress(), types.DustCollectorAddress, sdk.NewCoins(sdk.NewCoin(pair.QuoteCoinDenom, quoteCoinDustAmt)))
		if err := bulkOp.Run(ctx, k.bankKeeper); err != nil {
			return err
		}

		pair.LastPrice = &matchPrice
	}

	pair.CurrentBatchId++
	k.SetPair(ctx, pair)

	// TODO: emit an event?
	_ = matchPrice
	return nil
}

func (k Keeper) FinishSwapRequest(ctx sdk.Context, req types.SwapRequest, status types.SwapRequestStatus) error {
	if req.Status.IsCanceledOrExpired() { // sanity check
		return nil
	}

	if req.RemainingOfferCoin.IsPositive() {
		pair, _ := k.GetPair(ctx, req.PairId)
		if err := k.bankKeeper.SendCoins(ctx, pair.GetEscrowAddress(), req.GetOrderer(), sdk.NewCoins(req.RemainingOfferCoin)); err != nil {
			return err
		}
	}

	req.Status = status
	k.SetSwapRequest(ctx, req)

	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			types.EventTypeOrderResult,
			sdk.NewAttribute(types.AttributeKeyRequestId, strconv.FormatUint(req.Id, 10)),
			sdk.NewAttribute(types.AttributeKeyOrderer, req.Orderer),
			sdk.NewAttribute(types.AttributeKeyPairId, strconv.FormatUint(req.PairId, 10)),
			sdk.NewAttribute(types.AttributeKeySwapDirection, req.Direction.String()),
			// TODO: include these attributes?
			//sdk.NewAttribute(types.AttributeKeyOfferCoin, req.OfferCoin.String()),
			//sdk.NewAttribute(types.AttributeKeyAmount, req.Amount.String()),
			//sdk.NewAttribute(types.AttributeKeyOpenAmount, req.OpenAmount.String()),
			//sdk.NewAttribute(types.AttributeKeyPrice, req.Price.String()),
			sdk.NewAttribute(types.AttributeKeyRemainingOfferCoin, req.RemainingOfferCoin.String()),
			sdk.NewAttribute(types.AttributeKeyReceivedCoin, req.ReceivedCoin.String()),
			sdk.NewAttribute(types.AttributeKeyStatus, req.Status.String()),
		),
	})

	return nil
}
