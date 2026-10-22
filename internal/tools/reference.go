package tools

import (
	"context"
	"strings"
	"time"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

type expenseTypesArgs struct {
	ListArgs
	IncludeDeprecated bool `json:"include_deprecated,omitempty" jsonschema:"Include deprecated types (default false)"`
	Date              Date `json:"date,omitempty" jsonschema:"Types valid on this date (YYYY-MM-DD; default today)"`
	Raw               bool `json:"raw,omitempty" jsonschema:"Return the API response unchanged"`
}

type vatTypesArgs struct {
	For string `json:"for,omitempty" jsonschema:"Which list: expenses, invoices or both (default both)"`
}

type paymentMethodsArgs struct {
	ListArgs
	For       string `json:"for,omitempty" jsonschema:"Expense payments (default) or invoice (customer) payments"`
	Direction string `json:"direction,omitempty" jsonschema:"For invoices: the payment direction as Crunch names it (default INCOMING, money received from customers)"`
}

type countriesArgs struct {
	ListArgs
}

type personsArgs struct {
	ListArgs
	Date Date   `json:"date,omitempty" jsonschema:"Date the people were in post (YYYY-MM-DD; default today)"`
	Role string `json:"role,omitempty" jsonschema:"Only people with this role, as Crunch names it (for example DIRECTOR)"`
}

func today() string { return time.Now().Format("2006-01-02") }

// expenseTypeDefaults maps each expense type to its default VAT type.
func expenseTypeDefaults(ctx context.Context, cl *crunch.Client) (map[string]string, error) {
	var res Obj
	if err := cl.Get(ctx, "/expenses/expense-types/all", nil, &res); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, g := range crunch.List(res, "expenseGroups") {
		for _, t := range crunch.List(g, "expenseTypes") {
			if v := crunch.Str(t, "defaultVatType"); v != "" {
				out[crunch.Str(t, "expenseTypeName")] = v
			}
		}
	}
	return out, nil
}

func registerReference(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "reference", Name: "crunch_list_expense_types", Title: "List expense types",
		Description: "List the expense types (categories) Crunch accepts in expenseType fields, for example ACCOUNTANCY, TELEPHONE or TRAVEL_AND_SUBSISTENCE, grouped as in Crunch, with each type's default VAT type. Use the `name` values when creating expenses and suppliers.",
	}, func(ctx context.Context, a expenseTypesArgs) (any, error) {
		var res Obj
		var err error
		if a.IncludeDeprecated {
			err = cl.Get(ctx, "/expenses/expense-types/all", nil, &res)
		} else {
			err = cl.Get(ctx, "/expenses/expense-types", crunch.Query{"excludeDeprecated": true, "date": a.Date}, &res)
		}
		if err != nil {
			return nil, err
		}
		if a.Raw {
			return res, nil
		}
		var types []Obj
		for _, g := range crunch.List(res, "expenseGroups") {
			for _, t := range crunch.List(g, "expenseTypes") {
				o := crunch.Compact(Obj{
					"name":             crunch.Str(t, "expenseTypeName"),
					"display_name":     crunch.Str(t, "expenseTypeDisplayableName"),
					"group":            crunch.Str(g, "groupName"),
					"default_vat_type": crunch.Str(t, "defaultVatType"),
					"deprecated":       crunch.Bool(t, "deprecated"),
				})
				if v, ok := t["assignable"].(bool); ok && !v {
					o["assignable"] = false
				}
				types = append(types, o)
			}
		}
		return a.result(types), nil
	})

	add(c, Def{
		Toolset: "reference", Name: "crunch_list_vat_types", Title: "List VAT types",
		Description: "List the VAT types (rates) Crunch accepts, with their percentages. Expenses and invoices have separate lists; use the names exactly as shown (for example \"Standard\", \"Exempt\", \"Outside the Scope\"), not code-style values.",
		Enums:       map[string][]string{"for": {"expenses", "invoices", "both"}},
	}, func(ctx context.Context, a vatTypesArgs) (any, error) {
		out := Obj{}
		list := func(path string) ([]Obj, error) {
			var res Obj
			if err := cl.Get(ctx, path, nil, &res); err != nil {
				return nil, err
			}
			return mapItems(crunch.List(res, "vatTypes"), func(v Obj) Obj {
				name := crunch.Str(v, "displayName")
				if name == "" {
					name = crunch.Str(v, "name")
				}
				return crunch.Compact(Obj{"name": name, "percent": v["vatPercent"]})
			}), nil
		}
		var err error
		if a.For != "invoices" {
			if out["expenses"], err = list("/expenses/vat-types"); err != nil {
				return nil, err
			}
		}
		if a.For != "expenses" {
			if out["invoices"], err = list("/invoices/vat-types"); err != nil {
				return nil, err
			}
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "reference", Name: "crunch_list_payment_methods", Title: "List payment methods",
		Description: "List payment methods (for example EFT, CASH, DIRECTORS_EXPENSES) for recording expense payments or invoice payments. Use `name` as the payment method.",
		Enums:       map[string][]string{"for": {"expenses", "invoices"}},
	}, func(ctx context.Context, a paymentMethodsArgs) (any, error) {
		var res Obj
		var err error
		if a.For == "invoices" {
			dir := a.Direction
			if dir == "" {
				dir = "INCOMING"
			}
			err = cl.Get(ctx, "/invoices/payment-methods", crunch.Query{"direction": dir}, &res)
		} else {
			err = cl.Get(ctx, "/expenses/payment-methods", nil, &res)
		}
		if err != nil {
			return nil, err
		}
		return a.result(mapItems(crunch.List(res, "paymentMethods"), func(m Obj) Obj {
			return crunch.Compact(Obj{"name": crunch.Str(m, "paymentMethodName"), "display_name": crunch.Str(m, "paymentMethodDisplayableName")})
		})), nil
	})

	add(c, Def{
		Toolset: "reference", Name: "crunch_list_countries", Title: "List countries",
		Description: "List the countries Crunch knows, with the country codes to use in addresses and supplier records, and whether each is an EU member.",
	}, func(ctx context.Context, a countriesArgs) (any, error) {
		var res []any
		if err := cl.Get(ctx, "/immutable-data/countries", nil, &res); err != nil {
			return nil, err
		}
		items := mapItems(crunch.Objs(res), func(c Obj) Obj {
			return crunch.Compact(Obj{"code": crunch.Str(c, "countryCode"), "name": crunch.Str(c, "countryName"), "eu": crunch.Bool(c, "euMember")})
		})
		return a.result(items), nil
	})

	add(c, Def{
		Toolset: "reference", Name: "crunch_list_accounting_periods", Title: "List open accounting periods",
		Description: "List the open accounting periods (financial years) with their start and end dates and status. Expenses can only be created or changed in an open period; closed years can't be edited.",
	}, func(ctx context.Context, a ListArgs) (any, error) {
		var out any
		if err := cl.Get(ctx, "/expenses/ledgers", nil, &out); err != nil {
			return nil, err
		}
		if list, ok := out.([]any); ok {
			return a.result(crunch.Objs(list)), nil
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "reference", Name: "crunch_list_persons", Title: "List directors and employees",
		Description: "List the people (directors and employees) linked to the business on a date. Use their IDs as the person for DIRECTORS_EXPENSES payments and as the employee on expense lines.",
	}, func(ctx context.Context, a personsArgs) (any, error) {
		date := a.Date
		if date == "" {
			date = today()
		}
		var res []any
		if err := cl.Get(ctx, "/expenses/persons", crunch.Query{"date": date, "role": a.Role}, &res); err != nil {
			return nil, err
		}
		return a.result(mapItems(crunch.Objs(res), func(p Obj) Obj {
			name := crunch.Str(p, "displayableName")
			if name == "" {
				name = strings.TrimSpace(crunch.Str(p, "forename") + " " + crunch.Str(p, "surname"))
			}
			return crunch.Compact(Obj{"id": crunch.ID(p, "id"), "name": name, "forename": crunch.Str(p, "forename"), "surname": crunch.Str(p, "surname")})
		})), nil
	})
}
