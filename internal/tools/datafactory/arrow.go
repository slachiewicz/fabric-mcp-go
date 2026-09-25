package datafactory

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal256"
	"github.com/apache/arrow-go/v18/arrow/ipc"
)

// readArrow ports ArrowDataReaderService.ReadArrowStreamAsync: every column
// of every record batch, with nulls as "".
func readArrow(data []byte) querySummary {
	fail := func(err error) querySummary {
		msg := err.Error()
		zero := 0
		return querySummary{Columns: []string{}, EstimatedRowCount: &zero, StructuredSampleData: map[string][]any{}, ArrowParsingError: &msg}
	}
	r, err := ipc.NewReader(bytes.NewReader(data))
	if err != nil {
		return fail(err)
	}
	defer r.Release()
	var cols []string
	for _, f := range r.Schema().Fields() {
		cols = append(cols, f.Name)
	}
	values := make(map[string][]any, len(cols))
	for _, c := range cols {
		values[c] = []any{}
	}
	rows, batches := 0, 0
	for r.Next() {
		rec := r.RecordBatch()
		batches++
		rows += int(rec.NumRows())
		for ci := 0; ci < min(int(rec.NumCols()), len(cols)); ci++ {
			col := rec.Column(ci)
			for ri := 0; ri < col.Len(); ri++ {
				v := arrowValue(col, ri)
				if v == nil {
					v = ""
				}
				values[cols[ci]] = append(values[cols[ci]], v)
			}
		}
	}
	if err := r.Err(); err != nil {
		return fail(err)
	}
	if cols == nil {
		cols = []string{}
	}
	return querySummary{Columns: cols, EstimatedRowCount: &rows, StructuredSampleData: values, BatchCount: batches, ArrowParsingSuccess: true}
}

// arrowValue ports ExtractValueFromArray. Unlike upstream, Date32 values are
// read as days since the epoch; upstream reads them as seconds and so
// renders every date as 1970-01-01.
func arrowValue(a arrow.Array, i int) any {
	if a.IsNull(i) {
		return nil
	}
	switch a := a.(type) {
	case *array.String:
		return a.Value(i)
	case *array.LargeString:
		return a.Value(i)
	case *array.Int32:
		return a.Value(i)
	case *array.Int64:
		return a.Value(i)
	case *array.Float64:
		return a.Value(i)
	case *array.Boolean:
		return a.Value(i)
	case *array.Timestamp:
		unit := a.DataType().(*arrow.TimestampType).Unit
		return a.Value(i).ToTime(unit).UTC().Format("2006-01-02 15:04:05")
	case *array.Date32:
		return a.Value(i).ToTime().UTC().Format("2006-01-02")
	case *array.Date64:
		return a.Value(i).ToTime().UTC().Format("2006-01-02")
	case *array.Decimal128:
		return a.Value(i).ToString(a.DataType().(*arrow.Decimal128Type).Scale)
	case *array.Decimal256:
		scale := a.DataType().(*arrow.Decimal256Type).Scale
		return decimal256.Num(a.Value(i)).ToString(scale)
	case *array.Float32:
		return a.Value(i)
	case *array.Int8:
		return a.Value(i)
	case *array.Int16:
		return a.Value(i)
	case *array.Uint8:
		return a.Value(i)
	case *array.Uint16:
		return a.Value(i)
	case *array.Uint32:
		return a.Value(i)
	case *array.Uint64:
		return a.Value(i)
	case *array.Binary:
		return base64.StdEncoding.EncodeToString(a.Value(i))
	}
	return fmt.Sprintf("[%s] - Unsupported type", a.DataType().Name())
}

// arrowDataReport ports ArrowDataExtensions.CreateArrowDataReport.
func arrowDataReport(s querySummary, contentLength int, metadata map[string]any) map[string]any {
	cols := s.Columns
	rowCount := 0
	if len(cols) > 0 {
		rowCount = len(s.StructuredSampleData[cols[0]])
	}
	columns := make([]map[string]any, 0, len(cols))
	for _, c := range cols {
		columns = append(columns, map[string]any{"name": c, "dataType": inferDataType(s.StructuredSampleData[c])})
	}
	rows := make([]map[string]string, 0, rowCount)
	for i := range rowCount {
		row := make(map[string]string, len(cols))
		for _, c := range cols {
			if v := s.StructuredSampleData[c]; i < len(v) {
				row[c] = dotnetString(v[i])
			} else {
				row[c] = ""
			}
		}
		rows = append(rows, row)
	}
	return map[string]any{
		"table": map[string]any{
			"format":      "Table",
			"rowCount":    rowCount,
			"columnCount": len(cols),
			"summary":     fmt.Sprintf("%d rows × %d columns", rowCount, len(cols)),
			"columns":     columns,
			"rows":        rows,
		},
		"executionSummary": map[string]any{
			"success":           true,
			"contentType":       "application/octet-stream",
			"contentLength":     contentLength,
			"dataSize":          formatBytes(contentLength),
			"executionMetadata": metadata,
		},
	}
}

// inferDataType names the .NET type of the first non-null value, as
// upstream does; nulls were already replaced by "".
func inferDataType(values []any) string {
	for _, v := range values {
		if v == nil {
			continue
		}
		if _, ok := v.(int32); ok {
			return "Int32"
		}
		return "String"
	}
	return "String"
}

// dotnetString renders v the way .NET's object.ToString does.
func dotnetString(v any) string {
	switch v := v.(type) {
	case bool:
		if v {
			return "True"
		}
		return "False"
	case float64:
		return dotnetFloat(v, 64)
	case float32:
		return dotnetFloat(float64(v), 32)
	case string:
		return v
	}
	return fmt.Sprint(v)
}

// dotnetFloat formats like .NET's shortest round-trip double.ToString():
// plain notation for exponents from -5 to 14, "E+XX" otherwise.
func dotnetFloat(f float64, bits int) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "∞"
	case math.IsInf(f, -1):
		return "-∞"
	}
	if a := math.Abs(f); a == 0 || (a >= 1e-5 && a < 1e15) {
		return strconv.FormatFloat(f, 'f', -1, bits)
	}
	return strconv.FormatFloat(f, 'E', -1, bits)
}

// executedAt formats a UTC time like System.Text.Json writes DateTime.UtcNow.
func executedAt(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.0000000Z") }
