package tools

import (
	"context"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

type listClientsArgs struct {
	Search string `json:"search,omitempty" jsonschema:"Case-insensitive substring filter on name, email and postcode"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"Maximum number of results"`
	Raw    bool   `json:"raw,omitempty" jsonschema:"Return the full objects from the API instead of compact summaries (larger)"`
}

type clientIDArgs struct {
	ClientID int64 `json:"client_id" jsonschema:"Client (customer) ID (see crunch_list_clients)"`
}

// Address is a client's billing address. Crunch requires line1, line2 and country.
type Address struct {
	Line1    string `json:"line1" jsonschema:"First line of the address"`
	Line2    string `json:"line2" jsonschema:"Second line (the town or city if the address is short); Crunch requires it"`
	Line3    string `json:"line3,omitempty" jsonschema:"Third line"`
	Line4    string `json:"line4,omitempty" jsonschema:"Fourth line"`
	PostCode string `json:"post_code,omitempty" jsonschema:"Postcode"`
	Country  string `json:"country" jsonschema:"Country code, for example GB (see crunch_list_countries)"`
}

func (a *Address) body() Obj {
	if a == nil {
		return nil
	}
	return set(Obj{}, "line1", a.Line1, "line2", a.Line2, "line3", a.Line3, "line4", a.Line4, "postCode", a.PostCode, "country", a.Country)
}

type clientFields struct {
	Email            string         `json:"email,omitempty" jsonschema:"Email address invoices are sent to"`
	PaymentTermsDays *int           `json:"payment_terms_days,omitempty" jsonschema:"Default payment terms in days, for example 30"`
	Note             *string        `json:"note,omitempty" jsonschema:"Internal note about the customer"`
	Extra            map[string]any `json:"extra,omitempty" jsonschema:"Additional raw fields for the request body, using Crunch's own field names (advanced). Merged last."`
}

func (f clientFields) body(name string) Obj {
	o := set(Obj{}, "name", name, "email", f.Email, "paymentTermsDays", f.PaymentTermsDays)
	if f.Note != nil {
		o["note"] = Obj{"text": *f.Note}
	}
	return o
}

type createClientArgs struct {
	Name           string  `json:"name" jsonschema:"Customer name, for example Example Customer Ltd"`
	BillingAddress Address `json:"billing_address" jsonschema:"Billing address"`
	clientFields
}

type updateClientArgs struct {
	ClientID       int64    `json:"client_id" jsonschema:"Client (customer) ID (see crunch_list_clients)"`
	Name           string   `json:"name,omitempty" jsonschema:"New name"`
	BillingAddress *Address `json:"billing_address,omitempty" jsonschema:"New billing address (replaces the whole address)"`
	clientFields
}

func registerClients(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "clients", Name: "crunch_list_clients", Title: "List clients",
		Description: "List the business's clients (the customers you invoice) with email, payment terms and billing address. Use the IDs as the customer on invoices.",
	}, func(ctx context.Context, a listClientsArgs) (any, error) {
		var res []any
		if err := cl.Get(ctx, "/clients/list", nil, &res); err != nil {
			return nil, err
		}
		items := crunch.Objs(res)
		crunch.SortBy(items, "name")
		out := filterAndLimit(items, a.Search, a.Limit, func(o Obj) []string {
			return []string{crunch.Str(o, "name"), crunch.Str(o, "email"), crunch.Str(o, "billingAddress", "postCode"), crunch.Str(o, "id")}
		})
		if !a.Raw {
			out["items"] = mapItems(out["items"].([]Obj), crunch.SummarizeCustomer)
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "clients", Name: "crunch_get_client", Title: "Get client",
		Description: "Get a client (customer) in full: contact details, billing address, payment terms and note.",
	}, func(ctx context.Context, a clientIDArgs) (any, error) {
		var out any
		return out, cl.Get(ctx, path("/clients/%d", a.ClientID), nil, &out)
	})

	add(c, Def{
		Toolset: "clients", Name: "crunch_create_client", Title: "Create client",
		Description: "Create a client (customer) to invoice. A name and a billing address with line1, line2 and country are needed. Check crunch_list_clients first to avoid duplicates.",
		Gate:        Write,
	}, func(ctx context.Context, a createClientArgs) (any, error) {
		body := merge(set(a.body(a.Name), "billingAddress", a.BillingAddress.body()), a.Extra)
		var out any
		return out, cl.Post(ctx, "/clients", nil, body, &out)
	})

	add(c, Def{
		Toolset: "clients", Name: "crunch_update_client", Title: "Update client",
		Description: "Change a client's details. Only the fields you pass change; the rest are kept (a new billing_address replaces the whole address).",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a updateClientArgs) (any, error) {
		p := path("/clients/%d", a.ClientID)
		var current Obj
		if err := cl.Get(ctx, p, nil, &current); err != nil {
			return nil, err
		}
		merged := deepMerge(current, a.body(a.Name))
		if a.BillingAddress != nil {
			merged["billingAddress"] = a.BillingAddress.body()
		}
		var out any
		return out, cl.Put(ctx, p, nil, merge(merged, a.Extra), &out)
	})

	add(c, Def{
		Toolset: "clients", Name: "crunch_delete_client", Title: "Delete client",
		Description: "Delete a client (customer). Crunch refuses if the client has invoices.",
		Gate:        Delete,
	}, func(ctx context.Context, a clientIDArgs) (any, error) {
		if err := cl.Delete(ctx, path("/clients/%d", a.ClientID), nil, nil); err != nil {
			return nil, err
		}
		return Obj{"client_id": a.ClientID, "deleted": true}, nil
	})
}
