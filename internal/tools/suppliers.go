package tools

import (
	"context"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

type listSuppliersArgs struct {
	Name            string `json:"name,omitempty" jsonschema:"Exact company name to look up"`
	IncludeArchived bool   `json:"include_archived,omitempty" jsonschema:"Include archived suppliers (default false)"`
	ListArgs
	Raw bool `json:"raw,omitempty" jsonschema:"Return the full objects from the API instead of compact summaries (larger)"`
}

type supplierIDArgs struct {
	SupplierID int64 `json:"supplier_id" jsonschema:"Supplier ID (see crunch_list_suppliers)"`
}

type supplierFields struct {
	ContactName           string         `json:"contact_name,omitempty" jsonschema:"Contact at the supplier"`
	ExpenseType           string         `json:"expense_type,omitempty" jsonschema:"Default expense type for this supplier's expenses, for example TELEPHONE (see crunch_list_expense_types)"`
	Country               string         `json:"country,omitempty" jsonschema:"Country code, for example GB (see crunch_list_countries)"`
	VATRegistrationNumber string         `json:"vat_registration_number,omitempty" jsonschema:"The supplier's VAT number"`
	Extra                 map[string]any `json:"extra,omitempty" jsonschema:"Additional raw fields for the request body, using Crunch's own field names (advanced). Merged last."`
}

func (f supplierFields) body(name string) Obj {
	o := set(Obj{}, "companyName", name, "contactName", f.ContactName, "country", f.Country, "vatRegistrationNumber", f.VATRegistrationNumber)
	if f.ExpenseType != "" {
		o["expenseType"] = Obj{"expenseTypeName": f.ExpenseType}
	}
	return merge(o, f.Extra)
}

type createSupplierArgs struct {
	CompanyName string `json:"company_name" jsonschema:"Supplier name, for example Example Supplier Ltd"`
	supplierFields
}

type updateSupplierArgs struct {
	SupplierID  int64  `json:"supplier_id" jsonschema:"Supplier ID (see crunch_list_suppliers)"`
	CompanyName string `json:"company_name,omitempty" jsonschema:"New name"`
	Archived    *bool  `json:"archived,omitempty" jsonschema:"Archive (true) or restore (false) the supplier"`
	supplierFields
}

// findSuppliersByName asks Crunch for suppliers with exactly this company name.
func findSuppliersByName(ctx context.Context, cl *crunch.Client, name string) ([]Obj, error) {
	var res Obj
	if err := cl.Get(ctx, "/expenses/suppliers", crunch.Query{"supplierName": name, "page": 0, "size": 20}, &res); err != nil {
		return nil, err
	}
	return crunch.List(res, "content"), nil
}

func registerSuppliers(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "suppliers", Name: "crunch_list_suppliers", Title: "List suppliers",
		Description: "List suppliers (who you buy from), with their default expense type, country and VAT number. `name` asks Crunch for an exact company name. Use the IDs when creating expenses.",
	}, func(ctx context.Context, a listSuppliersArgs) (any, error) {
		var items []Obj
		if a.Name != "" {
			var err error
			if items, err = findSuppliersByName(ctx, cl, a.Name); err != nil {
				return nil, err
			}
		} else {
			var res Obj
			if err := cl.Get(ctx, "/expenses/suppliers/all", nil, &res); err != nil {
				return nil, err
			}
			items = crunch.List(res, "content")
		}
		if !a.IncludeArchived {
			kept := items[:0]
			for _, s := range items {
				if !crunch.Bool(s, "archived") {
					kept = append(kept, s)
				}
			}
			items = kept
		}
		crunch.SortBy(items, "companyName")
		if !a.Raw {
			items = mapItems(items, crunch.SummarizeSupplier)
		}
		return a.result(items), nil
	})

	add(c, Def{
		Toolset: "suppliers", Name: "crunch_get_supplier", Title: "Get supplier",
		Description: "Get a supplier in full.",
	}, func(ctx context.Context, a supplierIDArgs) (any, error) {
		var out any
		return out, cl.Get(ctx, path("/expenses/suppliers/%d", a.SupplierID), nil, &out)
	})

	add(c, Def{
		Toolset: "suppliers", Name: "crunch_create_supplier", Title: "Create supplier",
		Description: "Create a supplier. Only company_name is required; a default expense_type makes new expenses for this supplier quicker. Check crunch_list_suppliers first to avoid duplicates.",
		Gate:        Write,
	}, func(ctx context.Context, a createSupplierArgs) (any, error) {
		var out any
		return out, cl.Post(ctx, "/expenses/suppliers", nil, a.body(a.CompanyName), &out)
	})

	add(c, Def{
		Toolset: "suppliers", Name: "crunch_update_supplier", Title: "Update supplier",
		Description: "Change a supplier's details, or archive/restore it. Only the fields you pass change.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a updateSupplierArgs) (any, error) {
		p := path("/expenses/suppliers/%d", a.SupplierID)
		var current Obj
		if err := cl.Get(ctx, p, nil, &current); err != nil {
			return nil, err
		}
		var out any
		return out, cl.Put(ctx, p, nil, pinID(deepMerge(current, set(a.body(a.CompanyName), "archived", a.Archived)), a.SupplierID), &out)
	})

	add(c, Def{
		Toolset: "suppliers", Name: "crunch_delete_supplier", Title: "Delete supplier",
		Description: "Delete a supplier. Only possible when it has no expenses (see `deletable`); archive it with crunch_update_supplier otherwise.",
		Gate:        Delete,
	}, func(ctx context.Context, a supplierIDArgs) (any, error) {
		if err := cl.Delete(ctx, path("/expenses/suppliers/%d", a.SupplierID), nil, nil); err != nil {
			return nil, err
		}
		return Obj{"supplier_id": a.SupplierID, "deleted": true}, nil
	})
}
