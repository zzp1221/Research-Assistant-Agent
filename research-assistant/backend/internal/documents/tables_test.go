package documents

import (
	"strings"
	"testing"
)

func TestExtractTablesFromPageText(t *testing.T) {
	text := strings.Join([]string{
		"Results",
		"Table 2: Accuracy comparison",
		"Model    Accuracy    F1",
		"GNN      0.91        0.88",
		"CNN      0.85        0.80",
		"",
		"Discussion follows.",
	}, "\n")
	tables := extractTablesFromPageText(3, text)
	if len(tables) != 1 {
		t.Fatalf("tables=%d (%#v)", len(tables), tables)
	}
	table := tables[0]
	if table.Page != 3 || len(table.Rows) != 3 {
		t.Fatalf("table=%#v", table)
	}
	if table.Rows[0][0] != "Model" || table.Rows[1][1] != "0.91" {
		t.Fatalf("rows=%#v", table.Rows)
	}
	if !strings.Contains(table.Caption, "Table 2") {
		t.Fatalf("caption=%q", table.Caption)
	}
	if table.Confidence <= 0 || table.Confidence > 0.9 {
		t.Fatalf("confidence=%v", table.Confidence)
	}
}

func TestExtractTablesRejectsPlainProse(t *testing.T) {
	text := "This is a normal paragraph with single spaces only.\nAnother normal line without columns.\n"
	if tables := extractTablesFromPageText(1, text); len(tables) != 0 {
		t.Fatalf("unexpected tables: %#v", tables)
	}
	// A single two-column line is not a table either.
	if tables := extractTablesFromPageText(1, "Key    Value\n"); len(tables) != 0 {
		t.Fatalf("single line should not be a table: %#v", tables)
	}
}

func TestExtractFiguresFromPageText(t *testing.T) {
	text := "Figure 3: Architecture overview\nSome body text.\n图 4 实验结果对比\n"
	figures := extractFiguresFromPageText(2, text)
	if len(figures) != 2 {
		t.Fatalf("figures=%d (%#v)", len(figures), figures)
	}
	if figures[0].Page != 2 || !strings.Contains(figures[0].Caption, "Figure 3") {
		t.Fatalf("figure=%#v", figures[0])
	}
	if !strings.Contains(figures[1].Caption, "图 4") {
		t.Fatalf("chinese caption=%#v", figures[1])
	}
}
