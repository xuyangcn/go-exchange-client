package private

import (
	"github.com/xuyangcn/go-exchange-client/models"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/mock"
	"strings"
)

type TradeFee struct {
	MakerFee float64
	TakerFee float64
}

//go:generate mockery -name=PrivateClient -output=. -inpkg
type PrivateClient interface {
	TransferFee() (map[string]float64, error)
	TradeFeeRates() (map[string]map[string]TradeFee, error)
	TradeFeeRate(string, string) (TradeFee, error)
	Balances() (map[string]float64, error)
	CompleteBalances() (map[string]*models.Balance, error)
	CompleteBalance(coin string) (*models.Balance, error)
	ActiveOrders() ([]*models.Order, error)
	IsOrderFilled(trading string, settlement string, orderNumber string) (bool, error)
	Order(trading string, settlement string,
		ordertype models.OrderType, price float64, amount float64) (string, error)
	CancelOrder(trading string, settlement string,
		ordertype models.OrderType, orderNumber string) error
	//FilledOrderInfo(orderNumber string) (models.FilledOrderInfo,error)
	Transfer(typ string, addr string,
		amount float64, additionalFee float64) error
	Address(c string) (string, error)
}

func NewClient(mode ClientMode, exchangeName string, apikey func() (string, error), seckey func() (string, error)) (PrivateClient, error) {
	if mode == TEST {
		m := new(MockPrivateClient)
		retCompleteBalance := make(map[string]*models.Balance)
		retCompleteBalance["BTC"] = &models.Balance{Available: 10000, OnOrders: 0}
		retActiveOrders := make([]*models.Order, 0)
		retTradeFeeRate := TradeFee{MakerFee: 0.002, TakerFee: 0.002}
		m.On("CompleteBalances").Return(retCompleteBalance, nil)
		m.On("CompleteBalance").Return(retCompleteBalance["BTC"], nil)
		m.On("ActiveOrders").Return(retActiveOrders, nil)
		m.On("IsOrderFilled", mock.Anything, mock.Anything).Return(true, nil)
		m.On("Order", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("12345", nil)
		m.On("CancelOrder", mock.Anything, mock.Anything).Return(nil)
		m.On("TradeFeeRate", mock.Anything, mock.Anything).Return(retTradeFeeRate, nil)
		m.On("Address", mock.Anything).Return("", nil)
		m.On("Transfer", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		return m, nil
	}
	switch strings.ToLower(exchangeName) {
	case "bitflyer":
		return NewBitflyerPrivateApi(apikey, seckey)
	case "poloniex":
		return NewPoloniexApi(apikey, seckey)
	case "hitbtc":
		return NewHitbtcApi(apikey, seckey)
	case "huobi":
		return NewHuobiApi(apikey, seckey)
	case "lbank":
		return NewLbankApi(apikey, seckey)
	case "kucoin":
		return NewKucoinApi(apikey, seckey)
	case "binance":
		return NewBinanceApi(apikey, seckey)
	}
	return nil, errors.New("failed to init exchange api")
}
