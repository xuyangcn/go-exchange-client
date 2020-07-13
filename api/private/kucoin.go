package private

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io/ioutil"
	"net/http"
	"net/url"
	"sync"
	"time"

	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/antonholmquist/jason"
	"github.com/xuyangcn/go-exchange-client/api/public"
	"github.com/xuyangcn/go-exchange-client/models"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
)

const (
	KUCOIN_BASE_URL = "https://api.kucoin.com"
)

func NewKucoinApi(apikey func() (string, error), apisecret func() (string, error)) (*KucoinApi, error) {
	hitbtcPublic, err := public.NewKucoinPublicApi()
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize public client")
	}
	pairs, err := hitbtcPublic.CurrencyPairs()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get pairs")
	}
	var settlements []string
	for _, v := range pairs {
		settlements = append(settlements, v.Settlement)
	}
	m := make(map[string]bool)
	uniq := []string{}
	for _, ele := range settlements {
		if !m[ele] {
			m[ele] = true
			uniq = append(uniq, ele)
		}
	}

	return &KucoinApi{
		BaseURL:           KUCOIN_BASE_URL,
		RateCacheDuration: 30 * time.Second,
		ApiKeyFunc:        apikey,
		SecretKeyFunc:     apisecret,
		settlements:       uniq,
		rateMap:           nil,
		volumeMap:         nil,
		rateLastUpdated:   time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		rt:                &http.Transport{},

		m: new(sync.Mutex),
	}, nil
}

type KucoinApi struct {
	ApiKeyFunc        func() (string, error)
	SecretKeyFunc     func() (string, error)
	BaseURL           string
	RateCacheDuration time.Duration
	HttpClient        http.Client
	rt                *http.Transport
	settlements       []string

	volumeMap       map[string]map[string]float64
	rateMap         map[string]map[string]float64
	precisionMap    map[string]map[string]models.Precisions
	rateLastUpdated time.Time

	m *sync.Mutex
}

func (h *KucoinApi) privateApiUrl() string {
	return h.BaseURL
}

func (h *KucoinApi) publicApiUrl(command string) string {
	return h.BaseURL + command
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

		m, ok := h.precisionMap[trading]
		if !ok {
			m = make(map[string]models.Precisions)
			h.precisionMap[trading] = m
		}
		m[settlement] = models.Precisions{
			PricePrecision:  coinPrecision[settlement],
			AmountPrecision: coinPrecision[trading],
		}
	}
	return errors.Wrapf(err, "failed to fetch %s", url)
}

func (h *KucoinApi) precise(trading string, settlement string) (*models.Precisions, error) {
	if trading == settlement {
		return &models.Precisions{}, nil
	}

	h.fetchPrecision()
	if m, ok := h.precisionMap[trading]; !ok {
		return &models.Precisions{}, errors.Errorf("%s/%s missing trading", trading, settlement)
	} else if precisions, ok := m[settlement]; !ok {
		return &models.Precisions{}, errors.Errorf("%s/%s missing settlement", trading, settlement)
	} else {
		return &precisions, nil
	}
}
func (h *KucoinApi) privateApi(method string, path string, params *url.Values) ([]byte, error) {
	apiFraseAndKey, err := h.ApiKeyFunc()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create request command %s", path)
	}
	if !strings.Contains(apiFraseAndKey, "::") {
		return nil, errors.New("invalid passphrase")
	}
	sli := strings.SplitN(apiFraseAndKey, "::", 2)
	if len(sli) < 2 {
		return nil, errors.New("invalid passphrase")
	}
	if (sli[0] == "") || (sli[1] == "") {
		return nil, errors.New("invalid passphrase")
	}
	phrase := sli[0]
	apiKey := sli[1]
	secretKey, err := h.SecretKeyFunc()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create request command %s", path)
	}
	urlStr := h.BaseURL + path
	if strings.ToUpper(method) == "GET" {
		urlStr = urlStr + "?" + params.Encode()
	}

	reader := bytes.NewReader([]byte(params.Encode()))
	req, err := http.NewRequest(method, urlStr, reader)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create request command %s", path)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("Accept", "application/json")
	var b bytes.Buffer
	b.WriteString(method)
	b.WriteString(path)
	b.WriteString(params.Encode())
	t := strconv.FormatInt((time.Now().UnixNano() / 1000000), 10)
	p := []byte(t + b.String())
	hm := hmac.New(sha256.New, []byte(secretKey))
	hm.Write(p)
	s := base64.StdEncoding.EncodeToString(hm.Sum(nil))
	req.Header.Set("KC-API-KEY", apiKey)
	req.Header.Set("KC-API-TIMESTAMP", t)
	req.Header.Set(
		"KC-API-SIGN", s,
	)
	req.Header.Set("KC-API-PASSPHRASE", phrase)
	res, err := h.HttpClient.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to request command %s", path)
	}
	defer res.Body.Close()
	resBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch result of command %s", path)
	}
	return resBody, err
}

func (h *KucoinApi) TradeFeeRates() (map[string]map[string]TradeFee, error) {
	url := h.publicApiUrl("/api/v1/market/allTickers")
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
	traderFeeMap := make(map[string]map[string]TradeFee)
	for _, v := range value.Get("data.ticker").Array() {
		currencies := strings.Split(v.Get("symbol").Str, "-")
		if len(currencies) < 2 {
			continue
		}
		trading := currencies[0]
		settlement := currencies[1]

		feeRate := 0.001
		m, ok := traderFeeMap[trading]
		if !ok {
			m = make(map[string]TradeFee)
			traderFeeMap[trading] = m
		}
		m[settlement] = TradeFee{feeRate, feeRate}
	}
	return traderFeeMap, nil
}

func (b *KucoinApi) TradeFeeRate(trading string, settlement string) (TradeFee, error) {
	feeMap, err := b.TradeFeeRates()
	if err != nil {
		return TradeFee{}, err
	}
	return feeMap[trading][settlement], nil
}

type KucoinTransferFeeResponse struct {
	response []byte
	Currency string
	err      error
}

type kucoinTransferFeeMap map[string]float64
type kucoinTransferFeeSyncMap struct {
	kucoinTransferFeeMap
	m *sync.Mutex
}

func (sm *kucoinTransferFeeSyncMap) Set(currency string, fee float64) {
	sm.m.Lock()
	defer sm.m.Unlock()
	sm.kucoinTransferFeeMap[currency] = fee
}
func (sm *kucoinTransferFeeSyncMap) GetAll() map[string]float64 {
	sm.m.Lock()
	defer sm.m.Unlock()
	return sm.kucoinTransferFeeMap
}

func (h *KucoinApi) TransferFee() (map[string]float64, error) {
	url := h.publicApiUrl("/api/v1/currencies")
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
	transferFeeMap := kucoinTransferFeeSyncMap{make(map[string]float64), new(sync.Mutex)}
	for _, v := range data {
		fees, err := v.GetString("withdrawalMinFee")
		if err != nil {
			continue
		}
		feef, err := strconv.ParseFloat(fees, 64)
		if err != nil {
			continue
		}
		coin, err := v.GetString("currency")
		if err != nil {
			continue
		}
		transferFeeMap.Set(strings.ToUpper(coin), feef)
	}
	return transferFeeMap.GetAll(), nil
}

func (h *KucoinApi) Balances() (map[string]float64, error) {
	m := make(map[string]float64)
	params := &url.Values{}
	byteArray, err := h.privateApi("GET", "/api/v1/accounts", params)
	if err != nil {
		return nil, err
	}
	json, err := jason.NewObjectFromBytes(byteArray)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json")
	}
	data, err := json.GetObjectArray("data")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json key data %s", json)
	}
	for _, v := range data {
		balanceStr, err := v.GetString("balance")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse balance on %s", json)
		}
		balance, err := strconv.ParseFloat(balanceStr, 64)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse balance on %s", json)
		}
		availableStr, err := v.GetString("available")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse available on %s", json)
		}
		available, err := strconv.ParseFloat(availableStr, 64)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse balance on %s", json)
		}
		freeze := balance - available
		currency, err := v.GetString("currency")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse currency on %s", json)
		}
		currency = strings.ToUpper(currency)
		m[currency] = balance - freeze
	}
	return m, nil
}

type KucoinBalance struct {
	T       string
	Balance float64
}

func (h *KucoinApi) CompleteBalances() (map[string]*models.Balance, error) {
	m := make(map[string]*models.Balance)
	params := &url.Values{}
	byteArray, err := h.privateApi("GET", "/api/v1/accounts", params)
	if err != nil {
		return nil, err
	}
	json, err := jason.NewObjectFromBytes(byteArray)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json")
	}
	data, err := json.GetObjectArray("data")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse json key data %s", json)
	}
	for _, v := range data {
		balance, err := v.GetFloat64("balance")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse balance on %s", json)
		}
		available, err := v.GetFloat64("available")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse available on %s", json)
		}
		freeze := balance - available
		currency, err := v.GetString("currency")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse currency on %s", json)
		}
		currency = strings.ToUpper(currency)
		m[currency] = &models.Balance{
			Available: balance,
			OnOrders:  freeze,
		}
	}
	return m, nil
}

func (h *KucoinApi) CompleteBalance(coin string) (*models.Balance, error) {
	completeBalances, err := h.CompleteBalances()

	if err != nil {
		return nil, err
	}

	completeBalance, ok := completeBalances[coin]
	if !ok {
		return nil, errors.New("cannot find complete balance")
	}
	return completeBalance, nil
}

type KucoinActiveOrderResponse struct {
	response   []byte
	Trading    string
	Settlement string
	err        error
}

func (h *KucoinApi) ActiveOrders() ([]*models.Order, error) {
	return nil, errors.New("not implemented")
}

func (h *KucoinApi) Order(trading string, settlement string, ordertype models.OrderType, price float64, amount float64) (string, error) {
	params := &url.Values{}
	if ordertype == models.Bid {
		params.Set("type", "SELL")
	} else if ordertype == models.Ask {
		params.Set("type", "BUY")
	} else {
		return "", errors.Errorf("unknown order type %d", ordertype)
	}
	precise, err := h.precise(trading, settlement)
	if err != nil {
		return "", err
	}
	params.Set("price", FloorFloat64ToStr(price, precise.PricePrecision))
	params.Set("amount", FloorFloat64ToStr(amount, precise.AmountPrecision))

	symbol := strings.ToUpper(fmt.Sprintf("%s-%s", trading, settlement))
	params.Set("symbol", symbol)
	byteArray, err := h.privateApi("POST", "/v1/order", params)
	if err != nil {
		return "", err
	}

	json, err := jason.NewObjectFromBytes(byteArray)
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse json object")
	}
	data, err := json.GetObject("data")
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse json data %s", json)
	}
	orderId, err := data.GetString("orderOid")
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse json orderId %s", json)
	}
	return orderId, nil
}

func (h *KucoinApi) Transfer(typ string, addr string, amount float64, additionalFee float64) error {
	params := &url.Values{}
	amountStr := strconv.FormatFloat(amount, 'f', 4, 64)
	params.Set("address", addr)
	params.Set("coin", typ)
	params.Set("amount", amountStr)
	_, err := h.privateApi("POST", fmt.Sprintf("/v1/account/%s/withdraw/apply", typ), params)
	return err
}

func (h *KucoinApi) CancelOrder(trading string, settlement string,
	ordertype models.OrderType, orderNumber string) error {
	params := &url.Values{}
	params.Set("symbol", trading+"-"+settlement)
	params.Set("orderOid", orderNumber)
	if ordertype == models.Ask {
		params.Set("type", "BUY")
	} else {
		params.Set("type", "SELL")
	}
	bs, err := h.privateApi("POST", "/v1/cancel-order", params)
	if err != nil {
		return errors.Wrapf(err, "failed to cancel order")
	}
	json, err := jason.NewObjectFromBytes(bs)
	if err != nil {
		return errors.Wrapf(err, "failed to parse json %s", json)
	}
	success, err := json.GetBoolean("success")
	if err != nil {
		return errors.Wrapf(err, "failed to parse json %s", json)
	}
	if !success {
		errors.Errorf("failed to cancel order %s", json)
	}
	return nil
}

func (h *KucoinApi) IsOrderFilled(trading string, settlement string, orderNumber string) (bool, error) {
	params := &url.Values{}
	params.Set("symbol", trading+"-"+settlement)
	bs, err := h.privateApi("GET", "/v1/order/active", params)
	if err != nil {
		return false, errors.Wrapf(err, "failed to cancel order")
	}
	json, err := jason.NewObjectFromBytes(bs)
	if err != nil {
		return false, errors.Wrapf(err, "failed to parse json %s", json)
	}
	data, err := json.GetObject("data")
	if err != nil {
		return false, errors.Wrapf(err, "failed to parse json %s", json)
	}
	buys, err := data.GetValueArray("BUY")
	if err != nil {
		return false, errors.Wrap(err, "failed to parse json")
	}
	sells, err := data.GetValueArray("SELL")
	if err != nil {
		return false, errors.Wrap(err, "failed to parse json")
	}
	for _, s := range sells {
		sary, err := s.Array()
		if err != nil {
			return false, errors.Wrap(err, "failed to parse json")
		}
		orderId, err := sary[5].String()
		if err != nil {
			return false, errors.Wrap(err, "failed to parse json")
		}
		if orderId == orderNumber {
			return false, nil
		}
	}
	for _, s := range buys {
		sary, err := s.Array()
		if err != nil {
			return false, errors.Wrap(err, "failed to parse json")
		}
		orderId, err := sary[5].String()
		if err != nil {
			return false, errors.Wrap(err, "failed to parse json")
		}
		if orderId == orderNumber {
			return false, nil
		}
	}
	return true, nil
}

func (h *KucoinApi) Address(c string) (string, error) {
	params := &url.Values{}
	bs, err := h.privateApi("GET", fmt.Sprintf("/v1/account/%s/wallet/address", c), params)
	if err != nil {
		return "", errors.Wrapf(err, "failed to cancel order")
	}
	json, err := jason.NewObjectFromBytes(bs)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse json")
	}
	data, err := json.GetObject("data")
	if err != nil {
		return "", errors.Wrap(err, "failed to parse json")
	}
	address, err := data.GetString("address")
	if err != nil {
		return "", errors.Wrap(err, "failed to parse json")
	}
	return address, errors.New("not implemented")
}
