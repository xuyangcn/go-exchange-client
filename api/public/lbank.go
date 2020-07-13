package public

import (
	"net/http"
	"sync"
	"time"

	"github.com/antonholmquist/jason"
	"github.com/xuyangcn/go-exchange-client/models"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
	"io/ioutil"
	url2 "net/url"
	"strings"
)

const (
	LBANK_BASE_URL = "https://api.lbkex.com"
)

func NewLbankPublicApi() (*LbankApi, error) {
	api := &LbankApi{
		BaseURL:           LBANK_BASE_URL,
		RateCacheDuration: 3 * time.Second,
		rateMap:           nil,
		volumeMap:         nil,
		orderBookTickMap:  nil,
		rateLastUpdated:   time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		boardCache:        cache.New(3*time.Second, 1*time.Second),

		HttpClient: &http.Client{},
		rt:         &http.Transport{},

		m:         new(sync.Mutex),
		rateM:     new(sync.Mutex),
		currencyM: new(sync.Mutex),
	}
	api.fetchSettlements()
	return api, nil
}

type LbankApi struct {
	BaseURL           string
	RateCacheDuration time.Duration
	rateLastUpdated   time.Time
	volumeMap         map[string]map[string]float64
	rateMap           map[string]map[string]float64
	orderBookTickMap  map[string]map[string]models.OrderBookTick
	precisionMap      map[string]map[string]models.Precisions
	currencyPairs     []models.CurrencyPair
	boardCache        *cache.Cache

	HttpClient *http.Client
	rt         http.RoundTripper

	settlements []string

	m         *sync.Mutex
	rateM     *sync.Mutex
	currencyM *sync.Mutex
}

func (h *LbankApi) SetTransport(transport http.RoundTripper) error {
	h.HttpClient.Transport = transport
	return nil
}

func (h *LbankApi) publicApiUrl(command string) string {
	return h.BaseURL + command
}

func (h *LbankApi) fetchSettlements() error {
	pairs, err := h.CurrencyPairs()
	if err != nil {
		return errors.Wrap(err, "failed to fetch settlements")
	}
	m := make(map[string]bool)
	uniq := []string{}
	for _, ele := range pairs {
		if !m[ele.Settlement] {
			m[ele.Settlement] = true
			uniq = append(uniq, ele.Settlement)
		}
	}
	h.settlements = uniq
	return nil
}

type LbankTickResponse struct {
	response   []byte
	Trading    string
	Settlement string
	err        error
}

func (h *LbankApi) fetchPrecision() error {
	if h.precisionMap != nil {
		return nil
	}
	h.precisionMap = make(map[string]map[string]models.Precisions)

	url := h.publicApiUrl("/v1/ticker.do") + "?symbol=all"
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

	for _, v := range value.Array() {
		pairString := v.Get("symbol").Str
		last := v.Get("ticker.latest").Raw
		volume := v.Get("ticker.vol").Raw

		currencies := strings.Split(pairString, "_")
		if len(currencies) != 2 {
			continue
		}
		trading := strings.ToUpper(currencies[0])
		settlement := strings.ToUpper(currencies[1])

		m, ok := h.precisionMap[trading]
		if !ok {
			m = make(map[string]models.Precisions)
			h.precisionMap[trading] = m
		}
		m[settlement] = models.Precisions{
			PricePrecision:  Precision(last),
			AmountPrecision: Precision(volume),
		}
	}
	return nil
}

func (h *LbankApi) fetchRate() error {
	h.rateMap = make(map[string]map[string]float64)
	h.volumeMap = make(map[string]map[string]float64)
	url := h.publicApiUrl("/v1/ticker.do") + "?symbol=all"
	resp, err := h.HttpClient.Get(url)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := jason.NewValueFromBytes(byteArray)
	if err != nil {
		return errors.Wrapf(err, "failed to parse json")
	}
	data, err := json.Array()
	if err != nil {
		return errors.Wrapf(err, "failed to parse json")
	}
	for _, v := range data {
		vo, err := v.Object()
		if err != nil {
			return errors.Wrapf(err, "failed to parse object")
		}
		pairString, err := vo.GetString("symbol")
		if err != nil {
			return errors.Wrapf(err, "failed to parse symbol")
		}
		ticker, err := vo.GetObject("ticker")
		if err != nil {
			return errors.Wrapf(err, "failed to parse ticker")
		}
		lastf, err := ticker.GetFloat64("latest")
		if err != nil {
			return errors.Wrapf(err, "failed to parse latest")
		}
		volumef, err := ticker.GetFloat64("vol")
		if err != nil {
			return errors.Wrapf(err, "failed to parse vol")
		}

		currencies := strings.Split(pairString, "_")
		if len(currencies) != 2 {
			continue
		}
		trading := strings.ToUpper(currencies[0])
		settlement := strings.ToUpper(currencies[1])
		m, ok := h.rateMap[trading]
		if !ok {
			m = make(map[string]float64)
			h.rateMap[trading] = m
		}
		m[settlement] = lastf
		m, ok = h.volumeMap[trading]
		if !ok {
			m = make(map[string]float64)
			h.volumeMap[trading] = m
		}
		m[settlement] = volumef
	}
	return nil
}

func (h *LbankApi) OrderBookTickMap() (map[string]map[string]models.OrderBookTick, error) {

	return nil, errors.New("can not fetch orderBookTick due to Lbank API")
}

func (h *LbankApi) RateMap() (map[string]map[string]float64, error) {
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

func (h *LbankApi) VolumeMap() (map[string]map[string]float64, error) {
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

func (h *LbankApi) Precise(trading string, settlement string) (*models.Precisions, error) {
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

func (h *LbankApi) CurrencyPairs() ([]models.CurrencyPair, error) {
	h.currencyM.Lock()
	defer h.currencyM.Unlock()
	if len(h.currencyPairs) != 0 {
		return h.currencyPairs, nil
	}
	url := h.publicApiUrl("/v1/currencyPairs.do")
	resp, err := h.HttpClient.Get(url)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := jason.NewValueFromBytes(byteArray)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json 1")
	}
	data, err := json.Array()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json 2")
	}
	var pairs []models.CurrencyPair
	for _, v := range data {
		pairString, err := v.String()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse quote")
		}
		currencies := strings.Split(pairString, "_")
		if len(currencies) != 2 {
			continue
		}
		pair := models.CurrencyPair{
			Trading:    strings.ToUpper(currencies[0]),
			Settlement: strings.ToUpper(currencies[1]),
		}
		pairs = append(pairs, pair)
	}
	h.currencyPairs = pairs
	return pairs, nil
}

func (h *LbankApi) Volume(trading string, settlement string) (float64, error) {
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

func (h *LbankApi) Rate(trading string, settlement string) (float64, error) {
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

func (h *LbankApi) FrozenCurrency() ([]string, error) {
	url := h.publicApiUrl("/v1/withdrawConfigs.do")
	resp, err := h.HttpClient.Get(url)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := jason.NewValueFromBytes(byteArray)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json value")
	}
	data, err := json.Array()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json array")
	}
	var currencies []string
	for _, v := range data {
		vo, err := v.Object()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse object")
		}
		currency, err := vo.GetString("assetCode")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse assetCode")
		}
		isNotFrozen, err := vo.GetBoolean("canWithDraw")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse canWithDraw")
		}
		if !isNotFrozen {
			currencies = append(currencies, currency)
		}
	}
	return currencies, nil
}

func (h *LbankApi) Board(trading string, settlement string) (board *models.Board, err error) {
	c, found := h.boardCache.Get(trading + "_" + settlement)
	if found {
		return c.(*models.Board), nil
	}
	args := url2.Values{}
	args.Add("symbol", strings.ToLower(trading)+"_"+strings.ToLower(settlement))
	args.Add("size", "60")
	method := "/v1/depth.do?" + args.Encode()
	url := h.publicApiUrl(method)
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
	jsonBids, err := json.GetValueArray("bids")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json bids %s %s %s", json, trading, settlement)
	}
	jsonAsks, err := json.GetValueArray("asks")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json asks")
	}
	bids := make([]models.BoardBar, 0)
	asks := make([]models.BoardBar, 0)
	for _, v := range jsonBids {
		arr, err := v.Array()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse array")
		}
		pricef, err := arr[0].Float64()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse array")
		}
		sizef, err := arr[1].Float64()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse array")
		}
		bids = append(bids, models.BoardBar{
			Price:  pricef,
			Amount: sizef,
			Type:   models.Bid,
		})
	}
	for _, v := range jsonAsks {
		arr, err := v.Array()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse array")
		}
		pricef, err := arr[0].Float64()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse array")
		}
		sizef, err := arr[1].Float64()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse array")
		}
		asks = append(asks, models.BoardBar{
			Price:  pricef,
			Amount: sizef,
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
