package tools

import (
	"strings"
	"testing"

	"github.com/mattoddie/crunch-mcp/internal/crunch/crunchtest"
)

func TestListArgsSearchLimitFields(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/clients/list", crunchtest.JSON([]any{
		Obj{"id": 1, "name": "Example Ltd", "email": "sam@example.com", "billingAddress": Obj{"postCode": "AB1 2CD", "country": "GB"}},
		Obj{"id": 2, "name": "Other Ltd", "email": "jo@other.example", "billingAddress": Obj{"postCode": "ZZ9 9ZZ", "country": "GB"}},
		Obj{"id": 3, "name": "Third Example plc", "email": "x@third.example", "billingAddress": Obj{"postCode": "AB3 4EF", "country": "FR"}},
	}))
	res := env.obj("crunch_list_clients", map[string]any{"search": "EXAMPLE", "limit": 1, "fields": []any{"name", "id"}})
	if asJSON(t, res) != `{"items":[{"id":1,"name":"Example Ltd"}],"returned":1,"total":3}` {
		t.Errorf("clients: %s", asJSON(t, res))
	}
	if res := env.obj("crunch_list_clients", map[string]any{"search": "ab3"}); num(res["total"]) != 1 {
		t.Errorf("search reaches nested fields: %s", asJSON(t, res))
	}
	res = env.obj("crunch_list_clients", map[string]any{"fields": []any{"nope"}})
	if hint, _ := res["hint"].(string); !strings.Contains(hint, "Available fields") || !strings.Contains(hint, "name") {
		t.Errorf("hint: %v", res)
	}
}

func TestPagedListSearchesFetchedItems(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/expenses", func(r crunchtest.Request, _ []string) (int, any) {
		return 200, Obj{"content": []any{jsonObj(t, expenseFixture), Obj{"id": 78, "supplier": Obj{"companyName": "Other"}}}, "totalElements": 40, "totalPages": 1, "last": false}
	})
	res := env.obj("crunch_list_expenses", map[string]any{"search": "example supplier", "fields": []any{"id", "supplier"}})
	if num(res["total"]) != 1 || num(res["searched"]) != 2 || num(res["next_page"]) != 1 || asJSON(t, res["items"]) != `[{"id":77,"supplier":"Example Supplier Ltd"}]` {
		t.Errorf("paged search: %s", asJSON(t, res))
	}
	res = env.obj("crunch_list_expenses", map[string]any{"limit": 1})
	if num(res["total"]) != 40 || num(res["returned"]) != 1 {
		t.Errorf("paged limit: %s", asJSON(t, res))
	}
}
