package public

import (
	"net/http"
	"sync"
	"time"

	"fmt"
	"github.com/antonholmquist/jason"
	"github.com/xuyangcn/go-exchange-client/api/unified"
	"github.com/xuyangcn/go-exchange-client/models"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
	"io/ioutil"
	url2 "net/url"
	"strconv"
	"strings"
)

const (
	HUOBI_BASE_URL = "https://api.huobi.pro"
)

func NewHuobiPublicApi() (*HuobiApi, error) {
	shrimpyApi, err := unified.NewShrimpyApi()
	if err != nil {
		return nil, err
	}
	api := &HuobiApi{
		BaseURL:           HUOBI_BASE_URL,
		RateCacheDuration: 3 * time.Second,
		rateMap:           nil,
		volumeMap:         nil,
		orderBookTickMap:  nil,
		rateLastUpdated:   time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		boardCache:        cache.New(3*time.Second, 1*time.Second),
		HttpClient:        &http.Client{Timeout: time.Duration(10) * time.Second},
		ShrimpyClient:     shrimpyApi,
		rt:                &http.Transport{},

		m:         new(sync.Mutex),
		rateM:     new(sync.Mutex),
		currencyM: new(sync.Mutex),
	}
	api.fetchSettlements()
	return api, nil
}

type HuobiApi struct {
	BaseURL           string
	RateCacheDuration time.Duration
	rateLastUpdated   time.Time
	volumeMap         map[string]map[string]float64
	rateMap           map[string]map[string]float64
	orderBookTickMap  map[string]map[string]models.OrderBookTick
	precisionMap      map[string]map[string]models.Precisions
	currencyPairs     []models.CurrencyPair
	boardCache        *cache.Cache

	HttpClient    *http.Client
	ShrimpyClient *unified.ShrimpyApiClient

	rt http.RoundTripper

	settlements []string

	m         *sync.Mutex
	rateM     *sync.Mutex
	currencyM *sync.Mutex
}

func (h *HuobiApi) SetTransport(transport http.RoundTripper) error {
	h.HttpClient.Transport = transport
	return nil
}

func (h *HuobiApi) publicApiUrl(command string) string {
	return h.BaseURL + command
}

func (h *HuobiApi) fetchSettlements() error {
	settlements := make([]string, 0)
	url := h.publicApiUrl("/v1/common/symbols")
	resp, err := h.HttpClient.Get(url)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := jason.NewObjectFromBytes(byteArray)
	if err != nil {
		return errors.Wrapf(err, "failed to parse json: jason err")
	}
	data, err := json.GetObjectArray("data")
	if err != nil {
		return errors.Wrapf(err, "failed to parse json: data")
	}
	for _, v := range data {
		settlement, err := v.GetString("quote-currency")
		if err != nil {
			return errors.Wrapf(err, "failed to parse json: quote-currency")
		}
		settlements = append(settlements, settlement)
	}
	m := make(map[string]bool)
	uniq := []string{}
	for _, ele := range settlements {
		if !m[ele] {
			m[ele] = true
			uniq = append(uniq, ele)
		}
	}
	h.settlements = uniq
	return nil
}

type HuobiTickResponse struct {
	response   []byte
	Trading    string
	Settlement string
	err        error
}

func (h *HuobiApi) fetchPrecision() error {
	if h.precisionMap != nil {
		return nil
	}
	h.precisionMap = make(map[string]map[string]models.Precisions)

	url := h.publicApiUrl("/v1/common/symbols")
	resp, err := h.HttpClient.Get(url)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}

	value := gjson.Parse(string(byteArray))

	for _, v := range value.Get("data").Array() {
		pricePrecision, err := strconv.Atoi(v.Get("price-precision").Raw)
		if err != nil {
			fmt.Println(err)
			continue
		}
		amountPrecision, err := strconv.Atoi(v.Get("amount-precision").Raw)
		if err != nil {
			fmt.Println(err)
			continue
		}
		trading := strings.ToUpper(v.Get("base-currency").Str)
		settlement := strings.ToUpper(v.Get("quote-currency").Str)

		m, ok := h.precisionMap[trading]
		if !ok {
			m = make(map[string]models.Precisions)
			h.precisionMap[trading] = m
		}
		m[settlement] = models.Precisions{
			PricePrecision:  pricePrecision,
			AmountPrecision: amountPrecision,
		}
	}
	return nil
}

func (h *HuobiApi) fetchRate() error {
	rateMap := make(map[string]map[string]float64)
	volumeMap := make(map[string]map[string]float64)
	orderBookTickMap := make(map[string]map[string]models.OrderBookTick)

	currencyPairs, err := h.CurrencyPairs()
	if err != nil {
		return err
	}
	ch := make(chan *HuobiTickResponse, len(currencyPairs))
	workers := make(chan int, 10)
	wg := &sync.WaitGroup{}
	for _, v := range currencyPairs {
		wg.Add(1)
		workers <- 1
		go func(trading string, settlement string) {
			defer wg.Done()
			url := h.publicApiUrl("/market/detail/merged?symbol=" + strings.ToLower(trading) + strings.ToLower(settlement))
			cli := &http.Client{Transport: h.rt}
			resp, err := cli.Get(url)
			if err != nil {
				ch <- &HuobiTickResponse{nil, trading, settlement, err}
				<-workers
				return
			}
			defer resp.Body.Close()
			byteArray, err := ioutil.ReadAll(resp.Body)
			ch <- &HuobiTickResponse{byteArray, trading, settlement, err}
			<-workers
		}(v.Trading, v.Settlement)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()

	for r := range ch {
		if r.err != nil {
			continue
		}
		data, err := jason.NewObjectFromBytes(r.response)
		if err != nil {
			continue
		}
		tick, err := data.GetObject("tick")
		if err != nil {
			continue
		}
		volume, err := tick.GetFloat64("vol")
		if err != nil {
			continue
		}
		h.rateM.Lock()
		n, ok := volumeMap[r.Trading]
		if !ok {
			n = make(map[string]float64)
			volumeMap[r.Trading] = n
		}
		n[r.Settlement] = volume
		close, err := tick.GetFloat64("close")
		if err != nil {
			continue
		}
		m, ok := rateMap[r.Trading]
		if !ok {
			m = make(map[string]float64)
			rateMap[r.Trading] = m
		}
		m[r.Settlement] = close
		ask, err := tick.GetFloat64Array("ask")
		if err != nil || len(ask) == 0 {
			continue
		}
		bid, err := tick.GetFloat64Array("bid")
		if err != nil || len(bid) == 0 {
			continue
		}
		l, ok := orderBookTickMap[r.Trading]
		if !ok {
			l = make(map[string]models.OrderBookTick)
			orderBookTickMap[r.Trading] = l
		}
		l[r.Settlement] = models.OrderBookTick{
			BestAskPrice: ask[0],
			BestBidPrice: bid[0],
		}
		h.rateM.Unlock()
	}
	h.rateMap = rateMap
	h.volumeMap = volumeMap
	h.orderBookTickMap = orderBookTickMap
	return nil
}

func (h *HuobiApi) fetchOrderBookTick() error {
	boardMap, err := h.ShrimpyClient.GetBoards("huobi")
	if err != nil {
		return err
	}
	orderBookTickMap := make(map[string]map[string]models.OrderBookTick)
	for settlement, m := range boardMap {
		for trading, value := range m {
			l, ok := orderBookTickMap[trading]
			if !ok {
				l = make(map[string]models.OrderBookTick)
				orderBookTickMap[trading] = l
			}
			l[settlement] = models.OrderBookTick{
				BestAskPrice:  value.BestAskPrice(),
				BestAskAmount: value.BestAskAmount(),
				BestBidPrice:  value.BestBidPrice(),
				BestBidAmount: value.BestBidAmount(),
			}
		}
	}
	h.orderBookTickMap = orderBookTickMap
	return nil
}

func (h *HuobiApi) OrderBookTickMap() (map[string]map[string]models.OrderBookTick, error) {
	h.m.Lock()
	defer h.m.Unlock()
	now := time.Now()
	if now.Sub(h.rateLastUpdated) >= h.RateCacheDuration {
		err := h.fetchOrderBookTick()
		if err != nil {
			return nil, err
		}
		h.rateLastUpdated = now
	}
	return h.orderBookTickMap, nil
}

func (h *HuobiApi) RateMap() (map[string]map[string]float64, error) {
	h.m.Lock()
	defer h.m.Unlock()
	now := time.Now()
	if now.Sub(h.rateLastUpdated) >= h.RateCacheDuration {
		err := h.fetchRate()
		if err != nil {
			return nil, err
		}
		h.rateLastUpdated = now
	}
	return h.rateMap, nil
}

func (h *HuobiApi) VolumeMap() (map[string]map[string]float64, error) {
	h.m.Lock()
	defer h.m.Unlock()
	now := time.Now()
	if now.Sub(h.rateLastUpdated) >= h.RateCacheDuration {
		err := h.fetchRate()
		if err != nil {
			return nil, err
		}
		h.rateLastUpdated = now
	}
	return h.volumeMap, nil
}

func (h *HuobiApi) CurrencyPairs() ([]models.CurrencyPair, error) {
	h.currencyM.Lock()
	defer h.currencyM.Unlock()
	if len(h.currencyPairs) != 0 {
		return h.currencyPairs, nil
	}
	url := h.publicApiUrl("/v1/common/symbols")
	resp, err := h.HttpClient.Get(url)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := jason.NewObjectFromBytes(byteArray)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json")
	}
	data, err := json.GetObjectArray("data")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json")
	}
	var pairs []models.CurrencyPair
	for _, v := range data {
		settlement, err := v.GetString("quote-currency")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse quote")
		}
		trading, err := v.GetString("base-currency")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse base")
		}
		pair := models.CurrencyPair{
			Trading:    strings.ToUpper(trading),
			Settlement: strings.ToUpper(settlement),
		}
		pairs = append(pairs, pair)
	}
	h.currencyPairs = pairs
	return pairs, nil
}

func (h *HuobiApi) Volume(trading string, settlement string) (float64, error) {
	h.m.Lock()
	defer h.m.Unlock()

	now := time.Now()
	if now.Sub(h.rateLastUpdated) >= h.RateCacheDuration {
		err := h.fetchRate()
		if err != nil {
			return 0, err
		}
		h.rateLastUpdated = now
	}
	if m, ok := h.volumeMap[trading]; !ok {
		return 0, errors.Errorf("%s/%s", trading, settlement)
	} else if volume, ok := m[settlement]; !ok {
		return 0, errors.Errorf("%s/%s", trading, settlement)
	} else {
		return volume, nil
	}
}

func (h *HuobiApi) Rate(trading string, settlement string) (float64, error) {
	h.m.Lock()
	defer h.m.Unlock()

	if trading == settlement {
		return 1, nil
	}

	now := time.Now()
	if now.Sub(h.rateLastUpdated) >= h.RateCacheDuration {
		err := h.fetchRate()
		if err != nil {
			return 0, err
		}
		h.rateLastUpdated = now
	}
	if m, ok := h.rateMap[trading]; !ok {
		return 0, errors.Errorf("%s/%s", trading, settlement)
	} else if rate, ok := m[settlement]; !ok {
		return 0, errors.Errorf("%s/%s", trading, settlement)
	} else {
		return rate, nil
	}
}

func (h *HuobiApi) Precise(trading string, settlement string) (*models.Precisions, error) {
	if trading == settlement {
		return &models.Precisions{}, nil
	}

	err := h.fetchPrecision()
	if err != nil {
		return &models.Precisions{}, err
	}
	if m, ok := h.precisionMap[trading]; !ok {
		return &models.Precisions{}, errors.Errorf("%s/%s", trading, settlement)
	} else if precisions, ok := m[settlement]; !ok {
		return &models.Precisions{}, errors.Errorf("%s/%s", trading, settlement)
	} else {
		return &precisions, nil
	}
}

func (h *HuobiApi) FrozenCurrency() ([]string, error) {
	var frozens []string
	args := url2.Values{}
	args.Add("language", "en-US")
	url := h.publicApiUrl("/v1/settings/currencys?") + args.Encode()
	resp, err := h.HttpClient.Get(url)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := jason.NewObjectFromBytes(byteArray)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json")
	}
	data, err := json.GetObjectArray("data")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json")
	}
	for _, v := range data {
		withdrawEnabled, err := v.GetBoolean("withdraw-enabled")
		if err != nil {
			continue
		}
		depositEnabled, err := v.GetBoolean("deposit-enabled")
		if err != nil {
			continue
		}
		if withdrawEnabled && depositEnabled {
			continue
		}
		currencyName, err := v.GetString("display-name")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse quote")
		}
		frozens = append(frozens, currencyName)
	}
	return frozens, nil
}

func (h *HuobiApi) Board(trading string, settlement string) (board *models.Board, err error) {
	c, found := h.boardCache.Get(trading + "_" + settlement)
	if found {
		return c.(*models.Board), nil
	}
	args := url2.Values{}
	args.Add("symbol", strings.ToLower(trading)+strings.ToLower(settlement))
	args.Add("type", "step0")
	url := h.publicApiUrl("/market/depth?") + args.Encode()
	resp, err := h.HttpClient.Get(url)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := jason.NewObjectFromBytes(byteArray)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json from byte array")
	}
	tick, err := json.GetObject("tick")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json by key tick")
	}
	jsonBids, err := tick.GetValueArray("bids")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json bids")
	}
	jsonAsks, err := tick.GetValueArray("asks")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json asks")
	}
	bids := make([]models.BoardBar, 0)
	asks := make([]models.BoardBar, 0)
	for _, v := range jsonBids {
		s, err := v.Array()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse array")
		}
		price, err := s[0].Float64()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse price")
		}
		amount, err := s[1].Float64()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse amount")
		}
		bids = append(bids, models.BoardBar{
			Price:  price,
			Amount: amount,
			Type:   models.Bid,
		})
	}
	for _, v := range jsonAsks {
		s, err := v.Array()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse array")
		}
		price, err := s[0].Float64()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse price")
		}
		amount, err := s[1].Float64()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse amount")
		}
		asks = append(asks, models.BoardBar{
			Price:  price,
			Amount: amount,
			Type:   models.Ask,
		})
	}
	board = &models.Board{
		Bids: bids,
		Asks: asks,
	}
	h.boardCache.Set(trading+"_"+settlement, board, cache.DefaultExpiration)
	return board, nil
}
