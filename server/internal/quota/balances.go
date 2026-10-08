package quota

import "context"

// Prepaid API balances read from each provider's own documented endpoint
// with the API key. Each provider is its own account and its own reader;
// they only share how a key is found and fetched.

// --- OpenRouter: GET /api/v1/credits, total bought minus total used, USD ---

func openRouterProvider() Provider {
	return Provider{ID: "openrouter", Name: "OpenRouter", Link: "https://openrouter.ai/settings/credits", Modes: []Mode{{
		ID: "credits", Name: "Credits", Kind: KindAPI, Live: true, Key: "apiKey",
		Rule:   "Prepaid credits in US dollars, spent per request across every model on OpenRouter. They don't expire for a year.",
		Fields: keyField("sk-or-…", "From openrouter.ai › Keys."),
		Discover: func(l Local) []Connection {
			return keyConnections(l, "openrouter", "credits", keyHint{Hosts: []string{"openrouter.ai"},
				OpenCode: []string{"openrouter"}, Env: []string{"OPENROUTER_API_KEY"}}, nil)
		},
		Read: readOpenRouter,
	}}}
}

func readOpenRouter(ctx context.Context, l Local, c Connection) Account {
	p := openRouterProvider()
	a := base(p, p.Modes[0], c, l.Now())
	var r struct {
		Data struct {
			Credits flexNumber `json:"total_credits"`
			Usage   flexNumber `json:"total_usage"`
		} `json:"data"`
	}
	if !getJSON(ctx, l, l.url("openrouter", "https://openrouter.ai")+"/api/v1/credits", c.Values["apiKey"], "OpenRouter", &a, &r) {
		return a
	}
	a.Status = StatusOK
	left := float64(r.Data.Credits - r.Data.Usage)
	a.Balances = []Balance{{Currency: "USD", Amount: money2(left)}}
	a.Groups = []Group{{Title: "Credits", Balances: []Balance{
		{Label: "Bought", Currency: "USD", Amount: money2(float64(r.Data.Credits))},
		{Label: "Used", Currency: "USD", Amount: money2(float64(r.Data.Usage))},
	}}}
	if left <= 0 {
		a.Message = "No credits left: free models only, at their daily limit."
	}
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("openrouter", "https://openrouter.ai")+"/api/v1/models/user")
	return a
}

// --- SiliconFlow: GET /v1/user/info, totalBalance = gift balance + topped up ---

func siliconFlowProvider() Provider {
	return Provider{ID: "siliconflow", Name: "SiliconFlow", Link: "https://cloud.siliconflow.cn/account/billing", Modes: []Mode{{
		ID: "api", Name: "API balance", Kind: KindAPI, Live: true, Key: "apiKey",
		Rule: "Prepaid balance spent per token. Gift balance is used before topped-up balance.",
		Fields: []Field{
			{ID: "apiKey", Label: "API key", Type: "secret", Placeholder: "sk-…", Help: "From SiliconFlow › API keys."},
			{ID: "region", Label: "Site", Type: "select", Options: []Option{{"cn", "siliconflow.cn (China)"}, {"com", "siliconflow.com (International)"}}},
		},
		Discover: func(l Local) []Connection {
			cn := keyConnections(l, "siliconflow", "api", keyHint{Hosts: []string{"api.siliconflow.cn"}, OpenCode: []string{"siliconflow-cn"}, Env: []string{"SILICONFLOW_API_KEY"}},
				func(foundKey) map[string]string { return map[string]string{"region": "cn"} })
			com := keyConnections(l, "siliconflow", "api", keyHint{Hosts: []string{"api.siliconflow.com"}, OpenCode: []string{"siliconflow"}},
				func(foundKey) map[string]string { return map[string]string{"region": "com"} })
			return append(cn, com...)
		},
		Read: readSiliconFlow,
	}}}
}

func readSiliconFlow(ctx context.Context, l Local, c Connection) Account {
	p := siliconFlowProvider()
	a := base(p, p.Modes[0], c, l.Now())
	host, currency := "https://api.siliconflow.cn", "CNY"
	if c.Values["region"] == "com" {
		host, currency, a.Link = "https://api.siliconflow.com", "USD", "https://cloud.siliconflow.com/account/billing"
	}
	var r struct {
		Data struct {
			Total   flexNumber `json:"totalBalance"`
			Gift    flexNumber `json:"balance"`
			Charged flexNumber `json:"chargeBalance"`
			Name    string     `json:"name"`
			Email   string     `json:"email"`
		} `json:"data"`
	}
	if !getJSON(ctx, l, l.url("siliconflow", host)+"/v1/user/info", c.Values["apiKey"], "SiliconFlow", &a, &r) {
		return a
	}
	a.Status = StatusOK
	a.User = r.Data.Email
	a.Balances = []Balance{{Currency: currency, Amount: money2(float64(r.Data.Total)),
		Granted: money2(float64(r.Data.Gift)), ToppedUp: money2(float64(r.Data.Charged))}}
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("siliconflow", host)+"/v1/models")
	return a
}

// --- StepFun: GET /v1/accounts, balance left to spend, vouchers included ---

func stepFunProvider() Provider {
	return Provider{ID: "stepfun", Name: "StepFun", Link: "https://platform.stepfun.com/account-overview", Modes: []Mode{{
		ID: "api", Name: "API balance", Kind: KindAPI, Live: true, Key: "apiKey",
		Rule: "Prepaid balance spent per token; vouchers count toward it.",
		Fields: []Field{
			{ID: "apiKey", Label: "API key", Type: "secret", Help: "From platform.stepfun.com › API keys."},
			{ID: "region", Label: "Platform", Type: "select", Options: []Option{{"com", "stepfun.com (China)"}, {"ai", "stepfun.ai (International)"}}},
		},
		Discover: func(l Local) []Connection {
			cn := keyConnections(l, "stepfun", "api", keyHint{Hosts: []string{"api.stepfun.com"}, OpenCode: []string{"stepfun"}, Env: []string{"STEPFUN_API_KEY"}},
				func(foundKey) map[string]string { return map[string]string{"region": "com"} })
			ai := keyConnections(l, "stepfun", "api", keyHint{Hosts: []string{"api.stepfun.ai"}},
				func(foundKey) map[string]string { return map[string]string{"region": "ai"} })
			return append(cn, ai...)
		},
		Read: readStepFun,
	}}}
}

func readStepFun(ctx context.Context, l Local, c Connection) Account {
	p := stepFunProvider()
	a := base(p, p.Modes[0], c, l.Now())
	host, currency := "https://api.stepfun.com", "CNY"
	if c.Values["region"] == "ai" {
		host, currency, a.Link = "https://api.stepfun.ai", "USD", "https://platform.stepfun.ai/account-overview"
	}
	var r struct {
		Balance flexNumber `json:"balance"`
		Voucher flexNumber `json:"total_voucher_balance"`
		Cash    flexNumber `json:"total_cash_balance"`
	}
	if !getJSON(ctx, l, l.url("stepfun", host)+"/v1/accounts", c.Values["apiKey"], "StepFun", &a, &r) {
		return a
	}
	a.Status = StatusOK
	a.Balances = []Balance{{Currency: currency, Amount: money2(float64(r.Balance))}}
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("stepfun", host)+"/v1/models")
	return a
}
