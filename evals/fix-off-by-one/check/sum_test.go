package mathx

import "testing"

func TestSum(t *testing.T) {
	for n, want := range map[int]int{-1: 0, 0: 0, 1: 1, 2: 3, 10: 55} {
		if got := Sum(n); got != want {
			t.Errorf("Sum(%d) = %d, want %d", n, got, want)
		}
	}
}
