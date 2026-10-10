package api

import (
	"encoding/csv"
	"fmt"
	"net/http"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/sheet"
)

// wantsFile reports whether the request asks for a download (?format=csv or ?format=xlsx).
func wantsFile(r *http.Request) bool {
	f := r.URL.Query().Get("format")
	return f == "csv" || f == "xlsx"
}

// writeTable sends rows (the first row is the header) as a CSV or Excel download, per ?format.
func writeTable(w http.ResponseWriter, r *http.Request, name string, rows [][]string) {
	if r.URL.Query().Get("format") == "xlsx" {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xlsx"`, name))
		_ = sheet.WriteXLSX(w, name, rows)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, name))
	cw := csv.NewWriter(w)
	_ = cw.WriteAll(rows)
}
