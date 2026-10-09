package observed

import "strings"

// DrawModelScope in an offer's draws stands for every meter of the account
// scoped to the offer's model (design 13 §4.1): a per-model grant or window.
const DrawModelScope = "scope:model"

// ModelID is the canonical id of an upstream model id: lower-case, without
// a "vendor/" prefix ("Zhipu/GLM-5.3" and "glm-5.3" are one model). Ids that
// differ otherwise stay different models: nothing is guessed. One rule for
// offers, scopes and the route preview.
func ModelID(upstream string) string {
	id := strings.ToLower(strings.TrimSpace(upstream))
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		id = id[i+1:]
	}
	return id
}
