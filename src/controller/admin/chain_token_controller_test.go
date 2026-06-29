package admin

import (
	"testing"

	"github.com/guyuxiang/scplus-pay/model/mdb"
	"github.com/guyuxiang/scplus-pay/util/constant"
)

func TestValidateChainTokenPaymentConfigRejectsEnabledAptosTokenWithoutAssetID(t *testing.T) {
	for _, row := range []mdb.ChainToken{
		{Network: mdb.NetworkAptos, Symbol: "USDT", Enabled: true},
	} {
		if err := validateChainTokenPaymentConfig(&row); err != constant.ParamsMarshalErr {
			t.Fatalf("validateChainTokenPaymentConfig(%+v) = %v, want %v", row, err, constant.ParamsMarshalErr)
		}
	}
}

func TestValidateChainTokenPaymentConfigAllowsDisabledAptosTokenWithoutAssetID(t *testing.T) {
	row := mdb.ChainToken{Network: mdb.NetworkAptos, Symbol: "USDT", Enabled: false}
	if err := validateChainTokenPaymentConfig(&row); err != nil {
		t.Fatalf("validateChainTokenPaymentConfig(%+v) = %v, want nil", row, err)
	}
}
