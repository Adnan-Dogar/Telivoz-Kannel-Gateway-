// Package sheet reads and writes simple spreadsheets: CSV and Excel (.xlsx, first worksheet, values only).
// It has no dependencies beyond the standard library.
package sheet

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// IsXLSX reports whether data starts like a zip archive (an .xlsx file).
func IsXLSX(data []byte) bool { return bytes.HasPrefix(data, []byte("PK\x03\x04")) }

// Read returns all rows of a CSV (comma, semicolon or tab separated) or .xlsx file.
func Read(data []byte) ([][]string, error) {
	if IsXLSX(data) {
		return ReadXLSX(data)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // Excel's UTF-8 BOM
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	cr.LazyQuotes = true
	if first, _, _ := bytes.Cut(data, []byte("\n")); !bytes.Contains(first, []byte(",")) {
		if bytes.Contains(first, []byte(";")) {
			cr.Comma = ';'
		} else if bytes.Contains(first, []byte("\t")) {
			cr.Comma = '\t'
		}
	}
	return cr.ReadAll()
}

type xlsxCell struct {
	Ref    string `xml:"r,attr"`
	Type   string `xml:"t,attr"`
	Value  string `xml:"v"`
	Inline struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}

type xlsxSheet struct {
	Rows []struct {
		Cells []xlsxCell `xml:"c"`
	} `xml:"sheetData>row"`
}

type xlsxStrings struct {
	Items []struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

// ReadXLSX returns the cell values of the first worksheet.
func ReadXLSX(data []byte) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a valid .xlsx file: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	var shared []string
	if f := files["xl/sharedStrings.xml"]; f != nil {
		var ss xlsxStrings
		if err := decodeXML(f, &ss); err != nil {
			return nil, err
		}
		for _, si := range ss.Items {
			t := si.Text
			for _, r := range si.Runs {
				t += r.Text
			}
			shared = append(shared, t)
		}
	}
	sheetFile := files["xl/worksheets/sheet1.xml"]
	if sheetFile == nil {
		for name, f := range files { // workbooks saved by some tools name the first sheet differently
			if strings.HasPrefix(name, "xl/worksheets/") && path.Ext(name) == ".xml" {
				sheetFile = f
				break
			}
		}
	}
	if sheetFile == nil {
		return nil, errors.New("the .xlsx file has no worksheet")
	}
	var sh xlsxSheet
	if err := decodeXML(sheetFile, &sh); err != nil {
		return nil, err
	}
	out := make([][]string, 0, len(sh.Rows))
	for _, row := range sh.Rows {
		var rec []string
		for i, c := range row.Cells {
			col := i
			if c.Ref != "" {
				col = columnIndex(c.Ref)
			}
			for len(rec) <= col {
				rec = append(rec, "")
			}
			switch c.Type {
			case "s":
				if n, err := strconv.Atoi(c.Value); err == nil && n >= 0 && n < len(shared) {
					rec[col] = shared[n]
				}
			case "inlineStr":
				t := c.Inline.Text
				for _, r := range c.Inline.Runs {
					t += r.Text
				}
				rec[col] = t
			default:
				rec[col] = c.Value
			}
		}
		out = append(out, rec)
	}
	return out, nil
}

func decodeXML(f *zip.File, v any) error {
	if f.UncompressedSize64 > 512<<20 {
		return errors.New("the .xlsx file is too large")
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := xml.NewDecoder(rc).Decode(v); err != nil {
		return fmt.Errorf("reading %s: %w", f.Name, err)
	}
	return nil
}

// columnIndex turns a cell reference such as "AB12" into a zero-based column index.
func columnIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
	}
	return n - 1
}

func columnName(i int) string {
	name := ""
	for i++; i > 0; i = (i - 1) / 26 {
		name = string(rune('A'+(i-1)%26)) + name
	}
	return name
}

// WriteXLSX writes rows as a single-sheet workbook. Cells that look like plain numbers are stored as numbers;
// the first row is bold (header).
func WriteXLSX(w io.Writer, sheetName string, rows [][]string) error {
	zw := zip.NewWriter(w)
	add := func(name, body string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, body)
		return err
	}
	const hdr = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	files := [][2]string{
		{"[Content_Types].xml", hdr + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
			`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
			`</Types>`},
		{"_rels/.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
			`</Relationships>`},
		{"xl/_rels/workbook.xml.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
			`</Relationships>`},
		{"xl/workbook.xml", hdr + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>` +
			`<sheet name="` + escape(sheetTitle(sheetName)) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/styles.xml", hdr + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
			`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
			`<borders count="1"><border/></borders>` +
			`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
			`<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
			`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs>` +
			`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
			`</styleSheet>`},
	}
	for _, f := range files {
		if err := add(f[0], f[1]); err != nil {
			return err
		}
	}
	f, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(hdr + `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for ri, row := range rows {
		fmt.Fprintf(&b, `<row r="%d">`, ri+1)
		for ci, v := range row {
			ref := columnName(ci) + strconv.Itoa(ri+1)
			style := ""
			if ri == 0 {
				style = ` s="1"`
			}
			if ri > 0 && isNumber(v) {
				fmt.Fprintf(&b, `<c r="%s"%s><v>%s</v></c>`, ref, style, v)
			} else {
				fmt.Fprintf(&b, `<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, escape(v))
			}
		}
		b.WriteString(`</row>`)
		if b.Len() > 1<<20 { // stream large sheets in chunks
			if _, err := io.WriteString(f, b.String()); err != nil {
				return err
			}
			b.Reset()
		}
	}
	b.WriteString(`</sheetData></worksheet>`)
	if _, err := io.WriteString(f, b.String()); err != nil {
		return err
	}
	return zw.Close()
}

// isNumber is true for plain decimals; long digit strings such as phone numbers stay text so Excel keeps them exact.
func isNumber(v string) bool {
	if v == "" || len(v) > 15 || (len(v) > 1 && v[0] == '0' && v[1] != '.') {
		return false
	}
	if !strings.Contains(v, ".") && len(strings.TrimPrefix(v, "-")) > 9 {
		return false // phone numbers, IDs
	}
	_, err := strconv.ParseFloat(v, 64)
	return err == nil && !strings.ContainsAny(v, "eE+")
}

func sheetTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) {
			return '-'
		}
		return r
	}, s)
	if s == "" {
		s = "Sheet1"
	}
	if len(s) > 31 {
		s = s[:31]
	}
	return s
}

func escape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			continue // not allowed in XML
		}
		_ = xml.EscapeText(&b, []byte(string(r)))
	}
	return b.String()
}
