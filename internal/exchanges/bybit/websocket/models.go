package websocket

type envelope struct {
	Topic string `json:"topic"`
	Type  string `json:"type"`
	TS    int64  `json:"ts"`
	CS    int64  `json:"cs"`
}

type klineMessage struct {
	Topic string      `json:"topic"`
	Type  string      `json:"type"`
	TS    int64       `json:"ts"`
	Data  []klineData `json:"data"`
}

type klineData struct {
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
	Interval  string `json:"interval"`
	Open      string `json:"open"`
	Close     string `json:"close"`
	High      string `json:"high"`
	Low       string `json:"low"`
	Volume    string `json:"volume"`
	Turnover  string `json:"turnover"`
	Confirm   bool   `json:"confirm"`
	Timestamp int64  `json:"timestamp"`
}

type tickerMessage struct {
	Topic string     `json:"topic"`
	Type  string     `json:"type"`
	TS    int64      `json:"ts"`
	CS    int64      `json:"cs"`
	Data  tickerData `json:"data"`
}

type tickerData struct {
	Symbol       string `json:"symbol"`
	LastPrice    string `json:"lastPrice"`
	Bid1Price    string `json:"bid1Price"`
	Bid1Size     string `json:"bid1Size"`
	Ask1Price    string `json:"ask1Price"`
	Ask1Size     string `json:"ask1Size"`
	MarkPrice    string `json:"markPrice"`
	IndexPrice   string `json:"indexPrice"`
	OpenInterest string `json:"openInterest"`
	FundingRate  string `json:"fundingRate"`
}

type tradeMessage struct {
	ID    string      `json:"id"`
	Topic string      `json:"topic"`
	Type  string      `json:"type"`
	TS    int64       `json:"ts"`
	Data  []tradeData `json:"data"`
}

type tradeData struct {
	TradeTime    int64  `json:"T"`
	Symbol       string `json:"s"`
	Side         string `json:"S"`
	Quantity     string `json:"v"`
	Price        string `json:"p"`
	TradeID      string `json:"i"`
	IsBlockTrade bool   `json:"BT"`
	Sequence     int64  `json:"seq"`
}

type orderbookMessage struct {
	Topic string        `json:"topic"`
	Type  string        `json:"type"`
	TS    int64         `json:"ts"`
	Data  orderbookData `json:"data"`
	CTS   int64         `json:"cts"`
}

type orderbookData struct {
	Symbol   string     `json:"s"`
	Bids     [][]string `json:"b"`
	Asks     [][]string `json:"a"`
	UpdateID int64      `json:"u"`
	Sequence int64      `json:"seq"`
}
