package rest

import "encoding/json"

type responseEnvelope[T any] struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  T      `json:"result"`
	Time    int64  `json:"time"`
}

type serverTimeResult struct {
	TimeSecond string `json:"timeSecond"`
	TimeNano   string `json:"timeNano"`
}

type instrumentsInfoResult struct {
	Category       string           `json:"category"`
	NextPageCursor string           `json:"nextPageCursor"`
	List           []instrumentInfo `json:"list"`
}

type instrumentInfo struct {
	Symbol          string          `json:"symbol"`
	ContractType    string          `json:"contractType"`
	Status          string          `json:"status"`
	BaseCoin        string          `json:"baseCoin"`
	QuoteCoin       string          `json:"quoteCoin"`
	LaunchTime      string          `json:"launchTime"`
	DeliveryTime    string          `json:"deliveryTime"`
	DeliveryFeeRate string          `json:"deliveryFeeRate"`
	PriceScale      string          `json:"priceScale"`
	LeverageFilter  json.RawMessage `json:"leverageFilter"`
	PriceFilter     priceFilter     `json:"priceFilter"`
	LotSizeFilter   lotSizeFilter   `json:"lotSizeFilter"`
	RiskParameters  json.RawMessage `json:"riskParameters"`
}

type priceFilter struct {
	MinPrice string `json:"minPrice"`
	MaxPrice string `json:"maxPrice"`
	TickSize string `json:"tickSize"`
}

type lotSizeFilter struct {
	MaxOrderQty         string `json:"maxOrderQty"`
	MinOrderQty         string `json:"minOrderQty"`
	QtyStep             string `json:"qtyStep"`
	PostOnlyMaxOrderQty string `json:"postOnlyMaxOrderQty"`
	MaxMktOrderQty      string `json:"maxMktOrderQty"`
	MinNotionalValue    string `json:"minNotionalValue"`
}

type klineResult struct {
	Category string     `json:"category"`
	Symbol   string     `json:"symbol"`
	List     [][]string `json:"list"`
}

type createOrderRequest struct {
	Category    string `json:"category"`
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`
	OrderType   string `json:"orderType"`
	Qty         string `json:"qty"`
	Price       string `json:"price,omitempty"`
	TimeInForce string `json:"timeInForce,omitempty"`
	PositionIdx int    `json:"positionIdx"`
	OrderLinkID string `json:"orderLinkId"`
	ReduceOnly  bool   `json:"reduceOnly"`
	TakeProfit  string `json:"takeProfit,omitempty"`
	StopLoss    string `json:"stopLoss,omitempty"`
}

type createOrderResult struct {
	OrderID     string `json:"orderId"`
	OrderLinkID string `json:"orderLinkId"`
}
