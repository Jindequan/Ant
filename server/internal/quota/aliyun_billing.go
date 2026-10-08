package quota

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// billItem is one line of the official BSS QueryBillOverview.
type billItem struct {
	ProductCode       string     `json:"ProductCode"`
	SubscriptionType  string     `json:"SubscriptionType"`
	PretaxAmount      flexNumber `json:"PretaxAmount"`
	PretaxGrossAmount flexNumber `json:"PretaxGrossAmount"`
	Currency          string     `json:"Currency"`
	OwnerID           string     `json:"OwnerID"`
}

// bailianSpend is what Bailian (product sfm) charged pay-as-you-go in a
// billing cycle, and what that usage was worth before free quota, coupons
// and discounts.
func bailianSpend(ctx context.Context, bill billing, cycle string) (charged, gross float64, currency, owner string, err error) {
	var r struct {
		Data struct {
			Items struct {
				Item []billItem `json:"Item"`
			} `json:"Items"`
		} `json:"Data"`
	}
	if err := bill(ctx, "QueryBillOverview", map[string]any{"BillingCycle": cycle}, &r); err != nil {
		return 0, 0, "", "", err
	}
	currency = "CNY"
	for _, it := range r.Data.Items.Item {
		owner = it.OwnerID
		if it.ProductCode != "sfm" || it.SubscriptionType != "PayAsYouGo" {
			continue
		}
		charged, gross = charged+float64(it.PretaxAmount), gross+float64(it.PretaxGrossAmount)
		if it.Currency != "" {
			currency = it.Currency
		}
	}
	return charged, gross, currency, owner, nil
}

// flexNumber is an amount sent as a JSON number or as a string.
type flexNumber float64

func (n *flexNumber) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	*n = flexNumber(f)
	return err
}

func money2(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

// billing reads an Alibaba Cloud bill API: officially with an AccessKey,
// or through the console with a browser session.
type billing func(ctx context.Context, action string, params map[string]any, dst any) error

func readBailianPayg(ctx context.Context, l Local, c Connection) Account {
	p := aliyunProvider()
	now := l.Now()
	a := base(p, p.Modes[1], c, now)
	var bill billing
	if c.Values["accessKeyId"] != "" && c.Values["accessKeySecret"] != "" {
		bill = officialBilling(l, c, now)
	} else if c.Values["token"] == "" || c.Values["session"] != "" {
		s := openFor(ctx, l, c, &a)
		if s == nil {
			return a
		}
		bill = func(ctx context.Context, action string, params map[string]any, dst any) error {
			product := "BssOpenApi"
			if action == "GetFundAccountAvailableAmount" {
				product = "BssOpenAPI-V3"
			}
			return s.openAPI(ctx, product, action, params, dst)
		}
	} else {
		// signed in through the CLI login only: it doesn't open bills
		a.Status, a.Message = StatusNotConnected, "Add a read-only AccessKey to read what Bailian charged; the Bailian sign-in doesn't open your bill."
		return a
	}
	var fund struct {
		AvailableAmount string `json:"AvailableAmount"`
		Currency        string `json:"Currency"`
	}
	if c.Values["accessKeyId"] != "" {
		var r struct {
			Data struct {
				AvailableAmount string `json:"AvailableAmount"`
				Currency        string `json:"Currency"`
			} `json:"Data"`
		}
		if err := bill(ctx, "QueryAccountBalance", nil, &r); err != nil {
			bailianFailed(&a, err)
			return a
		}
		fund.AvailableAmount, fund.Currency = r.Data.AvailableAmount, r.Data.Currency
	} else if err := bill(ctx, "GetFundAccountAvailableAmount", nil, &fund); err != nil {
		bailianFailed(&a, err)
		return a
	}
	a.Status = StatusOK
	if fund.AvailableAmount != "" {
		a.Balances = []Balance{{Label: "Balance", Currency: fund.Currency, Amount: strings.ReplaceAll(fund.AvailableAmount, ",", "")}}
	}
	spend := Group{Title: "Charged by Bailian"}
	local := now.In(time.FixedZone("CST", 8*3600)) // Alibaba Cloud bills in Beijing time
	var worth []string
	for i, label := range []string{"This month", "Last month"} {
		cycle := local.AddDate(0, -i, 1-local.Day()).Format("2006-01")
		charged, gross, currency, owner, err := bailianSpend(ctx, bill, cycle)
		if err != nil {
			spend.Note = "Couldn't read the bill: " + short(err.Error())
			break
		}
		if owner != "" {
			a.Identity = "payg:" + owner
		}
		spend.Balances = append(spend.Balances, Balance{Label: label, Currency: currency, Amount: money2(charged)})
		if gross > charged {
			worth = append(worth, fmt.Sprintf("%s ¥%s", strings.ToLower(label), money2(gross)))
		}
	}
	if len(worth) > 0 && spend.Note == "" {
		spend.Note = "Usage worth " + strings.Join(worth, " and ") + " before free quota, plans and discounts."
	}
	a.Groups = []Group{spend}
	return a
}

func requestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
