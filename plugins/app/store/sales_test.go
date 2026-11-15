package store

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
	"time"
)

const salesHead = "Provider\tProvider Country\tSKU\tDeveloper\tTitle\tVersion\tProduct Type Identifier\tUnits\tDeveloper Proceeds\tBegin Date\tEnd Date\tCustomer Currency\tCountry Code\tCurrency of Proceeds\tApple Identifier\tCustomer Price\tPromo Code\tParent Identifier\tSubscription\tPeriod\tCategory\tCMB\tDevice\tSupported Platforms\tProceeds Reason\tPreserved Pricing\tClient\tOrder Type\n"

func salesRow(sku, kind, units, proceeds, country, currency, appleID, parent string) string {
	return "APPLE\tUS\t" + sku + "\tGuedio\tX\t1.0\t" + kind + "\t" + units + "\t" + proceeds + "\t11/10/2026\t11/10/2026\t" + currency + "\t" + country + "\t" + currency + "\t" + appleID + "\t0\t \t" + parent + "\t \t \tGames\t \tiPhone\tiOS\t \t \t \t \n"
}

func sampleReport() []byte {
	return []byte(salesHead +
		salesRow("OW1", "1F", "5", "0", "ZA", "ZAR", "111", " ") +
		salesRow("OW1", "1F", "3", "0", "GB", "GBP", "111", " ") +
		salesRow("OW1", "3F", "2", "0", "ZA", "ZAR", "111", " ") +
		salesRow("OW1", "7F", "40", "0", "ZA", "ZAR", "111", " ") +
		salesRow("OW1.pro", "IA1", "2", "2.10", "GB", "GBP", "999", "OW1") +
		salesRow("OW1.pro", "IA1", "-1", "2.10", "GB", "GBP", "999", "OW1") +
		salesRow("OTHER", "1F", "70", "0", "US", "USD", "222", " ") +
		salesRow("OTHER.pro", "IA1", "9", "1.00", "US", "USD", "998", "OTHER"))
}

func TestParseSales(t *testing.T) {
	a := ParseSales(sampleReport(), "111", "OW1")
	if a.New != 8 || a.Again != 2 || a.Updates != 40 || a.Bought != 1 {
		t.Fatalf("got %+v", a)
	}
	if got := a.Proceeds["GBP"]; got < 2.09 || got > 2.11 || len(a.Proceeds) != 1 {
		t.Fatalf("proceeds %v", a.Proceeds)
	}
	if a.Countries["ZA"] != 5 || a.Countries["GB"] != 3 || len(a.Countries) != 2 {
		t.Fatalf("countries %v", a.Countries)
	}
	if b := ParseSales([]byte("not a report"), "111", "OW1"); b.New != 0 {
		t.Fatal("read numbers out of nothing")
	}
}

func TestSales(t *testing.T) {
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write(sampleReport())
	_ = zw.Close()
	var asked string
	status := 200
	get := func(u string, h map[string]string) (int, []byte, error) {
		asked = u
		if !strings.HasPrefix(h["Authorization"], "Bearer ") {
			t.Fatal("no token sent")
		}
		if status != 200 {
			return status, []byte(`{"errors":[{"detail":"There were no sales for the date specified."}]}`), nil
		}
		return 200, zipped.Bytes(), nil
	}
	a, found, err := Sales(get, "tok", " 85001234 ", "2026-11-10", "111", "OW1")
	if err != nil || !found || a.New != 8 {
		t.Fatalf("%+v %v %v", a, found, err)
	}
	for _, want := range []string{"frequency%5D=DAILY", "vendorNumber%5D=85001234", "reportDate%5D=2026-11-10", "reportType%5D=SALES"} {
		if !strings.Contains(asked, want) {
			t.Fatalf("%s missing from %s", want, asked)
		}
	}
	if _, _, _ = Sales(get, "tok", "85001234", "2026-10", "111", "OW1"); !strings.Contains(asked, "MONTHLY") {
		t.Fatal("a month wasn't asked for as monthly")
	}
	status = 404
	if _, found, err := Sales(get, "tok", "85001234", "2026-11-10", "111", "OW1"); found || err != nil {
		t.Fatal("no report should be nothing found, not an error")
	}
	status = 403
	if _, _, err := Sales(get, "tok", "85001234", "2026-11-10", "111", "OW1"); err != ErrSalesAccess {
		t.Fatalf("got %v", err)
	}
}

func TestLedger(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	l := &Ledger{}
	want := l.Wanted(now, 14)
	if len(want) != 14 || want[0] != "2026-11-11" || want[7] != "2026-11-04" || want[8] != "2025" || want[9] != "2026-10" {
		t.Fatalf("wanted %v", want)
	}
	// Yesterday's report isn't out yet: ask again next time.
	l.Put("2026-11-11", nil, false, now)
	if _, ok := l.Days["2026-11-11"]; ok {
		t.Fatal("a late report was taken as an empty day")
	}
	// An old day with nothing really is empty.
	l.Put("2026-11-05", nil, false, now)
	if _, ok := l.Days["2026-11-05"]; !ok {
		t.Fatal("an old empty day wasn't recorded")
	}
	l.Put("2026-11-10", &Agg{New: 10}, true, now)
	l.Put("2026-11-02", &Agg{New: 4}, true, now)
	l.Put("2026-10", &Agg{New: 100}, true, now)
	l.Put("2026-09", &Agg{New: 50}, true, now)
	l.Put("2025", &Agg{New: 1000}, true, now)
	if l.Since != "" {
		t.Fatal("stopped looking back too soon")
	}
	if w := l.Wanted(now, 40); !contains(w, "2024") || contains(w, "2025") || contains(w, "2026-10") {
		t.Fatalf("wanted %v", w)
	}
	l.Put("2024", nil, false, now)
	if l.Since != "2024" || contains(l.Wanted(now, 80), "2023") {
		t.Fatal("kept looking back past the first empty year")
	}
	total, _ := l.Total(now)
	if total.New != 1000+100+50+10+4 {
		t.Fatalf("total %d", total.New)
	}
	s := l.Series(now, 7, 0)
	if len(s) != 7 || s[0].Date != "2026-11-05" || !s[0].Known || s[5].Agg.New != 10 || s[6].Known {
		t.Fatalf("series %+v", s)
	}
	sum, known := Sum(s)
	if sum.New != 10 || known != 2 {
		t.Fatalf("sum %d over %d", sum.New, known)
	}
	l.Put("2026-08-01", &Agg{New: 1}, true, now)
	l.Trim(now)
	if _, ok := l.Days["2026-08-01"]; ok {
		t.Fatal("old days weren't forgotten")
	}
}

func contains(ps []Period, p Period) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}
