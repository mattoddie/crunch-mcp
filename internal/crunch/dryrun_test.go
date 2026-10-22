package crunch

import (
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	v, err := normalise([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDiff(t *testing.T) {
	current := decode(t, `{"id": 7, "note": "a", "amount": {"gross": 120.00, "net": 100}, "lines": [{"type": "X"}, {"type": "Y"}], "gone": "old", "nothing": null}`)
	body := decode(t, `{"id": 7, "note": "b", "amount": {"gross": 120, "net": 90}, "lines": [{"type": "X"}, {"type": "Z"}], "added": true}`)
	got, _ := json.Marshal(Diff(current, body))
	want := `{"added":{"added":true},"changed":{"amount.net":{"from":100,"to":90},"lines[1].type":{"from":"Y","to":"Z"},"note":{"from":"a","to":"b"}},"removed":{"gone":"old"}}`
	if string(got) != want {
		t.Errorf("diff:\n got %s\nwant %s", got, want)
	}
	if d := Diff(current, current); len(d) != 0 {
		t.Errorf("no changes: %v", d)
	}
}

func TestShortenFiles(t *testing.T) {
	long := strings.Repeat("A", 300)
	got, _ := json.Marshal(ShortenFiles(map[string]any{"f": "data:image/png;base64," + long, "b": long, "s": "short", "text": strings.Repeat("words ", 60) + "!"}))
	for _, want := range []string{`"f":"[image/png file, 300 characters of base64]"`, `"b":"[300 characters of base64]"`, `"s":"short"`, `"text":"words words`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}

func TestReadOnlyPost(t *testing.T) {
	if !IsReadOnlyPost("/banking/credit-cards/opening-balances") || IsReadOnlyPost("/banking/credit-cards/postOpeningBalance/1") || changes("POST", "/banking/bank-accounts/opening-balances") || !changes("PUT", "/x") || changes("GET", "/x") {
		t.Error("read-only POST detection")
	}
}
