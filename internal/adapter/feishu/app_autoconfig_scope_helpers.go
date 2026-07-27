package feishu

import (
	"sort"
	"strings"
)

func scopeKey(scope, scopeType string) string {
	return strings.TrimSpace(scope) + "|" + normalizeTokenType(scopeType)
}

func scopeRefMap(values []AutoConfigScopeRef) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, item := range values {
		out[scopeKey(item.Scope, item.ScopeType)] = true
	}
	return out
}

func subtractScopeRefs(left, right []AutoConfigScopeRef) []AutoConfigScopeRef {
	rightKeys := scopeRefMap(right)
	var out []AutoConfigScopeRef
	for _, item := range left {
		if rightKeys[scopeKey(item.Scope, item.ScopeType)] {
			continue
		}
		out = append(out, item)
	}
	return sortScopeRefs(out)
}

func subtractStrings(left, right []string) []string {
	rightSet := stringSet(right)
	var out []string
	for _, item := range left {
		item = strings.TrimSpace(item)
		if item == "" || rightSet[item] {
			continue
		}
		out = append(out, item)
	}
	return sortUniqueStrings(out)
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, item := range values {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out[trimmed] = true
		}
	}
	return out
}

func sortUniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, item := range values {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func sortScopeRefs(values []AutoConfigScopeRef) []AutoConfigScopeRef {
	seen := map[string]bool{}
	out := make([]AutoConfigScopeRef, 0, len(values))
	for _, item := range values {
		item.Scope = strings.TrimSpace(item.Scope)
		item.ScopeType = normalizeTokenType(item.ScopeType)
		if item.Scope == "" {
			continue
		}
		key := scopeKey(item.Scope, item.ScopeType)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ScopeType == out[j].ScopeType {
			return out[i].Scope < out[j].Scope
		}
		return out[i].ScopeType < out[j].ScopeType
	})
	return out
}

func normalizeTokenType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "user":
		return "user"
	default:
		return "tenant"
	}
}
