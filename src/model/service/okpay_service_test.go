package service

import (
	"strings"
	"testing"
	"time"

	"github.com/guyuxiang/scplus-pay/internal/testutil"
	"github.com/guyuxiang/scplus-pay/model/dao"
	"github.com/guyuxiang/scplus-pay/model/data"
	"github.com/guyuxiang/scplus-pay/model/mdb"
	"github.com/guyuxiang/scplus-pay/notify"
)

func TestHandleOkPayNotifySendsPaymentNotificationOnSuccess(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const (
		channelType     = "test-okpay-pay-success"
		shopID          = "okpay-shop-notify"
		shopToken       = "okpay-shop-token"
		tradeID         = "okpay-notify-success-001"
		orderID         = "merchant-okpay-notify-success-001"
		providerOrderID = "okpay-provider-success-001"
		rawFormData     = "raw-okpay-form"
	)

	got := make(chan string, 1)
	notify.RegisterSender(channelType, func(config, text string) error {
		got <- text
		return nil
	})

	if err := dao.Mdb.Create(&mdb.NotificationChannel{
		Type:    channelType,
		Name:    "test okpay pay success",
		Config:  "{}",
		Events:  `{"pay_success":true}`,
		Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed notification channel: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopID, shopID, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay shop id: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopToken, shopToken, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay shop token: %v", err)
	}

	order := &mdb.Orders{
		TradeId:        tradeID,
		OrderId:        orderID,
		Amount:         1,
		Currency:       "CNY",
		ActualAmount:   0.15,
		ReceiveAddress: "OKPAY",
		Token:          "USDT",
		Network:        mdb.NetworkTron,
		Status:         mdb.StatusWaitPay,
		PaymentType:    mdb.PaymentTypeEpay,
		PayProvider:    mdb.PaymentProviderOkPay,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.ProviderOrder{
		TradeId:         tradeID,
		Provider:        mdb.PaymentProviderOkPay,
		ProviderOrderID: providerOrderID,
		Amount:          0.15,
		Coin:            "USDT",
		Status:          mdb.ProviderOrderStatusPending,
	}).Error; err != nil {
		t.Fatalf("seed provider order: %v", err)
	}

	form := okPayNotifyTestForm(shopID, shopToken, providerOrderID, tradeID, "0.15000000", "USDT")
	if err := HandleOkPayNotify(form, rawFormData); err != nil {
		t.Fatalf("handle okpay notify: %v", err)
	}

	select {
	case text := <-got:
		if !strings.Contains(text, tradeID) {
			t.Fatalf("notification text = %q, want trade id %s", text, tradeID)
		}
		if !strings.Contains(text, orderID) {
			t.Fatalf("notification text = %q, want order id %s", text, orderID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for payment notification")
	}

	if err := HandleOkPayNotify(form, rawFormData); err != nil {
		t.Fatalf("handle duplicate okpay notify: %v", err)
	}
	select {
	case text := <-got:
		t.Fatalf("duplicate notification sent: %q", text)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandleOkPayNotifySettlesPlaceholderParent(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const (
		shopID          = "okpay-shop-placeholder"
		shopToken       = "okpay-shop-placeholder-token"
		parentTradeID   = "okpay-placeholder-parent-001"
		providerOrderID = "okpay-placeholder-provider-001"
	)

	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopID, shopID, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay shop id: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopToken, shopToken, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay shop token: %v", err)
	}

	parent := &mdb.Orders{
		TradeId:        parentTradeID,
		OrderId:        "merchant-okpay-placeholder-parent-001",
		Amount:         1,
		Currency:       "CNY",
		ActualAmount:   0.15,
		ReceiveAddress: "OKPAY",
		Token:          "USDT",
		Network:        mdb.PaymentProviderOkPay,
		Status:         mdb.StatusWaitPay,
		IsSelected:     true,
		PaymentType:    mdb.PaymentTypeGmpay,
		PayProvider:    mdb.PaymentProviderOkPay,
	}
	if err := dao.Mdb.Create(parent).Error; err != nil {
		t.Fatalf("seed parent order: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.ProviderOrder{
		TradeId:         parentTradeID,
		Provider:        mdb.PaymentProviderOkPay,
		ProviderOrderID: providerOrderID,
		Amount:          0.15,
		Coin:            "USDT",
		Status:          mdb.ProviderOrderStatusPending,
	}).Error; err != nil {
		t.Fatalf("seed provider order: %v", err)
	}

	form := okPayNotifyTestForm(shopID, shopToken, providerOrderID, parentTradeID, "0.15000000", "USDT")
	if err := HandleOkPayNotify(form, "placeholder-okpay-form"); err != nil {
		t.Fatalf("handle okpay notify: %v", err)
	}

	reloadedParent, err := data.GetOrderInfoByTradeId(parentTradeID)
	if err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if reloadedParent.Status != mdb.StatusPaySuccess {
		t.Fatalf("parent status = %d, want %d", reloadedParent.Status, mdb.StatusPaySuccess)
	}
	if reloadedParent.PayBySubId != 0 {
		t.Fatalf("parent pay_by_sub_id = %d, want 0", reloadedParent.PayBySubId)
	}
	if reloadedParent.Token != "USDT" || reloadedParent.Network != mdb.PaymentProviderOkPay || reloadedParent.ReceiveAddress != "OKPAY" || reloadedParent.ActualAmount != 0.15 {
		t.Fatalf("okpay parent fields changed: token=%q network=%q address=%q actual=%v", reloadedParent.Token, reloadedParent.Network, reloadedParent.ReceiveAddress, reloadedParent.ActualAmount)
	}
	providerRow, err := data.GetProviderOrderByTradeIDAndProvider(parentTradeID, mdb.PaymentProviderOkPay)
	if err != nil {
		t.Fatalf("reload provider row: %v", err)
	}
	if providerRow.Status != mdb.ProviderOrderStatusPaid {
		t.Fatalf("provider status = %q, want %q", providerRow.Status, mdb.ProviderOrderStatusPaid)
	}
}

func okPayNotifyTestForm(shopID, shopToken, providerOrderID, tradeID, amount, coin string) map[string]string {
	form := map[string]string{
		"code":              "200",
		"id":                shopID,
		"status":            "success",
		"data[order_id]":    providerOrderID,
		"data[unique_id]":   tradeID,
		"data[pay_user_id]": "123456789",
		"data[amount]":      amount,
		"data[coin]":        coin,
		"data[status]":      "1",
		"data[type]":        "deposit",
	}
	form["sign"] = okPayNotifySign(form, shopToken)
	return form
}
