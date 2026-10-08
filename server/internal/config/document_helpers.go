package config

import (
	"encoding/json"
	"strings"

	"ant/server/internal/domain"
)

var secretKeyNames = map[string]bool{
	"value": true, "secret": true, "token": true, "password": true,
	"apikey": true, "api_key": true, "authorization": true, "credential": true,
}

// checkNoSecrets rejects secret-looking keys anywhere in free-form JSON so a
// provider extension can never smuggle credential material into a config
// revision (K1: 共享身份与秘密引用，不复制秘密). Matching is recursive and
// case-insensitive; the registered extension schema additionally makes such
// keys unrepresentable (R12).
func (v *validator) checkNoSecrets(path string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var node any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&node); err != nil {
		v.issue(path, "extensionJSONInvalid")
		return
	}
	walkSecretKeys(v, path, node)
}

func walkSecretKeys(v *validator, path string, node any) {
	switch t := node.(type) {
	case map[string]any:
		for k, val := range t {
			if secretKeyNames[strings.ToLower(k)] {
				v.issue(path+"/"+k, "secretMaterialForbidden")
			}
			walkSecretKeys(v, path+"/"+k, val)
		}
	case []any:
		for j, val := range t {
			walkSecretKeys(v, path+"/"+itoa(j), val)
		}
	}
}

func validID(id domain.ID) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '-' || c == ':'
		if !ok {
			return false
		}
	}
	return true
}

func validUnit(u string) bool {
	if len(u) == 0 || len(u) > 32 {
		return false
	}
	for i := 0; i < len(u); i++ {
		c := u[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// validSourceRef accepts provenance URIs (e.g. fixture://plan-doc,
// https://…), not ant IDs; control characters and emptiness are the only
// hard failures.
func validSourceRef(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return false
		}
	}
	return true
}

func poolIndexByID(doc *ConfigDocument, id domain.ID) int {
	for i := range doc.Pools {
		if doc.Pools[i].PoolID == id {
			return i
		}
	}
	return -1
}

func resourceIndex(doc *ConfigDocument, id domain.ID) string {
	for i := range doc.Resources {
		if doc.Resources[i].ResourceID == id {
			return itoa(i)
		}
	}
	return "-"
}

func bindingIndexByID(doc *ConfigDocument, id domain.ID) int {
	for i := range doc.Bindings {
		if doc.Bindings[i].BindingID == id {
			return i
		}
	}
	return -1
}

func accountExists(doc *ConfigDocument, id domain.ID) bool {
	for i := range doc.Accounts {
		if doc.Accounts[i].AccountID == id {
			return true
		}
	}
	return false
}

func bindingExists(doc *ConfigDocument, id domain.ID) bool {
	for i := range doc.Bindings {
		if doc.Bindings[i].BindingID == id {
			return true
		}
	}
	return false
}

func groupExists(doc *ConfigDocument, id domain.ID) bool {
	for i := range doc.EntitlementGroups {
		if doc.EntitlementGroups[i].GroupID == id {
			return true
		}
	}
	return false
}

func poolExists(doc *ConfigDocument, id domain.ID) bool {
	for i := range doc.Pools {
		if doc.Pools[i].PoolID == id {
			return true
		}
	}
	return false
}

func offerExists(doc *ConfigDocument, id domain.ID) bool {
	for i := range doc.Offers {
		if doc.Offers[i].OfferID == id {
			return true
		}
	}
	return false
}

func containsID(ids []domain.ID, id domain.ID) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func itoa(n int) string { return itoa64(int64(n)) }

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func bindingAccountID(doc *ConfigDocument, bindingID domain.ID) domain.ID {
	for i := range doc.Bindings {
		if doc.Bindings[i].BindingID == bindingID {
			return doc.Bindings[i].AccountID
		}
	}
	return ""
}

func providerTypeOfAccount(doc *ConfigDocument, accountID domain.ID) domain.ID {
	for i := range doc.Accounts {
		if doc.Accounts[i].AccountID == accountID {
			return doc.Accounts[i].ProviderTypeID
		}
	}
	return ""
}

func resourcePath(doc *ConfigDocument, id domain.ID) string {
	for i := range doc.Resources {
		if doc.Resources[i].ResourceID == id {
			return "/resources/" + itoa(i)
		}
	}
	return "/resources/-"
}
