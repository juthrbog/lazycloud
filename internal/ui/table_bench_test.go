package ui

import (
	"fmt"
	"testing"

	"charm.land/bubbles/v2/table"
)

// Run with: go test ./internal/ui -run '^$' -bench Table -benchmem

// benchRowCount is the number of rows in each table benchmark.
const benchRowCount = 100_000

func benchRows() []Row {
	n := benchRowCount
	rows := make([]Row, n)
	for i := range rows {
		name := fmt.Sprintf("logs/2026/10/%06d/object-%d.json", (i*7919)%n, i)
		rows[i] = Row{
			Key:   name,
			Cells: table.Row{"F", name, fmt.Sprintf("%d KB", i%5000), "2026-10-11 12:00:00", "STANDARD"},
			Sort:  table.Row{"1", name, SortKeyBytes(int64(i % 5000)), "2026-10-11 12:00:00", "STANDARD"},
		}
	}
	return rows
}

func benchTable(rows []Row) Table {
	t := NewTable([]Column{
		{Title: "T", Width: 2},
		{Title: "Name", Width: 30, Weight: 3},
		{Title: "Size", Width: 10},
		{Title: "Modified", Width: 19},
		{Title: "Class", Width: 12},
	})
	t.SetSize(160, 40)
	t.Sort(1)
	t.SetRows(rows)
	return t
}

func BenchmarkTableSetRows100k(b *testing.B) {
	rows := benchRows()
	t := benchTable(nil)
	b.ResetTimer()
	for range b.N {
		t.SetRows(rows)
	}
}

func BenchmarkTableFilterKeystroke100k(b *testing.B) {
	t := benchTable(benchRows())
	qs := []string{"o", "ob", "obj", "obje", "objec", "object"}
	b.ResetTimer()
	for i := range b.N {
		t.Filter(qs[i%len(qs)])
	}
}

func BenchmarkTableToggleSelect100k(b *testing.B) {
	t := benchTable(benchRows())
	b.ResetTimer()
	for range b.N {
		t.ToggleSelect()
	}
}

// 100 pages of 1,000 rows, each added with AppendRows.
func BenchmarkTablePagedLoad100k(b *testing.B) {
	rows := benchRows()
	for range b.N {
		t := benchTable(nil)
		for p := 0; p < len(rows); p += 1000 {
			t.AppendRows(rows[p : p+1000])
		}
	}
}
