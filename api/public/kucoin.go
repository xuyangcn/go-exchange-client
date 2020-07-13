package public

import (
	"net/http"
	"sync"
	"time"

	"io/ioutil"
	url2 "net/url"
	"strings"

	"github.com/antonholmquist/jason"
	"github.com/xuyangcn/go-exchange-client/api/unified"
	"github.com/xuyangcn/go-exchange-client/models"
	cache "github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
)

const (
	KUCOIN_BASE_URL = "https://api.kucoin.com"
)

func NewKucoinPublicApi() (*KucoinApi, error) {
	shrimpyApi, err := unified.NewShrimpyApi()
	if err != nil {
		return nil, err
	}
	api := &KucoinApi{
		BaseURL:           KUCOIN_BASE_URL,
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

type KucoinApi struct {
	BaseURL           string
	RateCacheDuration time.Duration
	rateLastUpdated   time.Time
	volumeMap         map[string]map[string]float64
	rateMap           map[string]map[string]float64
	orderBookTickMap  map[string]map[string]models.OrderBookTick
	precisionMap      map[string]map[string]models.Precisions
	boardCache        *cache.Cache
	currencyPairs     []models.CurrencyPair
	ShrimpyClient     *unified.ShrimpyApiClient

	HttpClient *http.Client
	rt         http.RoundTripper

	settlements []string

	m         *sync.Mutex
	rateM     *sync.Mutex
	currencyM *sync.Mutex
}

func (h *KucoinApi) SetTransport(transport http.RoundTripper) error {
	h.HttpClient.Transport = transport
	return nil
}

func (h *KucoinApi) publicApiUrl(command string) string {
	return h.BaseURL + command
}

func (h *KucoinApi) fetchSettlements() error {
	h.settlements = []string{"BTC", "ETH", "NEO", "USDT", "KCS"}
	return nil
}

func (h *KucoinApi) fetchOrderBookTick() error {
	boardMap, err := h.ShrimpyClient.GetBoards("kucoin")
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

func (h *KucoinApi) OrderBookTickMap() (map[string]map[string]models.OrderBookTick, error) {
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

type KucoinTickResponse struct {
	response   []byte
	Trading    string
	Settlement string
	err        error
}

func requestGetAsChrome(url string) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return req, err
	}
	req.Header.Add("User-Agent", "Mozilla/5.0 (Windows NT 6.3; WOW64; Trident/7.0; MAFSJS; rv:11.0) like Gecko")
	return req, err
}

func (h *KucoinApi) fetchPrecision() error {
	if h.precisionMap != nil {
		return nil
	}
	coinPrecision := make(map[string]int)
	url := h.publicApiUrl("/api/v1/currencies")
	req, err := requestGetAsChrome(url)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	resp, err := h.HttpClient.Do(req)
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
		coinPrecision[v.Get("currency").Str] = int(v.Get("precision").Int())
	}

	h.precisionMap = make(map[string]map[string]models.Precisions)

	url = h.publicApiUrl("/api/v1/market/allTickers")
	req, err = requestGetAsChrome(url)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	resp, err = h.HttpClient.Do(req)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()
	byteArray, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	value = gjson.ParseBytes(byteArray)
	for _, v := range value.Get("data.ticker").Array() {

		currencies := strings.Split(v.Get("symbol").Str, "-")
		if len(currencies) < 2 {
			continue
		}
		trading := currencies[0]
		settlement := currencies[1]
		buyPrecision := Precision(v.Get("buy").Str)
		sellPrecision := Precision(v.Get("sell").Str)
		highPrecision := Precision(v.Get("high").Str)
		lowPrecision := Precision(v.Get("low").Str)
		volPrecision := Precision(v.Get("vol").Str)
		precisionArray := []int{buyPrecision, sellPrecision, highPrecision, lowPrecision}
		maxPrecision := 0
		for _, v := range precisionArray {
			if v > maxPrecision {
				maxPrecision = v
			}
		}

		m, ok := h.precisionMap[trading]
		if !ok {
			m = make(map[string]models.Precisions)
			h.precisionMap[trading] = m
		}
		m[settlement] = models.Precisions{
			PricePrecision:  maxPrecision,
			AmountPrecision: volPrecision,
		}
	}
	return errors.Wrapf(err, "failed to fetch %s", url)
}

func (h *KucoinApi) fetchRate() error {
	url := h.publicApiUrl("/api/v1/market/allTickers")
	req, err := requestGetAsChrome(url)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	resp, err := h.HttpClient.Do(req)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()
	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	value := gjson.ParseBytes(byteArray)
	rateMap := make(map[string]map[string]float64)
	volumeMap := make(map[string]map[string]float64)
	orderBookTickMap := make(map[string]map[string]models.OrderBookTick)
	for _, v := range value.Get("data.ticker").Array() {
		currencies := strings.Split(v.Get("symbol").Str, "-")
		if len(currencies) < 2 {
			continue
		}
		trading := currencies[0]
		settlement := currencies[1]

		lastf := v.Get("last").Float()
		volumef := v.Get("vol").Float()
		bestbidPrice := v.Get("buy").Float()
		bestaskPrice := v.Get("sell").Float()

		h.rateM.Lock()
		n, ok := volumeMap[trading]
		if !ok {
			n = make(map[string]float64)
			volumeMap[trading] = n
		}
		n[settlement] = volumef
		m, ok := rateMap[trading]
		if !ok {
			m = make(map[string]float64)
			rateMap[trading] = m
		}
		m[settlement] = lastf
		l, ok := orderBookTickMap[trading]
		if !ok {
			l = make(map[string]models.OrderBookTick)
			orderBookTickMap[trading] = l
		}
		l[settlement] = models.OrderBookTick{
			BestBidPrice: bestbidPrice,
			BestAskPrice: bestaskPrice,
		}
		h.rateM.Unlock()
	}
	h.rateMap = rateMap
	h.volumeMap = volumeMap
	h.orderBookTickMap = orderBookTickMap
	return nil
}

func (h *KucoinApi) RateMap() (map[string]map[string]float64, error) {
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

func (h *KucoinApi) Precise(trading string, settlement string) (*models.Precisions, error) {
	if trading == settlement {
		return &models.Precisions{}, nil
	}

	h.fetchPrecision()
	if m, ok := h.precisionMap[trading]; !ok {
		return &models.Precisions{}, errors.Errorf("%s/%s", trading, settlement)
	} else if precisions, ok := m[settlement]; !ok {
		return &models.Precisions{}, errors.Errorf("%s/%s", trading, settlement)
	} else {
		return &precisions, nil
	}
}

func (h *KucoinApi) VolumeMap() (map[string]map[string]float64, error) {
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

func (h *KucoinApi) CurrencyPairs() ([]models.CurrencyPair, error) {
	h.currencyM.Lock()
	defer h.currencyM.Unlock()
	if len(h.currencyPairs) != 0 {
		return h.currencyPairs, nil
	}
	h.fetchSettlements()
	currecyPairs := make([]models.CurrencyPair, 0)
	url := h.publicApiUrl("/api/v1/symbols")
	req, err := requestGetAsChrome(url)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	resp, err := h.HttpClient.Do(req)
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
		trading, err := v.GetString("baseCurrency")
		if err != nil {
			continue
		}
		settlement, err := v.GetString("quoteCurrency")
		if err != nil {
			continue
		}
		currecyPairs = append(currecyPairs, models.CurrencyPair{
			Trading:    trading,
			Settlement: settlement,
		})
	}
	h.currencyPairs = currecyPairs
	return currecyPairs, nil
}

func (h *KucoinApi) Volume(trading string, settlement string) (float64, error) {
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

func (h *KucoinApi) Rate(trading string, settlement string) (float64, error) {
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

func (h *KucoinApi) FrozenCurrency() ([]string, error) {
	url := h.publicApiUrl("/api/v1/currencies")
	req, err := requestGetAsChrome(url)
	if err != nil {
		return []string{}, errors.Wrapf(err, "failed to fetch %s", url)
	}
	resp, err := h.HttpClient.Do(req)
	if err != nil {
		return []string{}, errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return []string{}, errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := jason.NewObjectFromBytes(byteArray)
	if err != nil {
		return []string{}, errors.Wrapf(err, "failed to parse json")
	}
	data, err := json.GetObjectArray("data")
	if err != nil {
		return []string{}, errors.Wrapf(err, "failed to parse json")
	}
	var frozenCurrencies []string
	for _, v := range data {
		enableWithdraw, err := v.GetBoolean("isWithdrawEnabled")
		if err != nil {
			return []string{}, errors.Wrapf(err, "failed to parse isTrading")
		}
		enableDeposit, err := v.GetBoolean("isDepositEnabled")
		if err != nil {
			return []string{}, errors.Wrapf(err, "failed to parse isTrading")
		}
		trading, err := v.GetString("currency")
		if err != nil {
			return []string{}, errors.Wrapf(err, "failed to parse object")
		}
		if !enableWithdraw || !enableDeposit {
			frozenCurrencies = append(frozenCurrencies, trading)
		}
	}
	return frozenCurrencies, nil
}

func (h *KucoinApi) Board(trading string, settlement string) (board *models.Board, err error) {
	c, found := h.boardCache.Get(trading + "_" + settlement)
	if found {
		return c.(*models.Board), nil
	}
	args := url2.Values{}
	args.Add("symbol", strings.ToUpper(trading)+"-"+strings.ToUpper(settlement))
	url := h.publicApiUrl("/api/v2/market/orderbook/level2?") + args.Encode()
	req, err := requestGetAsChrome(url)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	resp, err := h.HttpClient.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	value := gjson.ParseBytes(byteArray)
	sells := value.Get("data.asks").Array()
	buys := value.Get("data.bids").Array()

	bids := make([]models.BoardBar, 0)
	asks := make([]models.BoardBar, 0)
	for _, v := range buys {
		price := v.Array()[0].Float()
		amount := v.Array()[1].Float()
		bids = append(bids, models.BoardBar{
			Price:  price,
			Amount: amount,
			Type:   models.Bid,
		})
	}
	for _, v := range sells {
		price := v.Array()[0].Float()
		amount := v.Array()[1].Float()
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
