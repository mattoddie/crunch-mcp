package tools

// Shared handling for list tools. Every crunch_list_* tool returns
// {total, returned, items} and accepts the same narrowing arguments, so a
// model can ask for exactly what it needs:
//
//   - search: case-insensitive substring match on the items' text and numbers
//   - fields: keep only these fields in each item (dot paths for nested ones)
//   - limit:  maximum number of items
//
// To add a list tool, embed ListArgs in its input and return a.result(items),
// or call fetchPaged for an endpoint Crunch pages. See docs/development.md.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ListArgs narrow a list tool's results.
type ListArgs struct {
	Search string   `json:"search,omitempty" jsonschema:"Case-insensitive substring match on any value"`
	Fields []string `json:"fields,omitempty" jsonschema:"Keep only these fields (dot paths, such as supplier.companyName)"`
	Limit  *int     `json:"limit,omitempty" jsonschema:"Maximum number of items"`
}

// listHelp is the server instructions' explanation of the list arguments.
const listHelp = "List tools take `search` (case-insensitive substring match on any text or number in an item), `fields` (keep only these fields in each item; dot paths like supplier.companyName with raw: true) and `limit`. " +
	"On paged lists (page, size, all) they apply to what was fetched: one page, or every page with all=true. Use them to fetch only what you need. "

// result narrows items (already summarised) and wraps them as {total, returned, items}:
// search first, then limit, then fields.
func (a ListArgs) result(items []Obj) Obj {
	kept := a.filter(items)
	total := len(kept)
	kept = a.limit(kept)
	return a.project(Obj{"total": total, "returned": len(kept)}, kept)
}

// filter keeps the items that match search.
func (a ListArgs) filter(items []Obj) []Obj {
	q := strings.ToLower(strings.TrimSpace(a.Search))
	if q == "" {
		return items
	}
	var out []Obj
	for _, it := range items {
		if containsFold(it, q) {
			out = append(out, it)
		}
	}
	return out
}

// limit cuts items to the limit argument.
func (a ListArgs) limit(items []Obj) []Obj {
	if a.Limit != nil && *a.Limit >= 0 && *a.Limit < len(items) {
		return items[:*a.Limit]
	}
	return items
}

// project sets out's items, keeping only the requested fields.
func (a ListArgs) project(out Obj, items []Obj) Obj {
	if items == nil {
		items = []Obj{}
	}
	out["returned"] = len(items)
	if len(a.Fields) == 0 {
		out["items"] = items
		return out
	}
	projected, hint := selectFields(items, a.Fields)
	out["items"] = projected
	if hint != "" {
		out["hint"] = hint
	}
	return out
}

// containsFold reports whether any text or number in v contains q (already lower-case).
func containsFold(v any, q string) bool {
	switch x := v.(type) {
	case map[string]any:
		for _, val := range x {
			if containsFold(val, q) {
				return true
			}
		}
	case []Obj:
		for _, val := range x {
			if containsFold(val, q) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if containsFold(val, q) {
				return true
			}
		}
	case string:
		return strings.Contains(strings.ToLower(x), q)
	case json.Number:
		return strings.Contains(x.String(), q)
	case int, int64, float64:
		return strings.Contains(fmt.Sprint(x), q)
	}
	return false
}

// selectFields keeps only the requested fields in each item. When no item has
// any of them, the hint names the fields the first item does have.
func selectFields(items []Obj, fields []string) ([]Obj, string) {
	var paths [][]string
	var names []string
	for _, f := range fields {
		f = strings.Trim(strings.TrimSpace(f), ".")
		if f == "" {
			continue
		}
		paths = append(paths, strings.Split(f, "."))
		names = append(names, f)
	}
	if len(paths) == 0 {
		return items, ""
	}
	out := make([]Obj, len(items))
	found := false
	for i, it := range items {
		v, ok := pick(it, paths)
		o, _ := v.(Obj)
		if !ok || o == nil {
			o = Obj{}
		}
		found = found || ok
		out[i] = o
	}
	if found || len(items) == 0 {
		return out, ""
	}
	var avail []string
	for k := range items[0] {
		avail = append(avail, k)
	}
	sort.Strings(avail)
	return out, fmt.Sprintf("None of the requested fields (%s) exist. Available fields: %s.", strings.Join(names, ", "), strings.Join(avail, ", "))
}

// pick copies the paths that exist in v, keeping their nesting. Lists along a
// path are walked item by item, so lines.type keeps each line's type.
func pick(v any, paths [][]string) (any, bool) {
	switch x := v.(type) {
	case []Obj:
		list := make([]any, len(x))
		for i, e := range x {
			list[i] = e
		}
		return pick(list, paths)
	case []any:
		out := []any{}
		for _, e := range x {
			if p, ok := pick(e, paths); ok {
				out = append(out, p)
			}
		}
		return out, len(out) > 0
	case map[string]any:
		out := Obj{}
		tails := map[string][][]string{}
		var heads []string
		for _, p := range paths {
			if _, seen := tails[p[0]]; !seen {
				heads = append(heads, p[0])
			}
			tails[p[0]] = append(tails[p[0]], p[1:])
		}
		for _, h := range heads {
			val, ok := x[h]
			if !ok {
				continue
			}
			whole := false
			var sub [][]string
			for _, t := range tails[h] {
				if len(t) == 0 {
					whole = true
				} else {
					sub = append(sub, t)
				}
			}
			if whole {
				out[h] = val
			} else if p, ok := pick(val, sub); ok {
				out[h] = p
			}
		}
		return out, len(out) > 0
	}
	return nil, false
}
