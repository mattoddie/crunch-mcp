package tools

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

func summarizeAddress(a any) string {
	var parts []string
	for _, k := range []string{"addressLine1", "addressCity", "addressPostCode", "addressCountry"} {
		if s := crunch.Str(a, k); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

func summarizeCompany(c Obj) Obj {
	schemes := crunch.List(c, "vatSchemeChanges")
	now := time.Now()
	var current Obj
	for _, s := range schemes {
		from, errFrom := time.Parse(time.RFC3339, crunch.Str(s, "dateFrom"))
		to, errTo := time.Parse(time.RFC3339, crunch.Str(s, "dateTo"))
		if (errFrom != nil || !from.After(now)) && (errTo != nil || !to.Before(now)) {
			current = s
			break
		}
	}
	out := crunch.Compact(Obj{
		"company_name":            crunch.Str(c, "companyName"),
		"company_number":          crunch.Str(c, "companyNumber"),
		"vat_registration_number": crunch.Str(c, "vatRegistrationNumber"),
		"vat_scheme":              crunch.Str(current, "vatScheme"),
		"vat_industry":            crunch.Str(current, "industry"),
		"limited_cost_trader":     crunch.Bool(current, "limitedCostTrader"),
		"business_address":        summarizeAddress(c["businessAddress"]),
		"registered_address":      summarizeAddress(c["registeredAddress"]),
	})
	if len(schemes) > 1 {
		out["vat_scheme_history"] = schemes
	}
	return out
}

type authCompleteArgs struct {
	RedirectedURL string `json:"redirected_url" jsonschema:"The full URL from the browser's address bar after clicking Allow (it contains code= and state=), or just the code"`
}

type rawArgs struct {
	Raw bool `json:"raw,omitempty" jsonschema:"Return the full objects from the API instead of compact summaries (larger)"`
}

func registerAccount(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "account", Name: "crunch_auth_status", Title: "Crunch login status",
		Description: "Show whether this server is logged in to Crunch: where the tokens come from, the granted scopes, when the access token expires and whether a refresh token is available. Never shows the tokens themselves.",
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		return cl.Auth.Status(), nil
	})

	add(c, Def{
		Toolset: "account", Name: "crunch_auth_start", Title: "Start Crunch login",
		Description: "Start logging this server in to a Crunch account (OAuth 2.0 with PKCE). Returns a URL for the user to open and approve. After they click Allow, the browser is sent to the redirect URI; " +
			"if nothing is listening there the page fails to load, which is fine: ask the user to copy the full URL from the address bar and pass it to crunch_auth_complete. " +
			"Logging in replaces the current tokens, possibly with a different Crunch account.",
		ReadOnly: boolPtr(false), Destructive: boolPtr(false),
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		login, err := cl.Auth.StartLogin()
		if err != nil {
			return nil, err
		}
		return Obj{
			"url": login.URL, "state": login.State, "redirect_uri": login.RedirectURI, "scopes": login.Scopes,
			"next_step": "Open the URL, log in to Crunch and click Allow. Then copy the whole address your browser ends up on (it contains code=...) and call crunch_auth_complete with it within 15 minutes. The code can only be used once.",
		}, nil
	})

	add(c, Def{
		Toolset: "account", Name: "crunch_auth_complete", Title: "Finish Crunch login",
		Description: "Finish a login started with crunch_auth_start by exchanging the authorisation code for tokens, which are saved to the token file. Pass the full URL the browser was redirected to (or just the code).",
		ReadOnly:    boolPtr(false), Destructive: boolPtr(false),
	}, func(ctx context.Context, a authCompleteArgs) (any, error) {
		res, err := cl.Auth.CompleteLogin(ctx, a.RedirectedURL)
		if err != nil {
			return nil, err
		}
		var user Obj
		if cl.Get(ctx, "/user-details", nil, &user) == nil {
			// Not fatal if this fails: the login itself worked.
			res["account"] = crunch.Compact(Obj{"email": crunch.Str(user, "email"), "name": strings.TrimSpace(crunch.Str(user, "firstName") + " " + crunch.Str(user, "lastName"))})
		}
		return res, nil
	})

	add(c, Def{
		Toolset: "account", Name: "crunch_get_user_details", Title: "Get user details",
		Description: "Get the logged-in Crunch user: ID, email address, first and last name.",
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		var out any
		return out, cl.Get(ctx, "/user-details", nil, &out)
	})

	add(c, Def{
		Toolset: "account", Name: "crunch_get_company_details", Title: "Get company details",
		Description: "Get the business's details from Crunch: company name and number, VAT registration number, current VAT scheme (for example flat rate, with industry and limited cost trader status), and business and registered addresses.",
	}, func(ctx context.Context, a rawArgs) (any, error) {
		var res Obj
		if err := cl.Get(ctx, "/company-details", nil, &res); err != nil {
			return nil, err
		}
		if a.Raw {
			return res, nil
		}
		companies := crunch.List(res, "content")
		if len(companies) == 1 {
			return summarizeCompany(companies[0]), nil
		}
		return mapItems(companies, summarizeCompany), nil
	})

	add(c, Def{
		Toolset: "account", Name: "crunch_get_overview", Title: "Business overview",
		Description: "A one-call snapshot of the business: company and VAT details, bank account and credit card balances, sales invoice totals (invoiced and outstanding) and open accounting periods. A good first call for questions like \"how is my business doing?\".",
	}, func(ctx context.Context, _ NoArgs) (any, error) {
		if !cl.Auth.HasCredentials() {
			return nil, crunch.ErrNotLoggedIn
		}
		var (
			wg                       sync.WaitGroup
			company, invoices        Obj
			banks, cards, periods    []any
			errCompany, errBanks     error
			errCards, errInv, errPer error
		)
		wg.Add(5)
		go func() { defer wg.Done(); errCompany = cl.Get(ctx, "/company-details", nil, &company) }()
		go func() { defer wg.Done(); errBanks = cl.Get(ctx, "/banking/bank-accounts", nil, &banks) }()
		go func() { defer wg.Done(); errCards = cl.Get(ctx, "/banking/credit-cards/cards/false", nil, &cards) }()
		go func() { defer wg.Done(); errInv = cl.Get(ctx, "/invoices/sales-invoice-totals", nil, &invoices) }()
		go func() { defer wg.Done(); errPer = cl.Get(ctx, "/expenses/ledgers", nil, &periods) }()
		wg.Wait()

		var errs []string
		for i, err := range []error{errCompany, errBanks, errCards, errInv, errPer} {
			if err != nil {
				errs = append(errs, []string{"company", "bank accounts", "credit cards", "invoice totals", "accounting periods"}[i]+": "+err.Error())
			}
		}
		if len(errs) == 5 {
			return nil, errorList(errs)
		}
		out := Obj{}
		if companies := crunch.List(company, "content"); len(companies) > 0 {
			out["company"] = summarizeCompany(companies[0])
		}
		var bankList []Obj
		var balances []any
		for _, b := range crunch.Objs(banks) {
			if crunch.Bool(b, "archived") {
				continue
			}
			s := crunch.SummarizeBankAccount(b)
			bankList = append(bankList, s)
			balances = append(balances, s["balance"])
		}
		if len(bankList) > 0 {
			out["bank_accounts"] = bankList
			out["total_bank_balance"] = crunch.Sum(balances)
		}
		if cardList := mapItems(crunch.Objs(cards), crunch.SummarizeCreditCard); len(cardList) > 0 {
			out["credit_cards"] = cardList
		}
		if invoices != nil {
			out["sales_invoices"] = crunch.Compact(Obj{"total_invoiced": crunch.Money(invoices, "salesTotal"), "outstanding": crunch.Money(invoices, "outstandingTotal")})
		}
		if periods != nil {
			out["open_accounting_periods"] = periods
		}
		if len(errs) > 0 {
			out["errors"] = errs
		}
		return out, nil
	})
}

type errorList []string

func (e errorList) Error() string { return strings.Join(e, "\n") }
