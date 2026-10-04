package calc

// Mean is the average of xs, or 0 for none.
func Mean(xs ...int) float64 {
	if len(xs) == 0 {
		return 0
	}
	return float64(Sum(xs...)) / float64(len(xs))
}
