package task

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/guyuxiang/scplus-pay/model/data"
	"github.com/guyuxiang/scplus-pay/model/mdb"
	"github.com/guyuxiang/scplus-pay/model/service"
	"github.com/guyuxiang/scplus-pay/util/log"
	"github.com/gorilla/websocket"
)

// xrplWatchedAddrs holds the current set of XRPL wallet addresses to watch.
type xrplAddrSnapshot struct {
	addrs map[string]struct{}
}

var xrplWatchedAddrs atomic.Pointer[xrplAddrSnapshot]

// StartXrplListener polls every 10s until the chain is enabled with at least
// one token configured, then connects and subscribes to account transactions.
func StartXrplListener() {
	for {
		if data.IsChainEnabled(mdb.NetworkXrpl) {
			tokens, _ := data.ListEnabledChainTokensByNetwork(mdb.NetworkXrpl)
			if len(tokens) > 0 {
				runXrplListener()
			}
		}
		time.Sleep(10 * time.Second)
	}
}

func runXrplListener() {
	fingerprint := chainTokenFingerprint(mdb.NetworkXrpl)
	ctx, cancel := chainEnabledWatchdog(mdb.NetworkXrpl, "[XRPL-WS]", fingerprint)
	defer cancel()

	// Load wallet addresses and store them.
	wallets, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkXrpl)
	if err != nil {
		log.Sugar.Errorf("[XRPL-WS] load wallet addresses: %v", err)
		return
	}
	storeXrplAddresses(wallets)

	if len(xrplWatchedAddrs.Load().addrs) == 0 {
		log.Sugar.Info("[XRPL-WS] no wallet addresses configured, skipping")
		return
	}

	// Resolve WS RPC node.
	wsNode, ok := resolveChainWsNode(mdb.NetworkXrpl, "[XRPL-WS]")
	if !ok {
		return
	}
	rpcURL := strings.TrimSpace(wsNode.Url)
	log.Sugar.Infof("[XRPL-WS] connecting to %s", rpcURL)

	conn, _, dialErr := websocket.DefaultDialer.Dial(rpcURL, nil)
	if dialErr != nil {
		log.Sugar.Errorf("[XRPL-WS] dial %s: %v", rpcURL, dialErr)
		data.RecordRpcFailure(mdb.NetworkXrpl)
		return
	}
	defer conn.Close()
	data.RecordRpcSuccess(mdb.NetworkXrpl)
	log.Sugar.Infof("[XRPL-WS] connected to %s", rpcURL)

	// Subscribe to all watched addresses.
	if !xrplSubscribe(conn) {
		return
	}

	// Background ticker: refresh wallet addresses and re-subscribe on changes.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				conn.Close()
				return
			case <-ticker.C:
				w, e := data.GetAvailableWalletAddressByNetwork(mdb.NetworkXrpl)
				if e != nil {
					log.Sugar.Warnf("[XRPL-WS] refresh wallet addresses: %v", e)
					continue
				}
				storeXrplAddresses(w)
				// Re-subscribe so newly added addresses are watched.
				xrplSubscribe(conn)
			}
		}
	}()

	// Main read loop.
	for {
		select {
		case <-ctx.Done():
			log.Sugar.Info("[XRPL-WS] context cancelled, exiting")
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, msg, readErr := conn.ReadMessage()
		if readErr != nil {
			if websocket.IsCloseError(readErr, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Sugar.Infof("[XRPL-WS] connection closed normally")
			} else {
				log.Sugar.Warnf("[XRPL-WS] read error: %v", readErr)
				data.RecordRpcFailure(mdb.NetworkXrpl)
			}
			return
		}

		handleXrplMessage(msg)
	}
}

// xrplSubscribeMsg is the JSON structure sent to the XRPL node.
type xrplSubscribeMsg struct {
	ID       int      `json:"id"`
	Command  string   `json:"command"`
	Accounts []string `json:"accounts"`
}

func xrplSubscribe(conn *websocket.Conn) bool {
	snap := xrplWatchedAddrs.Load()
	if snap == nil || len(snap.addrs) == 0 {
		return true
	}
	accounts := make([]string, 0, len(snap.addrs))
	for addr := range snap.addrs {
		accounts = append(accounts, addr)
	}
	msg := xrplSubscribeMsg{
		ID:       1,
		Command:  "subscribe",
		Accounts: accounts,
	}
	payload, _ := json.Marshal(msg)
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		log.Sugar.Errorf("[XRPL-WS] subscribe write error: %v", err)
		return false
	}
	log.Sugar.Infof("[XRPL-WS] subscribed to %d address(es)", len(accounts))
	return true
}

// xrplTxNotification is the top-level structure of an account-subscription message.
type xrplTxNotification struct {
	Type        string          `json:"type"`
	Transaction xrplTransaction `json:"transaction"`
	Meta        xrplMeta        `json:"meta"`
}

type xrplTransaction struct {
	TransactionType string      `json:"TransactionType"`
	Destination     string      `json:"Destination"`
	Hash            string      `json:"hash"`
	Date            int64       `json:"date"` // Ripple epoch seconds (946684800 offset from Unix)
}

type xrplMeta struct {
	TransactionResult string          `json:"TransactionResult"`
	DeliveredAmount   json.RawMessage `json:"delivered_amount"`
}

// xrplIssuedAmount is the structure for XRPL issued-currency amounts.
type xrplIssuedAmount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
	Issuer   string `json:"issuer"`
}

// rippleEpochOffset is seconds between Unix epoch and Ripple epoch (2000-01-01).
const rippleEpochOffset = 946684800

func handleXrplMessage(raw []byte) {
	var notif xrplTxNotification
	if err := json.Unmarshal(raw, &notif); err != nil {
		return
	}
	// Only handle account transaction notifications for successful Payment txs.
	if notif.Type != "transaction" {
		return
	}
	if notif.Transaction.TransactionType != "Payment" {
		return
	}
	if notif.Meta.TransactionResult != "tesSUCCESS" {
		return
	}

	destination := strings.TrimSpace(notif.Transaction.Destination)
	if destination == "" {
		return
	}
	// Only process addresses we are watching.
	if !isWatchedXrplAddress(destination) {
		return
	}

	txHash := strings.TrimSpace(notif.Transaction.Hash)
	var blockTsMs int64
	if notif.Transaction.Date > 0 {
		blockTsMs = (notif.Transaction.Date + rippleEpochOffset) * 1000
	} else {
		blockTsMs = time.Now().UnixMilli()
	}

	// Parse delivered_amount — must be an issued-currency object (not a string, which would be XRP drops).
	if len(notif.Meta.DeliveredAmount) == 0 {
		return
	}
	// If it's a plain string it's XRP drops; skip.
	if notif.Meta.DeliveredAmount[0] == '"' {
		return
	}

	var issued xrplIssuedAmount
	if err := json.Unmarshal(notif.Meta.DeliveredAmount, &issued); err != nil {
		log.Sugar.Debugf("[XRPL-WS] parse delivered_amount: %v", err)
		return
	}
	if issued.Value == "" || issued.Currency == "" || issued.Issuer == "" {
		return
	}

	service.TryProcessXrplPayment(destination, issued.Currency, issued.Issuer, issued.Value, txHash, blockTsMs)
}

func storeXrplAddresses(wallets []mdb.WalletAddress) {
	m := make(map[string]struct{}, len(wallets))
	for _, w := range wallets {
		a := strings.TrimSpace(w.Address)
		if a != "" {
			m[a] = struct{}{}
		}
	}
	xrplWatchedAddrs.Store(&xrplAddrSnapshot{addrs: m})
}

func isWatchedXrplAddress(addr string) bool {
	snap := xrplWatchedAddrs.Load()
	if snap == nil {
		return false
	}
	_, ok := snap.addrs[addr]
	return ok
}
