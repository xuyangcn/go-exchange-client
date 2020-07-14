# Go-ccex-api-Client - a Client for  Bitcoin exchanges

[![GoDoc](https://img.shields.io/badge/api-Godoc-blue.svg?style=flat-square)](https://godoc.org/github.com/xuyangcn/go-ccex-api-client)
[![Build Status](https://travis-ci.org/xuyangcn/go-ccex-api-client.svg?branch=master&time=now)](https://travis-ci.org/xuyangcn/go-ccex-api-client)
[![Coverage Status](https://coveralls.io/repos/github/xuyangcn/go-ccex-api-client/badge.svg?branch=master)](https://coveralls.io/github/xuyangcn/go-ccex-api-client?branch=master&time=now)
[![Maintenance](https://img.shields.io/badge/Maintained%3F-yes-green.svg)](https://GitHub.com/Naereen/StrapDown.js/graphs/commit-activity)

This package presents a client for cryptocoin exchange api.

## Example

```go
package main

import (
	"fmt"
	"github.com/xuyangcn/go-exchange-client/api/public"
	"github.com/xuyangcn/go-exchange-client/api/private"
	"github.com/xuyangcn/go-exchange-client/models"
)

func main() {
	bitflyerPublicApi,err := public.NewClient("bitflyer")
	if err != nil {
    		panic(err)
    }
    currencyPairs,err := bitflyerPublicApi.CurrencyPairs()
    if err != nil {
    		panic(err)
    }
    for _,v := range currencyPairs {
    	fmt.Println(bitflyerPublicApi.Rate(v.Trading,v.Settlement))
    	fmt.Println(bitflyerPublicApi.Volume(v.Trading,v.Settlement))
    }
    
    bitflyerPrivateApi,err := private.NewClient("bitflyer","APIKEY","SECRETKEY")
    bitflyerPrivateApi.Balances()
    bitflyerPrivateApi.Order("BTC","USDT",models.Bid,10000.0,1)
}
```

## API Documents

- Bitflyer : https://lightning.bitflyer.jp/docs?lang=ja
- Poloniex : https://poloniex.com/support/api/
- Hitbtc : https://api.hitbtc.com/
- Huobi : https://github.com/huobiapi/API_Docs_en/wiki/REST_Reference


## PublicAPI

|           | fetchRate() | Volume() | CurrencyPairs() | Rate() | FrozenCurrency() | Board() |
|-----------|-------------|----------|-----------------|--------|------------------|---------|
| Bitflyer  | Done        | Done     | Done            | Done   | Done             | Done    |
| Poloniex  | Done        | Done     | Done            | Done   | Done             | Done    |
| Hitbtc    | Done        | Done     | Done            | Done   | Done             | Done    |
| Huobi     | Done        | Done     | Done            | Done   | Done             | Done    |
| Okex      | Done        | Done     | Done            | Done   | Done             | Done    |
| Cobinhood | Done        | Done     | Done            | Done   | Done             | Done    |
| Lbank     | Done        | Done     | Done            | Done   | Done             | Done    |
| Kucoin    | Done        | Done     | Done            | Done   | Done             | Done    |

## PrivateAPI

|          | Order() | CancelOrder() | SellFeeRate() | PurchaseFeeRate() | Balances() | CompleteBalances() | ActiveOrders() | TransferFee() | Transfer() | Address() | Precise() |
|----------|---------|---------------|---------------|-------------------|------------|--------------------|----------------|---------------|------------|-----------|-----------|
| Bitflyer | Done    | Done          | Done          | Done              | Done       | Done               | Done           | Done          | Done       | Done      | Done      |
| Poloniex | Done    | Done          | Done          | Done              | Done       | Done               | Done           | Done          | Done       | Done      | Done      |
| Hitbtc   | Done    | Done          | Done          | Done              | Done       | Done               | Done           | Done          | Done       | Done      | Done      |
| Huobi    | Done    | Done          | Done          | Done              | Done       | Done               | Done           | Done          | Done       | Done      | Done      |
| Lbank    | Done    | Done          | Done          | Done              | Done       | Done               | Done           | Done          | Done       | Done      | Done      |
| Kucoin   | Done    | Done          | Done          | Done              | Done       | Done               | Done           | Done          | Done       | Done      | Done      |
