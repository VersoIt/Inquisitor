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

type orderRealtimeResult struct {
	Category       string              `json:"category"`
	NextPageCursor string              `json:"nextPageCursor"`
	List           []orderRealtimeItem `json:"list"`
}

type orderRealtimeItem struct {
	OrderID      string `json:"orderId"`
	OrderLinkID  string `json:"orderLinkId"`
	Symbol       string `json:"symbol"`
	Price        string `json:"price"`
	Qty          string `json:"qty"`
	Side         string `json:"side"`
	OrderStatus  string `json:"orderStatus"`
	AvgPrice     string `json:"avgPrice"`
	LeavesQty    string `json:"leavesQty"`
	CumExecQty   string `json:"cumExecQty"`
	CumExecValue string `json:"cumExecValue"`
	CumExecFee   string `json:"cumExecFee"`
	TimeInForce  string `json:"timeInForce"`
	OrderType    string `json:"orderType"`
	RejectReason string `json:"rejectReason"`
	ReduceOnly   bool   `json:"reduceOnly"`
	CreatedTime  string `json:"createdTime"`
	UpdatedTime  string `json:"updatedTime"`
}

type positionListResult struct {
	Category       string             `json:"category"`
	NextPageCursor string             `json:"nextPageCursor"`
	List           []positionListItem `json:"list"`
}

type positionListItem struct {
	PositionIdx    int    `json:"positionIdx"`
	Symbol         string `json:"symbol"`
	Side           string `json:"side"`
	Size           string `json:"size"`
	AvgPrice       string `json:"avgPrice"`
	PositionValue  string `json:"positionValue"`
	PositionStatus string `json:"positionStatus"`
	Leverage       string `json:"leverage"`
	MarkPrice      string `json:"markPrice"`
	LiqPrice       string `json:"liqPrice"`
	UnrealisedPnl  string `json:"unrealisedPnl"`
	CurRealisedPnl string `json:"curRealisedPnl"`
	CumRealisedPnl string `json:"cumRealisedPnl"`
	Seq            int64  `json:"seq"`
	IsReduceOnly   bool   `json:"isReduceOnly"`
	CreatedTime    string `json:"createdTime"`
	UpdatedTime    string `json:"updatedTime"`
}

type walletBalanceResult struct {
	List []walletBalanceAccount `json:"list"`
}

type walletBalanceAccount struct {
	AccountType            string              `json:"accountType"`
	TotalEquity            string              `json:"totalEquity"`
	TotalWalletBalance     string              `json:"totalWalletBalance"`
	TotalMarginBalance     string              `json:"totalMarginBalance"`
	TotalAvailableBalance  string              `json:"totalAvailableBalance"`
	TotalPerpUPL           string              `json:"totalPerpUPL"`
	TotalInitialMargin     string              `json:"totalInitialMargin"`
	TotalMaintenanceMargin string              `json:"totalMaintenanceMargin"`
	Coin                   []walletBalanceCoin `json:"coin"`
}

type walletBalanceCoin struct {
	Coin                  string `json:"coin"`
	Equity                string `json:"equity"`
	USDValue              string `json:"usdValue"`
	WalletBalance         string `json:"walletBalance"`
	Locked                string `json:"locked"`
	BorrowAmount          string `json:"borrowAmount"`
	AccruedInterest       string `json:"accruedInterest"`
	TotalOrderIM          string `json:"totalOrderIM"`
	TotalPositionIM       string `json:"totalPositionIM"`
	TotalPositionMM       string `json:"totalPositionMM"`
	UnrealisedPnL         string `json:"unrealisedPnl"`
	CumulativeRealisedPnL string `json:"cumRealisedPnl"`
	SpotBorrow            string `json:"spotBorrow"`
	MarginCollateral      bool   `json:"marginCollateral"`
	CollateralSwitch      bool   `json:"collateralSwitch"`
}
