package crunch

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Obj is a JSON object from the API. The API's objects are passed around
// generically so fields this server doesn't know about survive updates.
type Obj = map[string]any

// Compact drops nil, "", empty slices/maps and false booleans so summaries stay small.
func Compact(o Obj) Obj {
	for k, v := range o {
		switch x := v.(type) {
		case nil:
			delete(o, k)
		case string:
			if x == "" {
				delete(o, k)
			}
		case bool:
			if !x {
				delete(o, k)
			}
		case []any:
			if len(x) == 0 {
				delete(o, k)
			}
		case []string:
			if len(x) == 0 {
				delete(o, k)
			}
		case []Obj:
			if len(x) == 0 {
				delete(o, k)
			}
		case Obj:
			if len(x) == 0 {
				delete(o, k)
			}
		}
	}
	return o
}

// Get walks nested objects: Get(o, "supplier", "companyName").
func Get(o any, path ...string) any {
	cur := o
	for _, p := range path {
		m, ok := cur.(Obj)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

// Str returns a nested string (or a number formatted as a string), or "".
func Str(o any, path ...string) string {
	switch v := Get(o, path...).(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// Bool returns a nested boolean, or false.
func Bool(o any, path ...string) bool {
	b, _ := Get(o, path...).(bool)
	return b
}

// Num returns a nested number, accepting numbers and decimal strings.
func Num(o any, path ...string) (float64, bool) {
	switch v := Get(o, path...).(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

// Money returns a nested amount rounded to the penny, or nil when absent.
func Money(o any, path ...string) any {
	f, ok := Num(o, path...)
	if !ok {
		return nil
	}
	return math.Round(f*100) / 100
}

// ID returns a nested ID as int64, or nil.
func ID(o any, path ...string) any {
	f, ok := Num(o, path...)
	if !ok {
		return nil
	}
	return int64(f)
}

// List returns a nested array of objects.
func List(o any, path ...string) []Obj {
	arr, _ := Get(o, path...).([]any)
	out := make([]Obj, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(Obj); ok {
			out = append(out, m)
		}
	}
	return out
}

// Sum adds money values to the penny.
func Sum(values []any) float64 {
	total := 0.0
	for _, v := range values {
		if f, ok := v.(float64); ok {
			total += f
		}
	}
	return math.Round(total*100) / 100
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func nonZero(v any) any {
	if n, ok := Num(Obj{"v": v}, "v"); ok && n == 0 {
		return nil
	}
	return v
}

// SummarizeExpense is the list view of an expense.
func SummarizeExpense(e Obj) Obj {
	lines := List(e, "expenseLineItems")
	var types, descs, methods []string
	for _, l := range lines {
		types = append(types, Str(l, "expenseType"))
		descs = append(descs, Str(l, "description"))
	}
	for _, p := range List(e, "supplierPayments") {
		methods = append(methods, Str(p, "paymentTarget", "paymentMethod", "paymentMethodName"))
	}
	return Compact(Obj{
		"id":          ID(e, "id"),
		"date":        Str(e, "postingDate"),
		"supplier":    Str(e, "supplier", "companyName"),
		"supplier_id": ID(e, "supplier", "id"),
		"reference":   Str(e, "supplierReference"),
		"gross":       Money(e, "amount", "grossAmount"),
		"net":         Money(e, "amount", "netAmount"),
		"vat":         Money(e, "amount", "vatAmount"),
		"outstanding": Money(e, "outstandingBalance"),
		"status":      Str(e, "status"),
		"types":       unique(types),
		"description": strings.Join(unique(descs), "; "),
		"paid_by":     unique(methods),
		"reconciled":  Bool(e, "reconciled"),
		"attachments": nonZero(len(List(e, "attachments"))),
		"recurring":   Bool(e, "createdByRecurringEvent"),
		"note":        Str(e, "note"),
	})
}

// SummarizeInvoiceState is the list view of an invoice.
func SummarizeInvoiceState(i Obj) Obj {
	currency := Str(i, "currency")
	if currency == "POUND_STERLING" {
		currency = ""
	}
	return Compact(Obj{
		"id":          ID(i, "id"),
		"number":      Str(i, "invoiceNumber"),
		"customer":    Str(i, "customerName"),
		"customer_id": ID(i, "customerId"),
		"reference":   Str(i, "reference"),
		"issued":      Str(i, "issuedDate"),
		"due":         Str(i, "dueDate"),
		"status":      Str(i, "status"),
		"net":         Money(i, "netAmount"),
		"vat":         Money(i, "vatAmount"),
		"gross":       Money(i, "grossAmount"),
		"outstanding": Money(i, "outstandingBalance"),
		"credits":     nonZero(Money(i, "totalCreditsAmount")),
		"currency":    currency,
		"deletable":   Bool(i, "deletable"),
	})
}

// SummarizeCustomer is the list view of a client.
func SummarizeCustomer(c Obj) Obj {
	var addr []string
	for _, k := range []string{"line1", "line2", "line3", "line4", "postCode", "country"} {
		if s := Str(c, "billingAddress", k); s != "" {
			addr = append(addr, s)
		}
	}
	return Compact(Obj{
		"id":                 ID(c, "id"),
		"name":               Str(c, "name"),
		"email":              Str(c, "email"),
		"telephone":          Str(c, "telephone"),
		"payment_terms_days": Get(c, "paymentTermsDays"),
		"address":            strings.Join(addr, ", "),
		"note":               Str(c, "note", "text"),
	})
}

// SummarizeSupplier is the list view of a supplier.
func SummarizeSupplier(s Obj) Obj {
	return Compact(Obj{
		"id":                   ID(s, "id"),
		"name":                 Str(s, "companyName"),
		"contact":              Str(s, "contactName"),
		"default_expense_type": Str(s, "expenseType", "expenseTypeName"),
		"country":              Str(s, "country"),
		"vat_number":           Str(s, "vatRegistrationNumber"),
		"archived":             Bool(s, "archived"),
		"deletable":            Get(s, "deletable"),
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// SummarizeBankAccount is the list view of a bank account.
func SummarizeBankAccount(b Obj) Obj {
	return Compact(Obj{
		"id":                    ID(b, "id"),
		"name":                  firstNonEmpty(Str(b, "displayName"), Str(b, "displayableName")),
		"bank":                  Str(b, "bankName"),
		"account_name":          Str(b, "accountName"),
		"sort_code":             Str(b, "sortCode"),
		"account_number":        Str(b, "accountNumber"),
		"iban":                  Str(b, "iban"),
		"balance":               Money(b, "balanceDto", "balance"),
		"balance_date":          Str(b, "balanceDto", "date"),
		"default":               Bool(b, "default"),
		"personal":              Bool(b, "personal"),
		"archived":              Bool(b, "archived"),
		"unmatched_entries":     nonZero(Get(b, "unmatchedEntriesAmount")),
		"unreconciled_payments": nonZero(Get(b, "unreconciledCrunchPaymentsAmount")),
		"bank_feed":             Str(b, "bankFeed", "state"),
	})
}

// SummarizeCreditCard is the list view of a credit card.
func SummarizeCreditCard(c Obj) Obj {
	return Compact(Obj{
		"id":                    ID(c, "id"),
		"name":                  firstNonEmpty(Str(c, "displayName"), Str(c, "displayableName")),
		"card_number":           Str(c, "cardNumber"),
		"balance":               Money(c, "balanceDto", "balance"),
		"balance_date":          Str(c, "balanceDto", "date"),
		"default":               Bool(c, "default"),
		"personal":              Bool(c, "personal"),
		"archived":              Bool(c, "archived"),
		"unmatched_entries":     nonZero(Get(c, "unmatchedEntriesAmount")),
		"unreconciled_payments": nonZero(Get(c, "unreconciledCrunchPaymentsAmount")),
	})
}

// SummarizePayment is the list view of a bank account payment.
func SummarizePayment(p Obj) Obj {
	return Compact(Obj{
		"id":                 ID(p, "id"),
		"date":               Str(p, "date"),
		"amount":             Money(p, "amount"),
		"direction":          Str(p, "amountType"),
		"type":               Str(p, "paymentType"),
		"name":               Str(p, "paymentName"),
		"description":        Str(p, "description"),
		"reference":          Str(p, "reference"),
		"statement_entry_id": ID(p, "statementEntryId"),
		"unmatchable":        Bool(p, "unmatchable"),
	})
}

// SummarizeRecurringExpense is the list view of a recurring expense.
func SummarizeRecurringExpense(r Obj) Obj {
	return Compact(Obj{
		"id":          ID(r, "id"),
		"supplier":    Str(r, "supplierName"),
		"total":       Money(r, "totalAmount"),
		"status":      Str(r, "status"),
		"next":        Str(r, "nextOccurrenceDate"),
		"description": Str(r, "description"),
	})
}

// SortBy sorts objects by a string field, case-insensitively.
func SortBy(items []Obj, field string) {
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(Str(items[i], field)) < strings.ToLower(Str(items[j], field))
	})
}

// IDString formats an ID argument for a URL path.
func IDString(id int64) string { return fmt.Sprint(id) }

// Objs keeps the objects in a decoded JSON array.
func Objs(arr []any) []Obj {
	return List(Obj{"a": arr}, "a")
}
