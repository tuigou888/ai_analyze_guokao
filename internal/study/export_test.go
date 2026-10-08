package study

import "testing"

func TestSafeCSVCellFormulaPrefixes(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{"=1+2", "'=1+2"}, {"+SUM(A1:A2)", "'+SUM(A1:A2)"}, {"-1+2", "'-1+2"}, {"@SUM(A1)", "'@SUM(A1)"},
		{" \t=1+2", "' \t=1+2"}, {"\ufeff=1", "'\ufeff=1"}, {"　=1", "'　=1"}, {"\nplain", "'\nplain"}, {"\rplain", "'\rplain"}, {"\tplain", "'\tplain"},
		{"A", "A"}, {"", ""}, {"中文,引号\"和\n换行", "中文,引号\"和\n换行"}, {"42", "42"},
	} {
		if got := safeCSVCell(test.in); got != test.want {
			t.Errorf("%q got %q want %q", test.in, got, test.want)
		}
	}
}
