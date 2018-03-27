package models

import (
	"github.com/pkg/errors"
	"sort"
)

type BoardOrder struct {
	Type   OrderType
	Price  float64
	Amount float64
}

type Board struct {
	Asks []BoardOrder
	Bids []BoardOrder
}

func (b *Board) BestBuyAmount() float64 {
	sort.Slice(b.Bids, func(i, j int) bool {
		return b.Bids[i].Price > b.Bids[j].Price
	})
	if len(b.Bids) == 0 {
		return 0
	}
	return b.Bids[0].Amount
}

func (b *Board) BestSellAmount() float64 {
	sort.Slice(b.Asks, func(i, j int) bool {
		return b.Asks[i].Price < b.Asks[j].Price
	})
	if len(b.Asks) == 0 {
		return 0
	}
	return b.Asks[0].Amount
}

func (b *Board) BestBuyPrice() float64 {
	sort.Slice(b.Bids, func(i, j int) bool {
		return b.Bids[i].Price > b.Bids[j].Price
	})
	if len(b.Bids) == 0 {
		return 0
	}
	return b.Bids[0].Price
}

func (b *Board) BestSellPrice() float64 {
	sort.Slice(b.Asks, func(i, j int) bool {
		return b.Asks[i].Price < b.Asks[j].Price
	})
	if len(b.Asks) == 0 {
		return 0
	}
	return b.Asks[0].Price
}

func (b *Board) AverageBuyRate(amount float64) (float64, error) {
	sort.Slice(b.Bids, func(i, j int) bool {
		return b.Bids[i].Price < b.Bids[j].Price
	})
	if len(b.Bids) == 0 {
		return 0, errors.New("there is no bids")
	}
	var sum float64
	remainingAmount := amount
	for _, v := range b.Bids {
		if v.Amount > remainingAmount {
			sum += remainingAmount * v.Price
			return sum / amount, nil
		} else {
			sum += v.Amount * v.Price
			remainingAmount = remainingAmount - v.Amount
		}
	}
	return 0, errors.New("there is not enough board orders")
}

func (b *Board) AverageSellRate(amount float64) (float64, error) {
	sort.Slice(b.Asks, func(i, j int) bool {
		return b.Asks[i].Price < b.Asks[j].Price
	})
	if len(b.Asks) == 0 {
		return 0, errors.New("there is no asks")
	}
	var sum float64
	remainingAmount := amount
	for _, v := range b.Asks {
		if v.Amount > remainingAmount {
			sum += remainingAmount * v.Price
			return sum / amount, nil
		} else {
			sum += v.Amount * v.Price
			remainingAmount = remainingAmount - v.Amount
		}
	}
	return 0, errors.New("there is not enough board orders")
}
