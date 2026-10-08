// Package quota knows what each of the user's services has left —
// subscription windows, API balances, free allowances — per provider and
// per mode, each by that mode's own rule. Sign-ins are found on this
// machine (other apps' configuration, the environment, the browser) or
// added on Ant's page; either way the provider's own module reads them.
//
// Nothing here spends quota: every request a mode makes is a read of the
// account's own usage or balance.
package quota

import "time"

// Kind is how a mode is paid for.
type Kind string

const (
	KindSubscription Kind = "subscription" // paid plan with renewing allowance
	KindAPI          Kind = "api"          // prepaid or pay-as-you-go balance
	KindFree         Kind = "free"         // free allowance that resets or expires
)

// Status is whether Ant could read the account just now.
type Status string

const (
	StatusOK           Status = "ok"
	StatusNotConnected Status = "not_connected" // nothing found or added yet
	StatusSignedOut    Status = "signed_out"    // sign-in found but no longer accepted
	StatusError        Status = "error"         // found, but the read failed
)

// Window is one allowance. Used/Limit/Left are present only when known in
// units, UsedPercent only when reported as a share: a missing value is
// unknown, never zero.
type Window struct {
	Name string `json:"name"`
	// Model is the model this allowance is for, when it is one model's
	// (Bailian's free quota, Gemini's buckets); Media what it makes, when
	// that differs from the provider's.
	Model       string     `json:"model,omitempty"`
	Media       string     `json:"media,omitempty"`
	UsedPercent *float64   `json:"usedPercent,omitempty"`
	Used        *float64   `json:"used,omitempty"`
	Limit       *float64   `json:"limit,omitempty"`
	Left        *float64   `json:"left,omitempty"`
	Unit        string     `json:"unit,omitempty"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
	// Expires: at ResetsAt the allowance ends instead of renewing (a free
	// grant with a due date).
	Expires bool `json:"expires,omitempty"`
	// ResetEstimated: the provider didn't say when; ResetsAt follows the
	// mode's published rule or the setup the user gave.
	ResetEstimated bool `json:"resetEstimated,omitempty"`
	// Aside: using it up doesn't stop the main use (Z.ai's monthly MCP
	// tool calls beside its model windows).
	Aside bool `json:"aside,omitempty"`
}

// Balance is money or credit held on an account, as an exact decimal string.
type Balance struct {
	Label    string `json:"label,omitempty"` // what the amount is, when an account shows several
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
	Granted  string `json:"granted,omitempty"`
	ToppedUp string `json:"toppedUp,omitempty"`
}

// Account is one read of one connection.
type Account struct {
	ID       string     `json:"id"` // the connection's id
	Provider string     `json:"provider"`
	Name     string     `json:"name"`
	Mode     string     `json:"mode,omitempty"`
	ModeName string     `json:"modeName,omitempty"`
	Way      string     `json:"way,omitempty"`
	WayName  string     `json:"wayName,omitempty"` // how it was read, when the mode has several ways
	Kind     Kind       `json:"kind"`
	Media    []string   `json:"media,omitempty"` // what it makes: text, image, video, music, speech, design, agent, search
	Label    string     `json:"label,omitempty"`
	Plan     string     `json:"plan,omitempty"`
	User     string     `json:"user,omitempty"`
	Status   Status     `json:"status"`
	Message  string     `json:"message,omitempty"` // what happened, and what to do
	Windows  []Window   `json:"windows,omitempty"`
	Balances []Balance  `json:"balances,omitempty"`
	RenewsAt *time.Time `json:"renewsAt,omitempty"`
	// EndsAt and AutoRenew: a paid period that ends, renewing only when
	// AutoRenew is true (nil: the provider doesn't say).
	EndsAt    *time.Time `json:"endsAt,omitempty"`
	AutoRenew *bool      `json:"autoRenew,omitempty"`
	// Groups are the parts of an account a provider shows separately (a
	// plan's credits, its add-ons, its tool benefits), each in the
	// provider's own terms.
	Groups []Group `json:"groups,omitempty"`
	// Models are what this account can call, as the provider lists them for
	// it (models.go): what a router may send its requests to.
	Models []Model   `json:"models,omitempty"`
	Origin Origin    `json:"origin,omitempty"`
	Source string    `json:"source,omitempty"`
	Live   bool      `json:"live"` // read from the provider, not only from the rule
	Link   string    `json:"link"`
	ReadAt time.Time `json:"readAt"`
	// Identity is the provider's own account, as the reading shows it; two
	// sign-ins reading the same identity are one account (Z.ai keys of one
	// plan). Keys lists the sign-ins merged into this one.
	Identity string   `json:"-"`
	Keys     []string `json:"keys,omitempty"`
	// Renewed: values a reader renewed while reading (a rotated refresh
	// token, fresh short-lived credentials) for the service to keep.
	Renewed map[string]string `json:"-"`
	// Quiet: nothing to show — a browser that visited a site without being
	// signed in to it.
	Quiet bool `json:"-"`
}

// Group is one part of an account, as its provider presents it.
type Group struct {
	Title    string    `json:"title"`
	Note     string    `json:"note,omitempty"`
	Windows  []Window  `json:"windows,omitempty"`
	Balances []Balance `json:"balances,omitempty"`
	Link     string    `json:"link,omitempty"`
	// Action: what the page offers to do here — "edit" opens the account's
	// settings (to add a key the group needs).
	Action string `json:"action,omitempty"`
}

func ptr[T any](v T) *T { return &v }
