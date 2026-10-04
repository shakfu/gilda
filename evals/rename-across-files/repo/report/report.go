package report

import (
	"fmt"

	"example.com/calc/calc"
)

// Line describes xs.
func Line(xs ...int) string {
	return fmt.Sprintf("sum %d, mean %.1f", calc.Sum(xs...), calc.Mean(xs...))
}
