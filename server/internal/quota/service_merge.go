package quota

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// mergeSame folds readings of the same provider account, reached through
// different sign-ins, into one.
func mergeSame(as []Account) []Account {
	as = hideFailedWays(as)
	out := make([]Account, 0, len(as))
	at := map[string]int{}
	for _, a := range as {
		key := a.Provider + "\x00" + a.Identity
		i, seen := at[key]
		if a.Identity == "" || a.Status != StatusOK || !seen {
			if a.Identity != "" && a.Status == StatusOK {
				at[key] = len(out)
			}
			a.Keys = []string{a.ID}
			out = append(out, a)
			continue
		}
		m := &out[i]
		m.Keys = append(m.Keys, a.ID)
		if a.Source != "" && !strings.Contains(m.Source, a.Source) {
			m.Source += ", " + a.Source
		}
	}
	for i := range out {
		// sources read once: an account kept from the last refresh may
		// already carry the count
		if j := strings.Index(out[i].Source, " · "); j >= 0 && strings.HasSuffix(out[i].Source, " keys") {
			out[i].Source = out[i].Source[:j]
		}
		srcs := []string{}
		for _, s := range strings.Split(out[i].Source, ", ") {
			if s != "" && !slices.Contains(srcs, s) {
				srcs = append(srcs, s)
			}
		}
		out[i].Source = strings.Join(srcs, ", ")
		if n := len(out[i].Keys); n > 1 && len(srcs) == 1 {
			out[i].Source = fmt.Sprintf("%s · %d keys", out[i].Source, n)
		}
	}
	return out
}

// hideFailedWays leaves out an account read by one way that failed when the
// same provider's mode was read fine by another way: that way stands in.
// Failures of the way that works (two keys, one refused) stay visible.
// Quiet accounts (a site visited, never signed in to) are left out too.
func hideFailedWays(as []Account) []Account {
	okWays := map[string]map[string]bool{}
	for _, a := range as {
		if a.Status == StatusOK {
			k := a.Provider + "\x00" + a.Mode
			if okWays[k] == nil {
				okWays[k] = map[string]bool{}
			}
			okWays[k][a.Way] = true
		}
	}
	out := as[:0:0]
	for _, a := range as {
		if a.Quiet {
			continue
		}
		ways := okWays[a.Provider+"\x00"+a.Mode]
		if a.Status != StatusOK && len(ways) > 0 && !ways[a.Way] {
			continue
		}
		out = append(out, a)
	}
	return out
}

func sortAccounts(as []Account) {
	rank := map[Status]int{StatusOK: 0, StatusError: 1, StatusSignedOut: 1, StatusNotConnected: 2}
	sort.SliceStable(as, func(i, j int) bool { return rank[as[i].Status] < rank[as[j].Status] })
}
