package calc

import "testing"

func TestTotalCheck(t *testing.T) {
	if Total(1, 2, 3) != 6 || Mean(1, 2, 3) != 2 {
		t.Fatal("Total or Mean is wrong")
	}
}
