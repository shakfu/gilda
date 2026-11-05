package units

import "testing"

func TestBytesAtEachBoundary(t *testing.T) {
	for n, want := range map[int64]string{
		0:             "0 B",
		1023:          "1023 B",
		1024:          "1.0 KiB",
		1 << 20:       "1.0 MiB",
		3 << 30:       "3.0 GiB",
		1<<20 + 1:     "1.0 MiB",
		1 << 40 * 2:   "2.0 TiB",
		1 << 40 << 10: "1024.0 TiB",
	} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}
