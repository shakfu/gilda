package mathx

// Sum returns 1 + 2 + ... + n, or 0 for n < 1.
func Sum(n int) int {
	total := 0
	for i := 1; i < n; i++ {
		total += i
	}
	return total
}
