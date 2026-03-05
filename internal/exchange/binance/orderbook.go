package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/exchange"
)

// GetSpotOrderBook 获取现货订单簿
func (b *binance) GetSpotOrderBook(symbol string) ([][]string, [][]string, error) {
	b.mu.RLock()
	client := b.restAPISpotClient
	b.mu.RUnlock()

	if client == nil {
		return nil, nil, exchange.ErrNotInitialized
	}

	orderBook, err := client.NewOrderBookService().
		Symbol(symbol).
		Limit(500).
		Do(context.Background())
	if err != nil {
		return nil, nil, err
	}

	bids := make([][]string, 0, len(orderBook.Bids))
	for _, bid := range orderBook.Bids {
		if len(bid) >= 2 {
			priceStr := bid[0].Text('f', -1)
			qtyStr := bid[1].Text('f', -1)
			bids = append(bids, []string{priceStr, qtyStr})
		}
	}

	asks := make([][]string, 0, len(orderBook.Asks))
	for _, ask := range orderBook.Asks {
		if len(ask) >= 2 {
			priceStr := ask[0].Text('f', -1)
			qtyStr := ask[1].Text('f', -1)
			asks = append(asks, []string{priceStr, qtyStr})
		}
	}

	return bids, asks, nil
}

// GetFuturesOrderBook 获取合约订单簿
func (b *binance) GetFuturesOrderBook(symbol string) ([][]string, [][]string, error) {
	apiURL := fmt.Sprintf("%s%s", constants.BinanceRestBaseFuturesUrl, constants.BinanceFuturesDepthPath)
	params := url.Values{}
	params.Add("symbol", symbol)
	params.Add("limit", "500")
	fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())

	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	resp, err := restClient.DoGet(constants.ConnectTypeBinance, fullURL, "", "", "", "", "")
	if err != nil {
		return nil, nil, err
	}

	var orderBookResp struct {
		LastUpdateID int64      `json:"lastUpdateId"`
		Bids         [][]string `json:"bids"`
		Asks         [][]string `json:"asks"`
	}
	if err := json.Unmarshal([]byte(resp), &orderBookResp); err != nil {
		return nil, nil, err
	}

	return orderBookResp.Bids, orderBookResp.Asks, nil
}
