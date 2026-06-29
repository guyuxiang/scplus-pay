package service

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/guyuxiang/scplus-pay/model/data"
	"github.com/guyuxiang/scplus-pay/model/mdb"
	"github.com/guyuxiang/scplus-pay/model/request"
	"github.com/guyuxiang/scplus-pay/notify"
	"github.com/guyuxiang/scplus-pay/util/constant"
	"github.com/guyuxiang/scplus-pay/util/log"
	"github.com/guyuxiang/scplus-pay/util/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"
)

func resolveTronNode() (string, string, error) {
	node, err := ResolveTronRpcNode()
	if err != nil {
		return "", "", err
	}
	rpcURL := strings.TrimRight(strings.TrimSpace(node.Url), "/")
	return rpcURL, node.ApiKey, nil
}

func ResolveTronRpcNode(excludeIDs ...uint64) (*mdb.RpcNode, error) {
	node, err := data.SelectGeneralRpcNode(mdb.NetworkTron, mdb.RpcNodeTypeHttp, excludeIDs...)
	if err != nil {
		return nil, err
	}
	if node == nil || node.ID == 0 {
		return nil, fmt.Errorf("no enabled %s %s RPC node configured in rpc_nodes", mdb.NetworkTron, mdb.RpcNodeTypeHttp)
	}
	rpcURL := strings.TrimRight(strings.TrimSpace(node.Url), "/")
	if rpcURL == "" {
		return nil, fmt.Errorf("rpc_nodes id=%d has empty url", node.ID)
	}
	node.Url = rpcURL
	return node, nil
}

func ResolveTronNode() (string, string, error) {
	return resolveTronNode()
}

func TryProcessTronTRC20Transfer(token mdb.ChainToken, toAddr string, rawValue *big.Int, txHash string, blockTsMs int64) {
	tokenSym := strings.ToUpper(strings.TrimSpace(token.Symbol))
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[TRC20-%s][%s] TryProcessTronTRC20Transfer panic: %v", tokenSym, toAddr, err)
		}
	}()

	addr := strings.TrimSpace(toAddr)
	if tokenSym == "" || addr == "" || rawValue == nil || rawValue.Sign() <= 0 {
		return
	}
	decimals := token.Decimals
	if decimals < 0 {
		decimals = 0
	}

	decimalQuant := decimal.NewFromBigInt(rawValue, 0)
	amount := math.MustParsePrecFloat64(decimalQuant.Div(decimal.New(1, int32(decimals))).InexactFloat64(), data.MaxAmountPrecision)
	if amount <= 0 {
		return
	}
	if token.MinAmount > 0 && amount < token.MinAmount {
		log.Sugar.Debugf("[TRC20-%s][%s] skip below min amount hash=%s amount=%.2f min=%.2f", tokenSym, addr, txHash, amount, token.MinAmount)
		return
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, addr, tokenSym, amount)
	if err != nil {
		log.Sugar.Warnf("[TRC20-%s][%s] lock lookup: %v", tokenSym, addr, err)
		return
	}
	if tradeID == "" {
		log.Sugar.Debugf("[TRC20-%s][%s] skip unmatched tx hash=%s amount=%.2f", tokenSym, addr, txHash, amount)
		return
	}

	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Warnf("[TRC20-%s][%s] load order: %v", tokenSym, addr, err)
		return
	}
	if strings.ToLower(strings.TrimSpace(order.Network)) != mdb.NetworkTron {
		log.Sugar.Warnf("[TRC20-%s][%s] skip trade_id=%s network=%q", tokenSym, addr, tradeID, order.Network)
		return
	}
	if strings.ToUpper(strings.TrimSpace(order.Token)) != tokenSym {
		log.Sugar.Warnf("[TRC20-%s][%s] skip trade_id=%s token mismatch order=%s", tokenSym, addr, tradeID, order.Token)
		return
	}
	if blockTsMs > 0 && blockTsMs < order.CreatedAt.TimestampMilli() {
		log.Sugar.Warnf("[TRC20-%s][%s] skip tx %s because block time %d is before order create time %d", tokenSym, addr, txHash, blockTsMs, order.CreatedAt.TimestampMilli())
		return
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     addr,
		Token:              tokenSym,
		Network:            mdb.NetworkTron,
		TradeId:            tradeID,
		Amount:             amount,
		BlockTransactionId: txHash,
	}
	err = OrderProcessing(req)
	if err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[TRC20-%s][%s] skip resolved transfer trade_id=%s hash=%s err=%v", tokenSym, addr, tradeID, txHash, err)
			return
		}
		log.Sugar.Errorf("[TRC20-%s][%s] OrderProcessing trade_id=%s hash=%s: %v", tokenSym, addr, tradeID, txHash, err)
		return
	}

	sendPaymentNotification(order)
	log.Sugar.Infof("[TRC20-%s][%s] payment processed trade_id=%s hash=%s", tokenSym, addr, tradeID, txHash)
}

func TryProcessTronTRXTransfer(toAddr string, rawSun int64, txHash string, blockTsMs int64) {
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[TRX][%s] TryProcessTronTRXTransfer panic: %v", toAddr, err)
		}
	}()

	addr := strings.TrimSpace(toAddr)
	if addr == "" || rawSun <= 0 {
		return
	}

	decimalQuant := decimal.NewFromInt(rawSun)
	amount := math.MustParsePrecFloat64(decimalQuant.Div(decimal.NewFromInt(1_000_000)).InexactFloat64(), data.MaxAmountPrecision)
	if amount <= 0 {
		return
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, addr, "TRX", amount)
	if err != nil {
		log.Sugar.Warnf("[TRX][%s] lock lookup: %v", addr, err)
		return
	}
	if tradeID == "" {
		log.Sugar.Debugf("[TRX][%s] skip unmatched tx hash=%s amount=%.2f", addr, txHash, amount)
		return
	}

	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Warnf("[TRX][%s] load order: %v", addr, err)
		return
	}
	if blockTsMs > 0 && blockTsMs < order.CreatedAt.TimestampMilli() {
		log.Sugar.Warnf("[TRX][%s] skip tx %s because block time %d is before order create time %d", addr, txHash, blockTsMs, order.CreatedAt.TimestampMilli())
		return
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     addr,
		Token:              "TRX",
		Network:            mdb.NetworkTron,
		TradeId:            tradeID,
		Amount:             amount,
		BlockTransactionId: txHash,
	}
	err = OrderProcessing(req)
	if err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[TRX][%s] skip resolved transfer trade_id=%s hash=%s err=%v", addr, tradeID, txHash, err)
			return
		}
		log.Sugar.Errorf("[TRX][%s] OrderProcessing trade_id=%s hash=%s: %v", addr, tradeID, txHash, err)
		return
	}

	sendPaymentNotification(order)
	log.Sugar.Infof("[TRX][%s] payment processed trade_id=%s hash=%s", addr, tradeID, txHash)
}

func evmChainLogLabel(chainNetwork string) string {
	switch chainNetwork {
	case mdb.NetworkEthereum:
		return "ETH"
	case mdb.NetworkBsc:
		return "BSC"
	case mdb.NetworkPolygon:
		return "POLYGON"
	case mdb.NetworkPlasma:
		return "PLASMA"
	default:
		return "EVM"
	}
}

func TryProcessEvmERC20Transfer(chainNetwork string, contract common.Address, toAddr common.Address, rawValue *big.Int, txHash string, blockTsMs int64) {
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[%s-WS] TryProcessEvmERC20Transfer panic: %v", evmChainLogLabel(chainNetwork), err)
		}
	}()

	net := evmChainLogLabel(chainNetwork)
	tokenConfig, err := data.GetEnabledChainTokenByContract(chainNetwork, contract.Hex())
	if err != nil {
		log.Sugar.Warnf("[%s-WS] load chain token contract=%s: %v", net, contract.Hex(), err)
		return
	}
	if tokenConfig == nil || tokenConfig.ID == 0 {
		log.Sugar.Warnf("[%s-WS] skip unconfigured contract %s", net, contract.Hex())
		return
	}
	tokenSym := strings.ToUpper(strings.TrimSpace(tokenConfig.Symbol))
	if tokenSym == "" {
		log.Sugar.Warnf("[%s-WS] skip contract %s with empty token symbol", net, contract.Hex())
		return
	}
	walletAddr := strings.ToLower(toAddr.Hex())
	if rawValue == nil || rawValue.Sign() <= 0 {
		log.Sugar.Infof("[%s-%s][%s] skip non-positive or nil amount", net, tokenSym, walletAddr)
		return
	}
	decimals := tokenConfig.Decimals
	if decimals < 0 {
		decimals = 0
	}
	pow := decimal.New(1, int32(decimals))

	decimalQuant := decimal.NewFromBigInt(rawValue, 0)
	amount := math.MustParsePrecFloat64(decimalQuant.Div(pow).InexactFloat64(), data.MaxAmountPrecision)
	if amount <= 0 {
		log.Sugar.Warnf("[%s-%s][%s] skip non-positive amount %.2f", net, tokenSym, walletAddr, amount)
		return
	}
	if tokenConfig.MinAmount > 0 && amount < tokenConfig.MinAmount {
		log.Sugar.Debugf("[%s-%s][%s] skip below min amount hash=%s amount=%.2f min=%.2f", net, tokenSym, walletAddr, txHash, amount, tokenConfig.MinAmount)
		return
	}

	log.Sugar.Debugf("[%s-%s][%s] processing transfer hash=%s amount=%.2f", net, tokenSym, walletAddr, txHash, amount)

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(chainNetwork, walletAddr, tokenSym, amount)
	if err != nil {
		log.Sugar.Warnf("[%s-%s][%s] lock lookup: %v", net, tokenSym, walletAddr, err)
		return
	}
	if tradeID == "" {
		log.Sugar.Warnf("[%s-%s][%s] skip unmatched tx hash=%s amount=%.2f", net, tokenSym, walletAddr, txHash, amount)
		return
	}

	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Warnf("[%s-%s][%s] load order: %v", net, tokenSym, walletAddr, err)
		return
	}
	if strings.ToLower(strings.TrimSpace(order.Network)) != chainNetwork {
		log.Sugar.Warnf("[%s-%s][%s] skip trade_id=%s network=%q", net, tokenSym, walletAddr, tradeID, order.Network)
		return
	}
	if strings.ToUpper(strings.TrimSpace(order.Token)) != tokenSym {
		log.Sugar.Warnf("[%s-%s][%s] skip trade_id=%s token mismatch order=%s", net, tokenSym, walletAddr, tradeID, order.Token)
		return
	}
	if blockTsMs > 0 && blockTsMs < order.CreatedAt.TimestampMilli() {
		log.Sugar.Warnf("[%s-%s][%s] skip tx %s because block time %d is before order create time %d", net, tokenSym, walletAddr, txHash, blockTsMs, order.CreatedAt.TimestampMilli())
		return
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     walletAddr,
		Token:              tokenSym,
		Network:            chainNetwork,
		TradeId:            tradeID,
		Amount:             amount,
		BlockTransactionId: txHash,
	}
	err = OrderProcessing(req)
	if err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[%s-%s][%s] skip resolved trade_id=%s hash=%s err=%v", net, tokenSym, walletAddr, tradeID, txHash, err)
			return
		}
		log.Sugar.Errorf("[%s-%s][%s] OrderProcessing: %v", net, tokenSym, walletAddr, err)
		return
	}

	sendPaymentNotification(order)
	log.Sugar.Infof("[%s-%s][%s] payment processed trade_id=%s hash=%s", net, tokenSym, walletAddr, tradeID, txHash)
}

// TryProcessXrplPayment handles an XRPL issued-currency Payment delivered to
// one of our watched addresses. valueStr is the human-readable delivered
// amount (e.g. "1.5"), currency and issuer identify the token.
func TryProcessXrplPayment(toAddr, currency, issuer, valueStr, txHash string, blockTsMs int64) {
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[XRPL-%s][%s] TryProcessXrplPayment panic: %v", currency, toAddr, err)
		}
	}()

	toAddr = strings.TrimSpace(toAddr)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	issuer = strings.TrimSpace(issuer)
	if toAddr == "" || currency == "" || issuer == "" || valueStr == "" {
		return
	}

	// Verify the token is configured and the issuer matches.
	tokenConfig, err := data.GetEnabledChainTokenByContract(mdb.NetworkXrpl, issuer)
	if err != nil || tokenConfig == nil || tokenConfig.ID == 0 {
		log.Sugar.Debugf("[XRPL-%s][%s] skip unconfigured issuer %s", currency, toAddr, issuer)
		return
	}
	if strings.ToUpper(strings.TrimSpace(tokenConfig.Symbol)) != currency {
		log.Sugar.Debugf("[XRPL-%s][%s] skip issuer %s symbol mismatch (got %s)", currency, toAddr, issuer, tokenConfig.Symbol)
		return
	}

	// XRPL issued-currency amounts are already in human-readable units.
	amountDec, err2 := decimal.NewFromString(valueStr)
	if err2 != nil || amountDec.IsZero() || amountDec.IsNegative() {
		log.Sugar.Warnf("[XRPL-%s][%s] invalid amount %q: %v", currency, toAddr, valueStr, err2)
		return
	}
	amount := math.MustParsePrecFloat64(amountDec.InexactFloat64(), data.MaxAmountPrecision)
	if amount <= 0 {
		return
	}
	if tokenConfig.MinAmount > 0 && amount < tokenConfig.MinAmount {
		log.Sugar.Debugf("[XRPL-%s][%s] skip below min amount hash=%s amount=%.6f min=%.6f", currency, toAddr, txHash, amount, tokenConfig.MinAmount)
		return
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkXrpl, toAddr, currency, amount)
	if err != nil {
		log.Sugar.Warnf("[XRPL-%s][%s] lock lookup: %v", currency, toAddr, err)
		return
	}
	if tradeID == "" {
		log.Sugar.Debugf("[XRPL-%s][%s] skip unmatched tx hash=%s amount=%.6f", currency, toAddr, txHash, amount)
		return
	}

	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Warnf("[XRPL-%s][%s] load order: %v", currency, toAddr, err)
		return
	}
	if strings.ToLower(strings.TrimSpace(order.Network)) != mdb.NetworkXrpl {
		log.Sugar.Warnf("[XRPL-%s][%s] skip trade_id=%s network mismatch %q", currency, toAddr, tradeID, order.Network)
		return
	}
	if strings.ToUpper(strings.TrimSpace(order.Token)) != currency {
		log.Sugar.Warnf("[XRPL-%s][%s] skip trade_id=%s token mismatch order=%s", currency, toAddr, tradeID, order.Token)
		return
	}
	if blockTsMs > 0 && blockTsMs < order.CreatedAt.TimestampMilli() {
		log.Sugar.Warnf("[XRPL-%s][%s] skip tx %s block time %d before order created %d", currency, toAddr, txHash, blockTsMs, order.CreatedAt.TimestampMilli())
		return
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     toAddr,
		Token:              currency,
		Network:            mdb.NetworkXrpl,
		TradeId:            tradeID,
		Amount:             amount,
		BlockTransactionId: txHash,
	}
	if err = OrderProcessing(req); err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[XRPL-%s][%s] skip resolved trade_id=%s hash=%s: %v", currency, toAddr, tradeID, txHash, err)
			return
		}
		log.Sugar.Errorf("[XRPL-%s][%s] OrderProcessing trade_id=%s hash=%s: %v", currency, toAddr, tradeID, txHash, err)
		return
	}

	sendPaymentNotification(order)
	log.Sugar.Infof("[XRPL-%s][%s] payment processed trade_id=%s hash=%s amount=%.6f", currency, toAddr, tradeID, txHash, amount)
}

func sendPaymentNotification(order *mdb.Orders) {
	if order == nil {
		return
	}
	if strings.TrimSpace(order.TradeId) != "" {
		latest, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil {
			log.Sugar.Warnf("[notify] reload order failed trade_id=%s err=%v", order.TradeId, err)
		} else if latest != nil && latest.TradeId != "" {
			order = latest
		}
	}

	precision := data.GetAmountPrecision()
	amountFormat := fmt.Sprintf("%%.%df", precision)
	msg := fmt.Sprintf(
		"🎉 <b>收款成功通知</b>\n\n"+
			"💰 <b>金额信息</b>\n"+
			"├ 订单金额：<code>"+amountFormat+" %s</code>\n"+
			"└ 实际到账：<code>"+amountFormat+" %s</code>\n\n"+
			"📋 <b>订单信息</b>\n"+
			"├ 交易号：<code>%s</code>\n"+
			"├ 订单号：<code>%s</code>\n"+
			"├ 网络：<code>%s</code>\n"+
			"└ 钱包地址：<code>%s</code>\n\n"+
			"⏰ <b>时间信息</b>\n"+
			"├ 创建时间：%s\n"+
			"└ 支付时间：%s",
		order.Amount,
		strings.ToUpper(order.Currency),
		order.ActualAmount,
		strings.ToUpper(order.Token),
		order.TradeId,
		order.OrderId,
		networkDisplay(order.Network),
		order.ReceiveAddress,
		order.CreatedAt.ToDateTimeString(),
		order.UpdatedAt.ToDateTimeString(),
	)
	notify.Dispatch(mdb.NotifyEventPaySuccess, msg)
}

func networkDisplay(n string) string {
	switch strings.ToLower(strings.TrimSpace(n)) {
	case mdb.NetworkTron:
		return "Tron"
	case mdb.NetworkSolana:
		return "Solana"
	case mdb.NetworkEthereum:
		return "Ethereum"
	case mdb.NetworkBsc:
		return "BSC"
	case mdb.NetworkPolygon:
		return "Polygon"
	case mdb.NetworkPlasma:
		return "Plasma"
	default:
		if n == "" {
			return "Tron"
		}
		return strings.ToUpper(n)
	}
}
