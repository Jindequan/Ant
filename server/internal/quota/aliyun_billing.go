package quota

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ant/server/internal/providers/aliyun"
)

// bailianSpend is what Bailian charged pay-as-you-go in a billing cycle,
// summed exactly by the module.
func bailianSpend(ctx context.Context, bill billing, cycle string) (aliyun.Spend, error) {
	var r aliyun.BillOverview
	if err := bill(ctx, "QueryBillOverview", map[string]any{"BillingCycle": cycle}, &r); err != nil {
		return aliyun.Spend{}, err
	}
	return aliyun.SpendOf(r.Data.Items.Item)
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
	var worth []string
	for i, cycle := range aliyun.Cycles(now) { // Beijing time, as Alibaba Cloud bills
		label := [2]string{"This month", "Last month"}[i]
		sp, err := bailianSpend(ctx, bill, cycle)
		if err != nil {
			spend.Note = "Couldn't read the bill: " + short(err.Error())
			break
		}
		if sp.Owner != "" {
			a.Identity = "payg:" + sp.Owner
		}
		spend.Balances = append(spend.Balances, Balance{Label: label, Currency: sp.Currency, Amount: aliyun.Money(sp.Charged)})
		if sp.Gross > sp.Charged {
			worth = append(worth, fmt.Sprintf("%s ¥%s", strings.ToLower(label), aliyun.Money(sp.Gross)))
		}
	}
	if len(worth) > 0 && spend.Note == "" {
		spend.Note = "Usage worth " + strings.Join(worth, " and ") + " before free quota, plans and discounts."
	}
	a.Groups = []Group{spend}
	return a
}
