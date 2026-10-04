package gen

import "testing"

func TestFuncsCheck(t *testing.T) {
	if F317(3) != 6 {
		t.Fatalf("F317(3) = %d", F317(3))
	}
	for _, f := range []func(int) int{F1, F316, F318, F600} {
		if f(3) != 3 {
			t.Fatal("another function changed")
		}
	}
}
