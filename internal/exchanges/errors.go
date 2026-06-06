package exchanges

import (
	"errors"
	"fmt"
)

var ErrRateLimited = errors.New("exchange rate limited")

type ExchangeError struct {
	Exchange string
	RetCode  int
	RetMsg   string
}

func (e ExchangeError) Error() string {
	return fmt.Sprintf("%s exchange error: retCode=%d retMsg=%q", e.Exchange, e.RetCode, e.RetMsg)
}
